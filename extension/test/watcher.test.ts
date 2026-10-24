import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { describe, it } from "node:test";
import { WatchLine } from "../src/types";
import { Watcher } from "../src/watcher";

function fakeBinary(script: string) {
  const calls: string[][] = [];
  return {
    calls,
    spawnLong(args: string[]) {
      calls.push(args);
      return spawn(process.execPath, ["-e", script], { stdio: ["ignore", "pipe", "pipe"] });
    },
  };
}

function wait(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

describe("Watcher", () => {
  it("parses NDJSON lines and restarts with backoff when the process exits", async () => {
    const lines: WatchLine[] = [];
    const delays: number[] = [];
    const binary = fakeBinary('process.stdout.write(\'{"kind":"heartbeat"}\\n{"kind":"event","event":{"id":"e1","path":"a.go"},"project":"/p"}\\n\'); setTimeout(() => process.exit(0), 10);');
    const watcher = new Watcher({
      binary,
      root: "/p",
      onLine: (line) => lines.push(line),
      log: () => {},
      delay: (attempt) => {
        delays.push(attempt);
        return 20;
      },
    });
    watcher.start();
    await wait(250);
    watcher.dispose();

    assert.ok(binary.calls.length >= 2, `expected restarts, got ${binary.calls.length} spawns`);
    assert.deepEqual(binary.calls[0], ["watch"]);
    assert.ok(lines.length >= 2);
    assert.equal(lines[0].kind, "heartbeat");
    assert.equal(lines[1].kind, "event");
    assert.deepEqual(delays.slice(0, 2), [1, 1], "a healthy stream resets the backoff before each exit");
  });

  it("backs off further when the process keeps failing without output", async () => {
    const delays: number[] = [];
    const binary = fakeBinary("process.exit(1)");
    const watcher = new Watcher({
      binary,
      onLine: () => {},
      log: () => {},
      delay: (attempt) => {
        delays.push(attempt);
        return 10;
      },
    });
    watcher.start();
    await wait(300);
    watcher.dispose();
    assert.ok(delays.length >= 3);
    assert.deepEqual(delays.slice(0, 3), [1, 2, 3]);
    assert.equal(watcher.running, false);
  });

  it("restarts when no heartbeat arrives in time", async () => {
    const binary = fakeBinary("setInterval(() => {}, 1000)");
    const logs: string[] = [];
    const watcher = new Watcher({
      binary,
      onLine: () => {},
      log: (m) => logs.push(m),
      delay: () => 10,
      heartbeatTimeoutMs: 40,
    });
    watcher.start();
    await wait(200);
    watcher.dispose();
    assert.ok(binary.calls.length >= 2, "watcher should have been respawned after the heartbeat timeout");
    assert.ok(logs.some((l) => l.includes("heartbeat missing")));
  });

  it("does not restart after dispose", async () => {
    const binary = fakeBinary("process.exit(0)");
    const watcher = new Watcher({ binary, onLine: () => {}, log: () => {}, delay: () => 5 });
    watcher.start();
    watcher.dispose();
    await wait(60);
    assert.equal(binary.calls.length, 1);
  });
});
