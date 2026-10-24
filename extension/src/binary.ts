import { ChildProcess, spawn } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import type * as vscode from "vscode";
import { LineSplitter, errorOf, parseJsonLine } from "./ndjson";

export class BinaryError extends Error {
  constructor(
    message: string,
    public readonly exit: number
  ) {
    super(message);
    this.name = "BinaryError";
  }
}

/** Exit code the binary uses when a file was edited by hand since Claude's last write. */
export const EXIT_MODIFIED = 3;

export function isBinaryError(e: unknown): e is BinaryError {
  return e instanceof BinaryError;
}

/** A fresh store answers listing commands with an exit-1 "no history" error; callers treat that as empty. */
export function isNoHistory(e: unknown): boolean {
  return isBinaryError(e) && e.exit === 1 && /no history/i.test(e.message);
}

export interface BinaryEnv {
  home(): string;
}

/** All subprocess calls go through here: cwd is the project root, --json is added, NDJSON is parsed. */
export class Binary {
  constructor(
    readonly path: string,
    private readonly output: vscode.OutputChannel,
    private readonly settings: BinaryEnv
  ) {}

  env(): NodeJS.ProcessEnv {
    const env: NodeJS.ProcessEnv = { ...process.env, NO_COLOR: "1" };
    const home = this.settings.home();
    if (home) {
      env.PAWPRINTS_HOME = home;
    }
    return env;
  }

  private fullArgs(args: string[], root: string | undefined, json: boolean): string[] {
    const out = [...args];
    if (json) {
      out.splice(1, 0, "--json");
    }
    if (root) {
      out.splice(1, 0, `--project=${root}`);
    }
    return out;
  }

  private cwd(root: string | undefined): string {
    if (root && fs.existsSync(root)) {
      return root;
    }
    return os.homedir();
  }

  private log(line: string): void {
    this.output.appendLine(line);
  }

  /** Runs a command with --json and returns every parsed line. Error lines become BinaryError. */
  async json<T = unknown>(args: string[], root?: string): Promise<T[]> {
    const argv = this.fullArgs(args, root, true);
    const { stdout, stderr, code } = await this.capture(argv, root);
    const values: T[] = [];
    for (const line of stdout.split("\n")) {
      const value = parseJsonLine(line);
      if (value === undefined) {
        if (line.trim()) {
          this.log(`[stdout] ${line}`);
        }
        continue;
      }
      const err = errorOf(value);
      if (err) {
        throw new BinaryError(err.message, err.exit || code || 1);
      }
      values.push(value as T);
    }
    if (code !== 0) {
      throw new BinaryError(stderr.trim() || `pawprints ${args[0]} exited with code ${code}`, code);
    }
    return values;
  }

  async jsonOne<T>(args: string[], root?: string): Promise<T> {
    const values = await this.json<T>(args, root);
    if (values.length === 0) {
      throw new BinaryError(`pawprints ${args[0]} printed nothing`, 1);
    }
    return values[0];
  }

  /** Like json() but an empty store yields an empty list instead of an error. */
  async list<T>(args: string[], root?: string): Promise<T[]> {
    try {
      return await this.json<T>(args, root);
    } catch (e) {
      if (isNoHistory(e)) {
        return [];
      }
      throw e;
    }
  }

  /** Runs a command without --json and returns raw stdout bytes (used for cat). */
  async raw(args: string[], root?: string): Promise<Buffer> {
    const argv = this.fullArgs(args, root, false);
    return new Promise((resolve, reject) => {
      const child = spawn(this.path, argv, { cwd: this.cwd(root), env: this.env() });
      const chunks: Buffer[] = [];
      let stderr = "";
      child.stdout.on("data", (d: Buffer) => chunks.push(d));
      child.stderr.on("data", (d: Buffer) => (stderr += d.toString()));
      child.on("error", reject);
      child.on("close", (code) => {
        if (stderr.trim()) {
          this.log(`[stderr] pawprints ${argv.join(" ")}: ${stderr.trim()}`);
        }
        if (code !== 0) {
          const out = Buffer.concat(chunks).toString("utf8").trim();
          const err = errorOf(parseJsonLine(out));
          reject(new BinaryError(err?.message ?? (stderr.trim() || out || `exit ${code}`), err?.exit ?? code ?? 1));
          return;
        }
        resolve(Buffer.concat(chunks));
      });
    });
  }

