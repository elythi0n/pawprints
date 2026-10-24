import * as vscode from "vscode";
import { EXIT_MODIFIED, NOT_FOUND_MESSAGE, isBinaryError, isNoHistory } from "./binary";
import { statsUri, versionUri } from "./content";
import { Ext } from "./ext";
import { basename, clockTime, humanBytes, markdownSnippet, sessionTitle, truncate } from "./format";
import { installHook } from "./hookPrompt";
import { openMetrics } from "./metricsPanel";
import { netChanges } from "./treeModel";
import { SessionsTree } from "./trees/sessions";
import { FileNode, PinNode, SessionNode, TurnNode, VersionNode } from "./trees/nodes";
import { ChangePlan, ClearPlan, PawEvent, Session, ShowResult } from "./types";
import { referenceText } from "./uris";

interface Target {
  root: string;
  event: PawEvent;
}

interface Deps {
  ext: Ext;
  sessionsTree: SessionsTree;
}

function isTarget(value: unknown): value is Target {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as Target).root === "string" &&
    typeof (value as Target).event === "object"
  );
}

async function resolveTarget(ext: Ext, arg: unknown): Promise<Target> {
  if (arg instanceof VersionNode) {
    return { root: arg.root, event: arg.event };
  }
  if (arg instanceof PinNode) {
    if (!arg.event) {
      const shown = await ext.requireBinary().jsonOne<ShowResult>(["show", arg.pin.event], arg.root);
      arg.event = shown.event;
    }
    return { root: arg.root, event: arg.event };
  }
  if (arg instanceof FileNode) {
    if (!arg.latest) {
      throw new Error(`No versions recorded for ${arg.path}`);
    }
    return { root: arg.root, event: arg.latest };
  }
  if (isTarget(arg)) {
    return arg;
  }
  const editor = vscode.window.activeTextEditor;
  const location = editor ? ext.locate(editor.document.uri) : undefined;
  if (location) {
    const latest = await ext.data(location.root).latestEventFor(location.relativePath);
    if (latest) {
      return { root: location.root, event: latest };
    }
    throw new Error(`No Claude edits recorded for ${location.relativePath}`);
  }
  throw new Error("Select a version in the Pawprints view first");
}

function beforeUri(t: Target): vscode.Uri {
  return versionUri({ root: t.root, path: t.event.path, hash: t.event.before, event: t.event.id, label: `before ${clockTime(t.event.ts)}` });
}

function afterUri(t: Target): vscode.Uri {
  return versionUri({ root: t.root, path: t.event.path, hash: t.event.after, event: t.event.id, label: clockTime(t.event.ts) });
}

function diffTitle(path: string, left: string, right: string): string {
  return `${basename(path)} (${left} ↔ ${right})`;
}

async function pickRoot(ext: Ext, placeHolder = "Project"): Promise<string | undefined> {
  const roots = ext.roots();
  if (roots.length <= 1) {
    return roots[0];
  }
  return vscode.window.showQuickPick(roots, { placeHolder });
}

async function pickSession(ext: Ext, root: string): Promise<Session | undefined> {
  const sessions = await ext.data(root).sessions();
  if (sessions.length === 0) {
    throw new Error("No sessions recorded for this project");
  }
  const items = sessions.map((s) => ({
    label: sessionTitle(s),
    description: `${s.edits} edits · ${s.status}`,
    detail: s.last_prompt ? truncate(s.last_prompt, 100) : undefined,
    session: s,
  }));
  const picked = await vscode.window.showQuickPick(items, { placeHolder: "Session" });
  return picked?.session;
}

function describePlan(plan: ChangePlan): string {
  if (plan.changes.length === 0) {
    return "Nothing to change.";
  }
  const lines = plan.changes.map((c) => {
    const notes: string[] = [];
    if (c.to === "" || c.to === null) {
      notes.push("delete");
    }
    if (c.modified) {
      notes.push("edited by you since");
    }
    if (c.conflicts > 0) {
      notes.push(`${c.conflicts} conflict${c.conflicts === 1 ? "" : "s"}`);
    }
    return notes.length > 0 ? `${c.path}  (${notes.join(", ")})` : c.path;
  });
  return lines.join("\n");
}

/**
 * Dry-run, confirm in a modal, then apply. On exit 3 (the file was edited since Claude's write)
 * the user chooses between a three-way merge and overwriting.
 */
