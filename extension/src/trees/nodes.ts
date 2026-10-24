import * as vscode from "vscode";
import { EventFilter } from "../data";
import {
  basename,
  clockTime,
  countsLabel,
  dateTime,
  dirname,
  isDeletion,
  isNewFile,
  isUnchanged,
  sessionTitle,
  statusWord,
  timeAgo,
  toolName,
  truncate,
  versionDescription,
  versionLabel,
} from "../format";
import { DirTree } from "../treeModel";
import { PawEvent, Pin, Session, SubagentInfo, Turn } from "../types";

export type PawNode = FolderNode | MessageNode | SessionNode | TurnNode | FileNode | VersionNode | PinNode | DirNode | WhoNode;

export class FolderNode extends vscode.TreeItem {
  override readonly contextValue = "folder";

  constructor(readonly root: string) {
    super(basename(root), vscode.TreeItemCollapsibleState.Expanded);
    this.iconPath = new vscode.ThemeIcon("root-folder");
    this.tooltip = root;
  }
}

export class MessageNode extends vscode.TreeItem {
  override readonly contextValue = "message";

  constructor(label: string, icon = "info", command?: vscode.Command) {
    super(label, vscode.TreeItemCollapsibleState.None);
    this.iconPath = new vscode.ThemeIcon(icon);
    this.command = command;
  }
}

export function binaryNotFoundNode(): MessageNode {
  return new MessageNode("Pawprints binary not found", "warning", {
    command: "pawprints.binaryNotFound",
    title: "Pawprints binary not found",
  });
}

const statusIcons: Record<string, string> = {
  busy: "loading~spin",
  waiting: "bell-dot",
  idle: "circle-outline",
  ended: "circle-slash",
  unknown: "question",
};

export class SessionNode extends vscode.TreeItem {
  override readonly contextValue = "session";

  constructor(
    readonly root: string,
    readonly session: Session,
    readonly live: boolean,
    readonly parent?: PawNode
  ) {
    super(sessionTitle(session), live ? vscode.TreeItemCollapsibleState.None : vscode.TreeItemCollapsibleState.Collapsed);
    this.id = `${live ? "live" : "session"}:${root}:${session.id}`;
    const counts = `${session.edits} edit${session.edits === 1 ? "" : "s"} · ${countsLabel(session.lines_added, session.lines_removed)}`;
    if (live) {
      const prompt = session.last_prompt ? ` · ${truncate(session.last_prompt, 60)}` : "";
      this.description = `${statusWord(session.status)}${prompt}`;
      this.iconPath = new vscode.ThemeIcon(statusIcons[session.status] ?? "question");
      this.command = { command: "pawprints.revealSession", title: "Reveal Session", arguments: [this] };
    } else {
      const start = session.started_at ? dateTime(session.started_at) : "";
      const end = session.last_event_at ? clockTime(session.last_event_at) : "";
      const range = start && end ? `${start} – ${end}` : start;
      this.description = `${range ? range + " · " : ""}${counts}`;
      this.iconPath = new vscode.ThemeIcon(session.alive ? (statusIcons[session.status] ?? "comment-discussion") : "comment-discussion");
    }
    const md = new vscode.MarkdownString();
    md.appendMarkdown(`**${sessionTitle(session)}**\n\n`);
    md.appendMarkdown(`Session \`${session.id}\`\n\n`);
    md.appendMarkdown(`Status: ${statusWord(session.status)}${session.status_since ? ` (${timeAgo(session.status_since)})` : ""}\n\n`);
    if (session.cwd) {
      md.appendMarkdown(`Directory: \`${session.cwd}\`\n\n`);
    }
    if (session.first_prompt) {
      md.appendMarkdown(`First prompt: ${truncate(session.first_prompt, 200)}\n\n`);
    }
    if (session.last_prompt) {
      md.appendMarkdown(`Last prompt: ${truncate(session.last_prompt, 200)}\n\n`);
    }
    md.appendMarkdown(`${counts}, ${session.files.length} file${session.files.length === 1 ? "" : "s"}`);
    this.tooltip = md;
  }
}

export class TurnNode extends vscode.TreeItem {
  override readonly contextValue = "turn";

  constructor(
    readonly root: string,
    readonly sessionId: string,
    readonly turn: Turn,
    readonly parent?: PawNode
  ) {
    super(truncate(turn.text || "(no prompt)", 80), vscode.TreeItemCollapsibleState.Collapsed);
    this.id = `turn:${root}:${sessionId}:${turn.id}`;
    this.iconPath = new vscode.ThemeIcon("comment");
    this.description = `${clockTime(turn.at)} · ${turn.edits} edit${turn.edits === 1 ? "" : "s"}`;
    const md = new vscode.MarkdownString();
    md.appendMarkdown(`${turn.text}\n\n`);
    md.appendMarkdown(`Turn \`${turn.id}\` at ${dateTime(turn.at)}\n\n`);
    md.appendMarkdown(turn.files.map((f) => `- ${f}`).join("\n"));
    this.tooltip = md;
  }
}

