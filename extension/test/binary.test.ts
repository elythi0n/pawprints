import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { describe, it } from "node:test";
import { Binary, isBinaryError, isNoHistory } from "../src/binary";
import { PawEvent } from "../src/types";

const binaryPath = path.resolve(__dirname, "..", "..", "..", "bin", "pawprints");
const haveBinary = fs.existsSync(binaryPath) && spawnSync("git", ["--version"]).status === 0;

interface Fixture {
  home: string;
  root: string;
  binary: Binary;
  lines: string[];
}

function fixture(): Fixture {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "pawprints-ext-"));
  const home = path.join(base, "home");
  const root = path.join(base, "proj");
  fs.mkdirSync(home);
  fs.mkdirSync(path.join(root, "src"), { recursive: true });
  execFileSync("git", ["init", "-q", root]);
  const lines: string[] = [];
  const output = { appendLine: (l: string) => lines.push(l) } as unknown as import("vscode").OutputChannel;
  const binary = new Binary(binaryPath, output, { home: () => home });
  return { home, root, binary, lines };
}

function hook(f: Fixture, event: string, tool: string, useId: string, file: string, kind: string, extra: Record<string, unknown> = {}): void {
  const input = JSON.stringify({
    session_id: "sess-test",
    transcript_path: "/nonexistent",
    cwd: f.root,
    hook_event_name: event,
    tool_name: tool,
    tool_use_id: useId,
    tool_input: { file_path: file },
    ...extra,
  });
  execFileSync(binaryPath, ["hook", kind], { input, env: { ...process.env, PAWPRINTS_HOME: f.home } });
}

describe("Binary against the real pawprints binary", { skip: !haveBinary && "bin/pawprints or git not available" }, () => {
  it("treats a fresh store as empty and surfaces error lines with their exit code", async () => {
    const f = fixture();
    assert.deepEqual(await f.binary.list(["log", "--limit", "5"], f.root), []);
    assert.deepEqual(await f.binary.list(["pins"], f.root), []);
    await assert.rejects(f.binary.json(["pins"], f.root), (e: unknown) => isNoHistory(e));
    await assert.rejects(f.binary.jsonOne(["show", "nope"], f.root), (e: unknown) => isBinaryError(e) && e.exit === 1);
  });

  it("records an edit through the hook and reads it back as JSON and raw bytes", async () => {
    const f = fixture();
    const file = path.join(f.root, "src", "main.go");
    fs.writeFileSync(file, "package main\n");
    hook(f, "UserPromptSubmit", "", "", "", "prompt", { prompt: "add main" });
    hook(f, "PreToolUse", "Edit", "t1", file, "pre");
    fs.writeFileSync(file, "package main\n\nfunc main() {}\n");
    hook(f, "PostToolUse", "Edit", "t1", file, "post");

    const events = await f.binary.list<PawEvent>(["log"], f.root);
    assert.equal(events.length, 1);
    assert.equal(events[0].path, "src/main.go");
    assert.equal(events[0].lines_added, 2);

    const after = await f.binary.raw(["cat", events[0].after as string], f.root);
    assert.equal(after.toString("utf8"), "package main\n\nfunc main() {}\n");
    const before = await f.binary.raw(["cat", events[0].id, "--before"], f.root);
    assert.equal(before.toString("utf8"), "package main\n");

    const plan = await f.binary.jsonOne<{ kind: string; applied: boolean; changes: unknown[] }>(["restore", events[0].id, "--before", "--dry-run"], f.root);
    assert.equal(plan.kind, "restore");
    assert.equal(plan.applied, false);
    assert.equal(plan.changes.length, 1);

    fs.appendFileSync(file, "// edited by hand\n");
    await assert.rejects(
      f.binary.jsonOne(["restore", events[0].id, "--before", "--dry-run"], f.root),
      (e: unknown) => isBinaryError(e) && e.exit === 3
    );
  });

  it("passes the project as --project=<root> so a trailing --project scope flag survives", async () => {
    const f = fixture();
    const file = path.join(f.root, "a.txt");
    fs.writeFileSync(file, "");
    hook(f, "PreToolUse", "Write", "t1", file, "pre");
    fs.writeFileSync(file, "x\n");
    hook(f, "PostToolUse", "Write", "t1", file, "post");
    const plan = await f.binary.jsonOne<{ scope: string; events: number }>(["clear", "--dry-run", "--project"], f.root);
    assert.equal(plan.events, 1);
    assert.match(plan.scope, /project/);
  });
});