  /** Spawns a long-running command such as watch; the caller owns the process. */
  spawnLong(args: string[], root?: string): ChildProcess {
    const argv = this.fullArgs(args, root, true);
    this.log(`[spawn] pawprints ${argv.join(" ")}`);
    return spawn(this.path, argv, { cwd: this.cwd(root), env: this.env(), stdio: ["ignore", "pipe", "pipe"] });
  }

  private capture(argv: string[], root: string | undefined): Promise<{ stdout: string; stderr: string; code: number }> {
    return new Promise((resolve, reject) => {
      const child = spawn(this.path, argv, { cwd: this.cwd(root), env: this.env() });
      const out = new LineSplitter();
      const lines: string[] = [];
      let stderr = "";
      child.stdout.setEncoding("utf8");
      child.stdout.on("data", (d: string) => lines.push(...out.push(d)));
      child.stderr.on("data", (d: Buffer) => (stderr += d.toString()));
      child.on("error", (e) => reject(new BinaryError(`cannot run ${this.path}: ${e.message}`, 127)));
      child.on("close", (code) => {
        const tail = out.flush();
        if (tail) {
          lines.push(tail);
        }
        if (stderr.trim()) {
          this.log(`[stderr] pawprints ${argv.join(" ")}: ${stderr.trim()}`);
        }
        resolve({ stdout: lines.join("\n"), stderr, code: code ?? 1 });
      });
    });
  }
}

export const NOT_FOUND_MESSAGE = "Run install.sh from the GitHub release or set pawprints.binaryPath";

async function runs(candidate: string, env: NodeJS.ProcessEnv): Promise<boolean> {
  return new Promise((resolve) => {
    let child: ChildProcess;
    try {
      child = spawn(candidate, ["version", "--json"], { env, stdio: ["ignore", "pipe", "ignore"] });
    } catch {
      resolve(false);
      return;
    }
    child.on("error", () => resolve(false));
    child.on("close", (code) => resolve(code === 0));
  });
}

function executableName(): string {
  return process.platform === "win32" ? "pawprints.exe" : "pawprints";
}

/** Absolute path of a command on PATH, so the terminal can use it as shellPath. */
function resolveOnPath(name: string): Promise<string | undefined> {
  const lookup = process.platform === "win32" ? "where" : "which";
  return new Promise((resolve) => {
    let out = "";
    const child = spawn(lookup, [name], { stdio: ["ignore", "pipe", "ignore"] });
    child.stdout.on("data", (d: Buffer) => (out += d.toString()));
    child.on("error", () => resolve(undefined));
    child.on("close", (code) => {
      const first = out.split(/\r?\n/).map((l) => l.trim()).find((l) => l.length > 0);
      resolve(code === 0 && first ? first : undefined);
    });
  });
}

/** Setting, then PATH, then <globalStorage>/bin/pawprints. */
export async function discoverBinary(
  context: vscode.ExtensionContext,
  configured: string,
  output: vscode.OutputChannel,
  quiet = false
): Promise<string | undefined> {
  const env = { ...process.env };
  if (configured.trim()) {
    const p = expandHome(configured.trim());
    if (await runs(p, env)) {
      return p;
    }
    if (!quiet) {
      output.appendLine(`pawprints.binaryPath is set to ${p} but it cannot be run`);
    }
    return undefined;
  }
  if (await runs("pawprints", env)) {
    return (await resolveOnPath("pawprints")) ?? "pawprints";
  }
  const stored = path.join(context.globalStorageUri.fsPath, "bin", executableName());
  if (fs.existsSync(stored) && (await runs(stored, env))) {
    return stored;
  }
  if (!quiet) {
    output.appendLine("pawprints binary not found on PATH or in global storage; looking again every 15 s");
  }
  return undefined;
}

export function expandHome(p: string): string {
  if (p === "~" || p.startsWith("~/")) {
    return path.join(os.homedir(), p.slice(1));
  }
  return p;
}