async function confirmAndApply(ext: Ext, root: string | undefined, baseArgs: string[], title: string): Promise<void> {
  const binary = ext.requireBinary();
  let mode: string[] = [];
  for (let attempt = 0; attempt < 3; attempt += 1) {
    let plan: ChangePlan;
    try {
      plan = await binary.jsonOne<ChangePlan>([...baseArgs, ...mode, "--dry-run"], root);
    } catch (e) {
      if (isBinaryError(e) && e.exit === EXIT_MODIFIED && mode.length === 0) {
        const choice = await vscode.window.showWarningMessage(e.message, { modal: true }, "Merge (three-way)", "Overwrite");
        if (!choice) {
          return;
        }
        mode = choice === "Overwrite" ? ["--force"] : ["--three-way"];
        continue;
      }
      throw e;
    }
    if (plan.changes.length === 0) {
      void vscode.window.showInformationMessage(`Pawprints: nothing to ${plan.kind}.`);
      return;
    }
    const verb = plan.kind === "undo" ? "Undo" : "Restore";
    const modeNote = mode[0] === "--force" ? " Your own edits will be overwritten." : mode[0] === "--three-way" ? " Your own edits are merged; conflicts become markers." : "";
    const detail = `${describePlan(plan)}\n${modeNote}`.trim();
    const ok = await vscode.window.showWarningMessage(
      `${title}: write ${plan.changes.length} file${plan.changes.length === 1 ? "" : "s"}?`,
      { modal: true, detail },
      verb
    );
    if (ok !== verb) {
      return;
    }
    try {
      const result = await binary.jsonOne<ChangePlan>([...baseArgs, ...mode, "--yes"], root);
      const conflicts = result.changes.reduce((n, c) => n + (c.conflicts ?? 0), 0);
      const summary = `Pawprints: ${result.kind} wrote ${result.changes.length} file${result.changes.length === 1 ? "" : "s"}`;
      if (conflicts > 0) {
        void vscode.window.showWarningMessage(`${summary}, ${conflicts} conflict${conflicts === 1 ? "" : "s"} left as markers`);
      } else {
        void vscode.window.showInformationMessage(summary);
      }
      ext.refresh();
      return;
    } catch (e) {
      if (isBinaryError(e) && e.exit === EXIT_MODIFIED && mode.length === 0) {
        continue;
      }
      throw e;
    }
  }
}

async function openMultiDiff(ext: Ext, root: string, title: string, events: PawEvent[]): Promise<void> {
  const changes = netChanges(events);
  if (changes.length === 0) {
    void vscode.window.showInformationMessage(`Pawprints: ${title} left no net change.`);
    return;
  }
  const triples = changes.map((c) => {
    const left = versionUri({ root, path: c.path, hash: c.before, event: c.firstEvent.id, label: `before ${clockTime(c.firstEvent.ts)}` });
    const right = versionUri({ root, path: c.path, hash: c.after, event: c.lastEvent.id, label: clockTime(c.lastEvent.ts) });
    return { resource: ext.fileUri(root, c.path), left, right, path: c.path };
  });
  try {
    await vscode.commands.executeCommand(
      "vscode.changes",
      title,
      triples.map((t) => [t.resource, t.left, t.right])
    );
  } catch (e) {
    ext.log(`vscode.changes unavailable (${e instanceof Error ? e.message : String(e)}), opening diffs one by one`);
    for (const t of triples) {
      await vscode.commands.executeCommand("vscode.diff", t.left, t.right, `${basename(t.path)} (${title})`, { preview: false });
    }
  }
}

async function contentOf(ext: Ext, t: Target): Promise<string> {
  const hash = t.event.after ?? t.event.before;
  if (!hash) {
    return "";
  }
  const bytes = await ext.requireBinary().raw(["cat", hash], t.root);
  return bytes.toString("utf8");
}

