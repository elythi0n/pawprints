import { ChildProcess } from "node:child_process";
import type * as vscode from "vscode";
import { HEARTBEAT_TIMEOUT_MS, ReconnectPolicy, backoffDelay } from "./backoff";
import type { Binary } from "./binary";
import { LineSplitter, parseJsonLine } from "./ndjson";
import { WatchLine } from "./types";

export interface WatcherOptions {
  binary: Pick<Binary, "spawnLong">;
  root?: string;
  allProjects?: boolean;
  onLine: (line: WatchLine, root: string | undefined) => void;
  log: (message: string) => void;
  /** Restart delay for the n-th consecutive failure; defaults to the exponential backoff. */
  delay?: (attempt: number) => number;
  heartbeatTimeoutMs?: number;
}

/** Keeps one `pawprints watch --json` process alive, restarting it with backoff. */
export class Watcher implements vscode.Disposable {
  private child: ChildProcess | undefined;
  private restartTimer: NodeJS.Timeout | undefined;
  private heartbeatTimer: NodeJS.Timeout | undefined;
  private readonly policy = new ReconnectPolicy();
  private disposed = false;

  constructor(private readonly options: WatcherOptions) {}

  get restartAttempts(): number {
    return this.policy.attempts;
  }

  get running(): boolean {
    return this.child !== undefined;
  }

  start(): void {
    if (this.disposed || this.child) {
      return;
    }
    const args = this.options.allProjects ? ["watch", "--all"] : ["watch"];
    let child: ChildProcess;
    try {
      child = this.options.binary.spawnLong(args, this.options.root);
    } catch (e) {
      this.options.log(`watch failed to start: ${String(e)}`);
      this.scheduleRestart();
      return;
    }
    this.child = child;
    const splitter = new LineSplitter();
    child.stdout?.setEncoding("utf8");
    child.stdout?.on("data", (chunk: string) => {
      this.armHeartbeat();
      for (const line of splitter.push(chunk)) {
        this.handleLine(line);
      }
    });
    child.stderr?.on("data", (chunk: Buffer) => this.options.log(`[watch stderr] ${chunk.toString().trim()}`));
    child.on("error", (e) => {
      this.options.log(`watch error: ${e.message}`);
      if (this.child === child) {
        this.child = undefined;
        this.clearHeartbeat();
        this.scheduleRestart();
      }
    });
    child.on("close", (code) => {
      if (this.child !== child) {
        return;
      }
      this.child = undefined;
      this.clearHeartbeat();
      if (!this.disposed) {
        this.options.log(`watch exited with code ${code}`);
        this.scheduleRestart();
      }
    });
    this.armHeartbeat();
  }

  private handleLine(line: string): void {
    const value = parseJsonLine(line) as WatchLine | undefined;
    if (!value || typeof value !== "object" || !("kind" in value)) {
      return;
    }
    this.policy.reset();
    this.options.onLine(value, this.options.root);
  }

  private armHeartbeat(): void {
    this.clearHeartbeat();
    this.heartbeatTimer = setTimeout(() => {
      this.options.log("watch heartbeat missing, restarting");
      this.kill();
      this.scheduleRestart();
    }, this.options.heartbeatTimeoutMs ?? HEARTBEAT_TIMEOUT_MS);
  }

  private clearHeartbeat(): void {
    if (this.heartbeatTimer) {
      clearTimeout(this.heartbeatTimer);
      this.heartbeatTimer = undefined;
    }
  }

  private scheduleRestart(): void {
    if (this.disposed || this.restartTimer) {
      return;
    }
    this.policy.nextDelay();
    const delay = (this.options.delay ?? backoffDelay)(this.policy.attempts);
    this.options.log(`watch restart in ${delay} ms`);
    this.restartTimer = setTimeout(() => {
      this.restartTimer = undefined;
      this.start();
    }, delay);
  }

  private kill(): void {
    const child = this.child;
    this.child = undefined;
    this.clearHeartbeat();
    if (child && !child.killed) {
      child.kill();
    }
  }

  dispose(): void {
    this.disposed = true;
    if (this.restartTimer) {
      clearTimeout(this.restartTimer);
      this.restartTimer = undefined;
    }
    this.kill();
  }
}
