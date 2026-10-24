import * as vscode from "vscode";
import { Binary, discoverBinary } from "./binary";
import { registerCommands } from "./commands";
import { StatsContentProvider, VersionContentProvider } from "./content";
import { ChangeMarks } from "./decorations";
import { Ext } from "./ext";
import { promptForHook } from "./hookPrompt";
import { stopMetricsServer } from "./metricsPanel";
import { LiveStatus } from "./status";
import { CurrentFileTree } from "./trees/currentFile";
import { FilesTree } from "./trees/files";
import { LiveTree } from "./trees/live";
import { PawNode } from "./trees/nodes";
import { PinsTree } from "./trees/pins";
import { RecentTree } from "./trees/recent";
import { SessionsTree } from "./trees/sessions";
import { WatchLine } from "./types";
import { STATS_SCHEME, VERSION_SCHEME } from "./uris";
import { Watcher } from "./watcher";

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  const ext = new Ext(context);
  context.subscriptions.push(ext);

  const live = new LiveTree(ext);
  const recent = new RecentTree(ext);
  const sessions = new SessionsTree(ext);
  const files = new FilesTree(ext);
  const pins = new PinsTree(ext);
  const currentFile = new CurrentFileTree(ext);
  const historyTrees = [recent, sessions, files, pins, currentFile];

  const liveView = vscode.window.createTreeView<PawNode>("pawprints.live", { treeDataProvider: live, showCollapseAll: false });
  const sessionsView = vscode.window.createTreeView<PawNode>("pawprints.sessions", { treeDataProvider: sessions, showCollapseAll: true, canSelectMany: true });
  sessions.attach(sessionsView);
  context.subscriptions.push(
    liveView,
    sessionsView,
    vscode.window.createTreeView<PawNode>("pawprints.recent", { treeDataProvider: recent, canSelectMany: true }),
    vscode.window.createTreeView<PawNode>("pawprints.files", { treeDataProvider: files, showCollapseAll: true, canSelectMany: true }),
    vscode.window.createTreeView<PawNode>("pawprints.pins", { treeDataProvider: pins, canSelectMany: true }),
    vscode.window.createTreeView<PawNode>("pawprints.currentFile", { treeDataProvider: currentFile, canSelectMany: true })
  );

  const status = new LiveStatus(liveView);
  const marks = new ChangeMarks(ext);
  context.subscriptions.push(
    status,
    marks,
    vscode.workspace.registerTextDocumentContentProvider(VERSION_SCHEME, new VersionContentProvider(ext)),
    vscode.workspace.registerTextDocumentContentProvider(STATS_SCHEME, new StatsContentProvider(ext)),
    vscode.languages.registerCodeLensProvider({ scheme: "file" }, marks)
  );

  registerCommands(context, { ext, sessionsTree: sessions });

  // The view menus show "View as Tree" or "View as List" from this key.
  const syncLayoutContext = () =>
    void vscode.commands.executeCommand("setContext", "pawprints.fileLayout", ext.config<string>("fileLayout", "list"));
  syncLayoutContext();
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("pawprints.fileLayout")) {
        syncLayoutContext();
      }
    })
  );

  const updateLive = async () => {
    live.refresh();
    try {
      status.update(await live.load());
    } catch (e) {
      ext.log(`live sessions: ${e instanceof Error ? e.message : String(e)}`);
    }
  };

  context.subscriptions.push(
    ext.onDidChangeTrees(() => {
      for (const tree of historyTrees) {
        tree.refresh();
      }
      marks.invalidate();
    }),
    ext.onDidChangeLive(() => void updateLive())
  );

  const watchers: Watcher[] = [];
  const stopWatchers = () => {
    for (const w of watchers.splice(0)) {
      w.dispose();
    }
  };

  const onLine = (line: WatchLine, root: string | undefined) => {
    if (line.kind === "event") {
      const eventRoot = root ?? line.project;
      ext.invalidate(eventRoot);
      marks.invalidate(eventRoot, line.event.path);
      ext.debounce("trees", 300, () => ext.refreshTrees());
      ext.debounce("live", 300, () => ext.refreshLive());
    } else if (line.kind === "session") {
      ext.invalidate(root ?? line.project);
      ext.debounce("live", 300, () => ext.refreshLive());
    }
  };

  const startWatchers = () => {
    stopWatchers();
    if (!ext.binary) {
      return;
    }
    const binary = ext.binary;
    const log = (m: string) => ext.log(m);
    for (const root of ext.roots()) {
      watchers.push(new Watcher({ binary, root, onLine, log }));
    }
    if (ext.showAllProjects) {
      watchers.push(new Watcher({ binary, root: ext.roots()[0], allProjects: true, onLine: (line) => onLine(line, undefined), log }));
    }
    for (const w of watchers) {
      w.start();
    }
  };
  context.subscriptions.push({ dispose: stopWatchers });

  // quiet is for the background re-checks while the binary is missing, so a
  // window opened before installing the binary starts working without a reload.
  const findBinary = async (quiet = false) => {
    const hadBinary = Boolean(ext.binary);
    const configured = ext.config<string>("binaryPath", "");
    const found = await discoverBinary(context, configured, ext.output, quiet);
    if (quiet && !found) {
      return;
    }
    ext.setBinary(found ? new Binary(found, ext.output, { home: () => ext.home }) : undefined);
    if (found) {
      ext.log(`using binary ${found}`);
      void checkVersion(ext, context.extension.packageJSON.version as string);
    }
    startWatchers();
    if (found && !hadBinary) {
      ext.refresh();
      ext.refreshLive();
    }
  };

  const recheck = setInterval(() => {
    if (!ext.binary) {
      void findBinary(true);
    }
  }, 15_000);
  context.subscriptions.push(
    { dispose: () => clearInterval(recheck) },
    vscode.window.onDidChangeWindowState((state) => {
      if (state.focused && !ext.binary) {
        void findBinary(true);
      }
    })
  );

  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("pawprints.binaryPath") || e.affectsConfiguration("pawprints.home")) {
        void findBinary();
        return;
      }
      if (e.affectsConfiguration("pawprints.showAllProjects")) {
        startWatchers();
        ext.refreshLive();
      }
      if (e.affectsConfiguration("pawprints.recentLimit") || e.affectsConfiguration("pawprints.fileVersions") || e.affectsConfiguration("pawprints.fileLayout")) {
        ext.refresh();
      }
      if (e.affectsConfiguration("pawprints.showGutterMarks") || e.affectsConfiguration("pawprints.showCodeLens")) {
        marks.invalidate();
      }
    }),
    vscode.workspace.onDidChangeWorkspaceFolders(() => {
      startWatchers();
      ext.refresh();
    }),
    vscode.window.onDidChangeActiveTextEditor((editor) => {
      currentFile.setEditor(editor);
      if (editor) {
        void marks.decorate(editor);
      }
    }),
    vscode.window.onDidChangeVisibleTextEditors(() => marks.decorateVisible()),
    vscode.workspace.onDidSaveTextDocument((doc) => {
      const location = ext.locate(doc.uri);
      if (location) {
        marks.invalidate(location.root, location.relativePath);
      }
    })
  );

  await findBinary();
  currentFile.setEditor(vscode.window.activeTextEditor);
  void promptForHook(ext);
}

/** The extension and binary share a version number; a mismatch still works but is worth a warning. */
async function checkVersion(ext: Ext, extensionVersion: string): Promise<void> {
  try {
    const info = await ext.requireBinary().jsonOne<{ version: string }>(["version"]);
    const binaryVersion = (info.version ?? "").replace(/^v/, "");
    ext.log(`binary version ${info.version}, extension ${extensionVersion}`);
    if (binaryVersion && binaryVersion !== "dev" && binaryVersion !== extensionVersion) {
      void vscode.window.showWarningMessage(
        `Pawprints: binary is ${info.version} but the extension is ${extensionVersion}. They should match; run install.sh to update both.`
      );
    }
  } catch (e) {
    ext.log(`version check failed: ${e instanceof Error ? e.message : String(e)}`);
  }
}

export function deactivate(): void {
  stopMetricsServer();
}