async function clearWithScope(ext: Ext, root: string | undefined, scopeArgs: string[], label: string): Promise<void> {
  const binary = ext.requireBinary();
  let plan: ClearPlan;
  try {
    plan = await binary.jsonOne<ClearPlan>(["clear", "--dry-run", ...scopeArgs], root);
  } catch (e) {
    if (isNoHistory(e)) {
      void vscode.window.showInformationMessage(`Pawprints: nothing to clear for ${label}.`);
      return;
    }
    throw e;
  }
  if (plan.events === 0) {
    void vscode.window.showInformationMessage(`Pawprints: nothing to clear for ${plan.scope || label}.`);
    return;
  }
  const detail = [
    `Scope: ${plan.scope || label}`,
    `${plan.events} event${plan.events === 1 ? "" : "s"} in ${plan.sessions} session${plan.sessions === 1 ? "" : "s"}, ${plan.files} file${plan.files === 1 ? "" : "s"}, ${humanBytes(plan.bytes)}`,
    plan.cut_at ? `Everything before ${plan.cut_at.slice(0, 10)}` : "",
    plan.kept_pins > 0 ? `${plan.kept_pins} pinned version${plan.kept_pins === 1 ? "" : "s"} kept` : "",
    "This cannot be undone.",
  ]
    .filter(Boolean)
    .join("\n");
  const ok = await vscode.window.showWarningMessage("Delete Pawprints history?", { modal: true, detail }, "Delete");
  if (ok !== "Delete") {
    return;
  }
  const result = await binary.jsonOne<ClearPlan>(["clear", "--yes", ...scopeArgs], root);
  void vscode.window.showInformationMessage(`Pawprints: deleted ${result.events} events (${humanBytes(result.bytes)}).`);
  ext.refresh();
}

interface ClearChoice extends vscode.QuickPickItem {
  args: string[] | "session";
}

const clearChoices: ClearChoice[] = [
  { label: "$(comment-discussion) This session…", description: "pick one session of this project", args: "session" },
  { label: "$(calendar) Older than 1 month", args: ["--older-than", "1m"] },
  { label: "$(calendar) Older than 3 months", args: ["--older-than", "3m"] },
  { label: "$(calendar) Older than 6 months", args: ["--older-than", "6m"] },
  { label: "$(root-folder) This project", description: "all history of the open folder", args: ["--project"] },
  { label: "$(warning) Everything", description: "every project on this machine", args: ["--all"] },
];

