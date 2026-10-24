import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { renderStats } from "../src/statsText";
import { Stats } from "../src/types";

const stats: Stats = {
  projects: 1,
  sessions: 4,
  live_sessions: 1,
  turns: 19,
  edits: 88,
  external_edits: 3,
  files: 21,
  lines_added: 900,
  lines_removed: 210,
  usage: { "claude-fable-5-1": { input: 1, output: 324, cache_read: 215743, cache_write: 1807, thinking: 179, messages: 12 } },
  usage_total: { input: 1, output: 324, cache_read: 215743, cache_write: 1807, thinking: 179, messages: 12 },
  cost_estimate: 1.23,
  top_files: [{ path: "a.go", edits: 12 }],
  days: [{ date: "2026-10-22", edits: 40, sessions: 2, files: 9, lines_added: 300, lines_removed: 40 }],
};

describe("renderStats", () => {
  it("renders every section as aligned plain text", () => {
    const text = renderStats(stats);
    assert.match(text, /^Pawprints Stats\n=+\nall time, 1 project\n/);
    assert.match(text, /Edits\s+88\n/);
    assert.match(text, /API cost \(USD\)\s+1\.23\n/);
    assert.match(text, /claude-fable-5-1\s+1\s+324\s+215,743\s+1,807\s+179\s+12\n/);
    assert.match(text, /Most edited files\n\nFile\s+Edits\n-+\s+-+\na\.go\s+12\n/);
    assert.match(text, /2026-10-22\s+40\s+2\s+9\s+300\s+40\n/);
  });

  it("copes with empty optional sections", () => {
    const text = renderStats({ ...stats, usage: {}, top_files: [], days: [], cost_estimate: undefined, since: "2026-10-15T13:32:57+03:00" });
    assert.match(text, /since 2026-10-15/);
    assert.doesNotMatch(text, /Most edited files/);
    assert.doesNotMatch(text, /API cost/);
    assert.match(text, /total\s+1\s+324/);
  });
});
