import * as vscode from "vscode";
import { Binary } from "./binary";
import { ProjectData } from "./data";
import { rootFor, relativeTo } from "./treeModel";

export type AutoInstallHook = "ask" | "always" | "never";

export interface FileLocation {
  root: string;
  relativePath: string;
}

/** State shared by every part of the extension: the binary, per-root caches, and refresh events. */
export class Ext {
  binary: Binary | undefined;
  readonly output: vscode.OutputChannel;
  private readonly dataByRoot = new Map<string, ProjectData>();
  private readonly treeEmitter = new vscode.EventEmitter<void>();
  private readonly liveEmitter = new vscode.EventEmitter<void>();
  private readonly binaryEmitter = new vscode.EventEmitter<void>();
  private readonly timers = new Map<string, NodeJS.Timeout>();

  readonly onDidChangeTrees = this.treeEmitter.event;
  readonly onDidChangeLive = this.liveEmitter.event;
  readonly onDidChangeBinary = this.binaryEmitter.event;

  constructor(readonly context: vscode.ExtensionContext) {
    this.output = vscode.window.createOutputChannel("Pawprints");
    context.subscriptions.push(this.output, this.treeEmitter, this.liveEmitter, this.binaryEmitter);
  }

  config<T>(key: string, fallback: T): T {
    return vscode.workspace.getConfiguration("pawprints").get<T>(key, fallback);
  }

  get recentLimit(): number {
    const n = this.config<number>("recentLimit", 200);
    return Number.isFinite(n) && n > 0 ? Math.floor(n) : 200;
  }

  get showAllProjects(): boolean {
    return this.config<boolean>("showAllProjects", false);
  }

  get showGutterMarks(): boolean {
    return this.config<boolean>("showGutterMarks", false);
  }

  get showCodeLens(): boolean {
    return this.config<boolean>("showCodeLens", true);
  }

  get autoInstallHook(): AutoInstallHook {
    return this.config<AutoInstallHook>("autoInstallHook", "ask");
  }

  get home(): string {
    return this.config<string>("home", "").trim();
  }

  roots(): string[] {
    return (vscode.workspace.workspaceFolders ?? []).filter((f) => f.uri.scheme === "file").map((f) => f.uri.fsPath);
  }

  /** Root and project-relative path for a file:// URI inside a workspace folder. */
  locate(uri: vscode.Uri): FileLocation | undefined {
    if (uri.scheme !== "file") {
      return undefined;
    }
    const root = rootFor(uri.fsPath, this.roots());
    if (!root) {
      return undefined;
    }
    return { root, relativePath: relativeTo(root, uri.fsPath) };
  }

  fileUri(root: string, relativePath: string): vscode.Uri {
    return vscode.Uri.joinPath(vscode.Uri.file(root), relativePath);
  }

  setBinary(binary: Binary | undefined): void {
    this.binary = binary;
    this.dataByRoot.clear();
    this.binaryEmitter.fire();
    this.refresh();
  }

  requireBinary(): Binary {
    if (!this.binary) {
      throw new Error("Pawprints binary not found. Run install.sh from the GitHub release or set pawprints.binaryPath.");
    }
    return this.binary;
  }

  data(root: string): ProjectData {
    const binary = this.requireBinary();
    let data = this.dataByRoot.get(root);
    if (!data || data.root !== root) {
      data = new ProjectData(binary, root);
      this.dataByRoot.set(root, data);
    }
    return data;
  }

  invalidate(root?: string): void {
    if (root === undefined) {
      for (const d of this.dataByRoot.values()) {
        d.invalidate();
      }
      return;
    }
    this.dataByRoot.get(root)?.invalidate();
  }

  refresh(): void {
    this.invalidate();
    this.treeEmitter.fire();
    this.liveEmitter.fire();
  }

  refreshTrees(): void {
    this.treeEmitter.fire();
  }

  refreshLive(): void {
    this.liveEmitter.fire();
  }

  /** Coalesces bursts of watcher lines into one refresh. */
  debounce(key: string, delayMs: number, fn: () => void): void {
    const existing = this.timers.get(key);
    if (existing) {
      clearTimeout(existing);
    }
    this.timers.set(
      key,
      setTimeout(() => {
        this.timers.delete(key);
        fn();
      }, delayMs)
    );
  }

  log(message: string): void {
    this.output.appendLine(message);
  }

  /** Runs a command body and turns any failure into an error message instead of an unhandled rejection. */
  async guard(fn: () => Promise<void> | void): Promise<void> {
    try {
      await fn();
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      this.log(`error: ${message}`);
      void vscode.window.showErrorMessage(`Pawprints: ${message}`);
    }
  }

  dispose(): void {
    for (const t of this.timers.values()) {
      clearTimeout(t);
    }
    this.timers.clear();
  }
}