export class FileNode extends vscode.TreeItem {
  override readonly contextValue = "file";

  constructor(
    readonly root: string,
    readonly path: string,
    readonly filter: EventFilter,
    readonly events: PawEvent[] | undefined,
    readonly parent?: PawNode
  ) {
    super(basename(path), vscode.TreeItemCollapsibleState.Collapsed);
    this.id = `file:${root}:${JSON.stringify(filter)}:${path}`;
    this.iconPath = new vscode.ThemeIcon("file");
    const dir = dirname(path);
    const count = events ? `${events.length} version${events.length === 1 ? "" : "s"}` : "";
    this.description = [dir, count].filter(Boolean).join(" · ");
    this.tooltip = path;
  }

  /** The newest event of this file, used for diffCurrent on a file node. */
  get latest(): PawEvent | undefined {
    return this.events?.[0];
  }
}

function versionIcon(event: PawEvent): vscode.ThemeIcon {
  if (event.pinned) {
    return new vscode.ThemeIcon("pinned");
  }
  if (isUnchanged(event)) {
    return new vscode.ThemeIcon("circle-outline");
  }
  if (event.kind === "external") {
    return new vscode.ThemeIcon("account");
  }
  if (event.kind === "restore" || event.kind === "undo") {
    return new vscode.ThemeIcon("history");
  }
  if (isNewFile(event)) {
    return new vscode.ThemeIcon("new-file");
  }
  if (isDeletion(event)) {
    return new vscode.ThemeIcon("trash");
  }
  if (event.kind === "background" || event.tool === "Bash") {
    return new vscode.ThemeIcon("terminal");
  }
  if (event.tool?.startsWith("mcp__")) {
    return new vscode.ThemeIcon("plug");
  }
  return new vscode.ThemeIcon("edit");
}

export function versionTooltip(event: PawEvent, prompt?: string): vscode.MarkdownString {
  const md = new vscode.MarkdownString();
  md.appendMarkdown(`**${event.path}**\n\n`);
  md.appendMarkdown(`${dateTime(event.ts)} · ${toolName(event)} · ${countsLabel(event.lines_added, event.lines_removed)}\n\n`);
  md.appendMarkdown(`Event \`${event.id}\`\n\n`);
  md.appendMarkdown(`Session \`${event.session}\`\n\n`);
  if (event.turn) {
    md.appendMarkdown(`Turn \`${event.turn}\`\n\n`);
  }
  if (prompt) {
    md.appendMarkdown(`Prompt: ${truncate(prompt, 300)}\n\n`);
  }
  if (event.kind === "external") {
    md.appendMarkdown("Changed outside Claude, between its commands\n\n");
  } else if (event.kind === "background") {
    md.appendMarkdown("Written while a background command was running\n\n");
  }
  if (event.command) {
    md.appendMarkdown("Command:\n\n");
    md.appendCodeblock(event.command, "sh");
  }
  if (event.before_unknown) {
    md.appendMarkdown("First seen: no earlier copy to compare against\n\n");
  }
  if (isUnchanged(event)) {
    md.appendMarkdown("Content unchanged\n\n");
  }
  if (event.pinned) {
    md.appendMarkdown("Pinned\n\n");
  }
  return md;
}

export class VersionNode extends vscode.TreeItem {
  override readonly contextValue: "version" | "pin";

  constructor(
    readonly root: string,
    readonly event: PawEvent,
    options: { prompt?: string; showPath?: boolean; parent?: PawNode; asFile?: boolean } = {}
  ) {
    super(options.asFile ? basename(event.path) : versionLabel(event), vscode.TreeItemCollapsibleState.None);
    this.id = `version:${root}:${event.id}:${JSON.stringify(options.parent?.id ?? "")}`;
    this.contextValue = event.pinned ? "pin" : "version";
    this.parent = options.parent;
    this.iconPath = versionIcon(event);
    const parts = [versionDescription(event)];
    if (options.showPath) {
      parts.push(event.path);
    }
    this.description = parts.filter(Boolean).join(" · ");
    if (options.asFile) {
      // A file row: where it is, then what happened to it.
      const what = event.combined ? `${event.combined} edits` : versionLabel(event);
      this.description = [dirname(event.path), what].filter((p) => p && p !== ".").join(" · ");
      if (event.combined) {
        this.iconPath = new vscode.ThemeIcon(event.pinned ? "pinned" : "diff");
      }
    }
    this.tooltip = versionTooltip(event, options.prompt);
    if (event.combined) {
      (this.tooltip as vscode.MarkdownString).appendMarkdown(
        `\n\nCombined change of ${event.combined} edits: from before the first to after the last. Set \`pawprints.fileVersions\` to \`each\` to list every version.`
      );
    }
    this.command = { command: "pawprints.diffPrevious", title: "Diff Against Previous Version", arguments: [this] };
  }