export function registerCommands(context: vscode.ExtensionContext, deps: Deps): void {
  const { ext, sessionsTree } = deps;

  const register = (id: string, fn: (...args: unknown[]) => Promise<void> | void) => {
    context.subscriptions.push(vscode.commands.registerCommand(id, (...args: unknown[]) => ext.guard(() => fn(...args))));
  };

  register("pawprints.diffPrevious", async (arg) => {
    const t = await resolveTarget(ext, arg);
    await vscode.commands.executeCommand(
      "vscode.diff",
      beforeUri(t),
      afterUri(t),
      diffTitle(t.event.path, `before ${clockTime(t.event.ts)}`, clockTime(t.event.ts)),
      { preview: true }
    );
  });

  register("pawprints.diffCurrent", async (arg) => {
    const t = await resolveTarget(ext, arg);
    await vscode.commands.executeCommand(
      "vscode.diff",
      afterUri(t),
      ext.fileUri(t.root, t.event.path),
      diffTitle(t.event.path, clockTime(t.event.ts), "current"),
      { preview: true }
    );
  });

  register("pawprints.compareSelected", async (arg, selection) => {
    const picked = Array.isArray(selection) ? selection : [arg];
    const targets: Target[] = [];
    for (const node of picked) {
      if (node instanceof VersionNode || node instanceof PinNode) {
        targets.push(await resolveTarget(ext, node));
      }
    }
    if (targets.length !== 2) {
      throw new Error("Select exactly two versions (ctrl+click) to compare them");
    }
    const [a, b] = targets.sort((x, y) => x.event.ts.localeCompare(y.event.ts));
    await vscode.commands.executeCommand(
      "vscode.diff",
      afterUri(a),
      afterUri(b),
      diffTitle(b.event.path, clockTime(a.event.ts), clockTime(b.event.ts)),
      { preview: true }
    );
  });

  register("pawprints.openVersion", async (arg) => {
    const t = await resolveTarget(ext, arg);
    const uri = t.event.after !== null ? afterUri(t) : beforeUri(t);
    const doc = await vscode.workspace.openTextDocument(uri);
    await vscode.window.showTextDocument(doc, { preview: true });
  });

  register("pawprints.openSession", async (arg) => {
    let root: string | undefined;
    let session: Session | undefined;
    if (arg instanceof SessionNode) {
      root = arg.root;
      session = arg.session;
    } else {
      root = await pickRoot(ext);
      session = root ? await pickSession(ext, root) : undefined;
    }
    if (!root || !session) {
      return;
    }
    const events = await ext.data(root).events({ session: session.id });
    await openMultiDiff(ext, root, `Session ${sessionTitle(session)}`, events);
  });

  register("pawprints.openTurn", async (arg) => {
    if (!(arg instanceof TurnNode)) {
      throw new Error("Select a turn in the Sessions view first");
    }
    const events = await ext.data(arg.root).events({ session: arg.sessionId, turn: arg.turn.id });
    await openMultiDiff(ext, arg.root, `Turn: ${truncate(arg.turn.text, 60)}`, events);
  });

  register("pawprints.restore", async (arg) => {
    const t = await resolveTarget(ext, arg);
    await confirmAndApply(ext, t.root, ["restore", t.event.id], `Restore ${t.event.path} to ${clockTime(t.event.ts)}`);
  });

  register("pawprints.restoreBefore", async (arg) => {
    const t = await resolveTarget(ext, arg);
    // A combined row reverts to before its first edit, not its last.
    const id = t.event.first_id ?? t.event.id;
    const what = t.event.combined ? `before these ${t.event.combined} edits` : `before ${clockTime(t.event.ts)}`;
    await confirmAndApply(ext, t.root, ["restore", id, "--before"], `Revert ${t.event.path} to ${what}`);
  });

  register("pawprints.undoLast", async () => {
    const root = await pickRoot(ext);
    await confirmAndApply(ext, root, ["undo"], "Undo the last Claude edit");
  });

  register("pawprints.undoTurn", async (arg) => {
    if (arg instanceof TurnNode) {
      await confirmAndApply(ext, arg.root, ["undo", "--turn", arg.turn.id], `Undo turn "${truncate(arg.turn.text, 50)}"`);
      return;
    }
    const root = await pickRoot(ext);
    await confirmAndApply(ext, root, ["undo", "--turn"], "Undo the last turn");
  });

  register("pawprints.undoSession", async (arg) => {
    let root: string | undefined;
    let session: Session | undefined;
    if (arg instanceof SessionNode) {
      root = arg.root;
      session = arg.session;
    } else {
      root = await pickRoot(ext);
      session = root ? await pickSession(ext, root) : undefined;
    }
    if (!root || !session) {
      return;
    }
    await confirmAndApply(ext, root, ["undo", "--session", session.id], `Undo session ${sessionTitle(session)}`);
  });

  register("pawprints.pin", async (arg) => {
    const t = await resolveTarget(ext, arg);
    const message = await vscode.window.showInputBox({ prompt: `Pin ${t.event.path} @ ${clockTime(t.event.ts)}`, placeHolder: "Why keep this version?" });
    if (message === undefined) {
      return;
    }
    const args = message.trim() ? ["pin", t.event.id, "-m", message.trim()] : ["pin", t.event.id];
    await ext.requireBinary().jsonOne(args, t.root);
    ext.refresh();
  });

  register("pawprints.unpin", async (arg) => {
    const t = await resolveTarget(ext, arg);
    await ext.requireBinary().jsonOne(["unpin", t.event.id], t.root);
    ext.refresh();
  });

  register("pawprints.copyContent", async (arg) => {
    const t = await resolveTarget(ext, arg);
    await vscode.env.clipboard.writeText(await contentOf(ext, t));
    void vscode.window.showInformationMessage(`Pawprints: copied ${t.event.path} @ ${clockTime(t.event.ts)}`);
  });

  register("pawprints.copySnippet", async (arg) => {
    const t = await resolveTarget(ext, arg);
    await vscode.env.clipboard.writeText(markdownSnippet(t.event, await contentOf(ext, t)));
    void vscode.window.showInformationMessage(`Pawprints: copied ${t.event.path} as a markdown snippet`);
  });

  register("pawprints.copyReference", async (arg) => {
    const t = await resolveTarget(ext, arg);
    await vscode.env.clipboard.writeText(referenceText(t.event.id, t.event.path));
    void vscode.window.showInformationMessage(`Pawprints: copied pawprints:${t.event.id}`);
  });

  register("pawprints.sendToTerminal", async (arg) => {
    const t = await resolveTarget(ext, arg);
    const terminal = vscode.window.activeTerminal;
    if (!terminal) {
      throw new Error("No active terminal to send the reference to");
    }
    terminal.sendText(referenceText(t.event.id, t.event.path), false);
    terminal.show(true);
  });

  register("pawprints.clear", async () => {
    const choice = await vscode.window.showQuickPick(clearChoices, { placeHolder: "What to delete" });
    if (!choice) {
      return;
    }
    const root = await pickRoot(ext);
    if (choice.args === "session") {
      if (!root) {
        throw new Error("Open a folder to pick one of its sessions");
      }
      const session = await pickSession(ext, root);
      if (session) {
        await clearWithScope(ext, root, ["--session", session.id], `session ${sessionTitle(session)}`);
      }
      return;
    }
    if (choice.args[0] === "--project" && !root) {
      throw new Error("Open a folder to clear its history");
    }
    await clearWithScope(ext, root, choice.args, choice.label);
  });

  register("pawprints.clearSession", async (arg) => {
    if (!(arg instanceof SessionNode)) {
      await vscode.commands.executeCommand("pawprints.clear");
      return;
    }
    await clearWithScope(ext, arg.root, ["--session", arg.session.id], `session ${sessionTitle(arg.session)}`);
  });

  register("pawprints.installHook", async () => {
    const result = await installHook(ext, ext.roots()[0]);
    void vscode.window.showInformationMessage(`Pawprints hook installed in ${result.settings_path}`);
  });

  register("pawprints.refresh", () => {
    ext.refresh();
  });

  register("pawprints.showLastChange", async () => {
    const editor = vscode.window.activeTextEditor;
    const location = editor ? ext.locate(editor.document.uri) : undefined;
    if (!location) {
      throw new Error("Open a file inside the workspace first");
    }
    const latest = await ext.data(location.root).latestEventFor(location.relativePath);
    if (!latest) {
      void vscode.window.showInformationMessage(`Pawprints: no Claude edits recorded for ${location.relativePath}`);
      return;
    }
    await vscode.commands.executeCommand("pawprints.diffPrevious", { root: location.root, event: latest });
  });

  register("pawprints.openTui", async () => {
    const binary = ext.requireBinary();
    const root = await pickRoot(ext);
    const terminal = vscode.window.createTerminal({
      name: "Pawprints",
      shellPath: binary.path,
      shellArgs: root ? ["--project", root] : [],
      cwd: root,
      env: ext.home ? { PAWPRINTS_HOME: ext.home } : undefined,
    });
    terminal.show();
  });

  register("pawprints.toggleAllProjects", async () => {
    const next = !ext.showAllProjects;
    await vscode.workspace.getConfiguration("pawprints").update("showAllProjects", next, vscode.ConfigurationTarget.Global);
    void vscode.window.setStatusBarMessage(`Pawprints: ${next ? "showing sessions from all projects" : "showing this workspace only"}`, 3000);
  });

  register("pawprints.openMetrics", () => openMetrics(ext, context.extensionUri));

  register("pawprints.openStats", async () => {
    const root = await pickRoot(ext);
    const doc = await vscode.workspace.openTextDocument(statsUri(root, ext.showAllProjects || !root));
    await vscode.window.showTextDocument(doc, { preview: false });
  });

  register("pawprints.revealSession", async (arg) => {
    if (!(arg instanceof SessionNode)) {
      return;
    }
    await vscode.commands.executeCommand("pawprints.sessions.focus");
    await sessionsTree.reveal(arg.root, arg.session.id);
  });

  register("pawprints.binaryNotFound", async () => {
    const choice = await vscode.window.showWarningMessage(`Pawprints binary not found. ${NOT_FOUND_MESSAGE}.`, "Open setting", "Show output");
    if (choice === "Open setting") {
      await vscode.commands.executeCommand("workbench.action.openSettings", "pawprints.binaryPath");
    } else if (choice === "Show output") {
      ext.output.show();
    }
  });

  const setLayout = async (layout: "tree" | "list") => {
    await vscode.workspace.getConfiguration("pawprints").update("fileLayout", layout, vscode.ConfigurationTarget.Global);
  };
  register("pawprints.viewAsTree", () => setLayout("tree"));
  register("pawprints.viewAsList", () => setLayout("list"));

  register("pawprints.showOutput", () => {
    ext.output.show();
  });
}