  readonly parent?: PawNode;
}

export class PinNode extends vscode.TreeItem {
  override readonly contextValue = "pin";
  private resolved: PawEvent | undefined;

  constructor(
    readonly root: string,
    readonly pin: Pin,
    readonly parent?: PawNode
  ) {
    super(pin.message || basename(pin.path), vscode.TreeItemCollapsibleState.None);
    this.id = `pin:${root}:${pin.event}`;
    this.iconPath = new vscode.ThemeIcon("pinned");
    this.description = `${pin.path} · ${dateTime(pin.at)}`;
    const md = new vscode.MarkdownString();
    md.appendMarkdown(`**${pin.path}**\n\n`);
    if (pin.message) {
      md.appendMarkdown(`${pin.message}\n\n`);
    }
    md.appendMarkdown(`Event \`${pin.event}\`\n\nPinned ${dateTime(pin.at)}`);
    this.tooltip = md;
    this.command = { command: "pawprints.diffPrevious", title: "Diff Against Previous Version", arguments: [this] };
  }

  get event(): PawEvent | undefined {
    return this.resolved;
  }

  set event(event: PawEvent | undefined) {
    this.resolved = event;
  }
}

function dirSummary(t: DirTree): string {
  const files = `${t.fileCount} file${t.fileCount === 1 ? "" : "s"}`;
  return [files, countsLabel(t.added, t.removed)].filter(Boolean).join(" · ");
}

/** One folder of a prompt's files in the tree layout. */
export class DirNode extends vscode.TreeItem {
  override readonly contextValue = "dir";

  constructor(
    readonly root: string,
    readonly tree: DirTree,
    readonly key: string,
    readonly parent?: PawNode
  ) {
    super(tree.name, vscode.TreeItemCollapsibleState.Collapsed);
    this.id = `dir:${root}:${key}:${tree.path}`;
    this.iconPath = new vscode.ThemeIcon(tree.path.startsWith("/") ? "folder-library" : "folder");
    this.description = dirSummary(tree);
    this.tooltip = `${tree.path}\n${dirSummary(tree)} · ${tree.edits} edit${tree.edits === 1 ? "" : "s"}`;
    this.resourceUri = tree.path.startsWith("/") ? vscode.Uri.file(tree.path) : vscode.Uri.file(`${root}/${tree.path}`);
  }
}

/** A group under a prompt: one subagent's changes, or changes made outside Claude. */
/** A subagent's task, else its type. */
export function agentName(agent?: SubagentInfo, type?: string): string {
  if (agent?.description) {
    return agent.description;
  }
  const kind = agent?.type || type;
  return kind ? `${kind} agent` : "subagent";
}

export class WhoNode extends vscode.TreeItem {
  override readonly contextValue: "agent" | "outside";

  constructor(
    readonly root: string,
    readonly who: "agent" | "outside",
    readonly events: PawEvent[],
    readonly key: string,
    options: { type?: string; agent?: SubagentInfo; parent?: PawNode } = {}
  ) {
    super(who === "agent" ? agentName(options.agent, options.type) : "outside Claude", vscode.TreeItemCollapsibleState.Collapsed);
    this.contextValue = who;
    this.parent = options.parent;
    this.id = `who:${root}:${key}`;
    const files = new Set(events.map((e) => e.path)).size;
    const count = `${files} file${files === 1 ? "" : "s"}`;
    if (who === "agent") {
      const agent = options.agent;
      const type = agent?.type || options.type;
      this.iconPath = new vscode.ThemeIcon("hubot");
      this.description = [agent?.description ? type : "", count, agent?.summary].filter(Boolean).join(" · ");
      const md = new vscode.MarkdownString();
      md.appendMarkdown(`**${agentName(agent, type)}**`);
      if (agent?.description && type) {
        md.appendMarkdown(` · ${type}`);
      }
      md.appendMarkdown("\n\n");
      const report = agent?.report || agent?.summary;
      if (report) {
        md.appendText(report);
      } else if (agent && !agent.stopped_at) {
        md.appendMarkdown("Still running; its report shows here when it finishes");
      }
      this.tooltip = md;
    } else {
      this.iconPath = new vscode.ThemeIcon("account");
      this.description = `${count} · changed between Claude's commands`;
      this.tooltip = "Changed on disk while Claude was not running a command: your own edits, a formatter, or Claude Code's /rewind";
    }
  }

  readonly parent?: PawNode;
}
