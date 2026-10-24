import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { countsLabel, fenceLanguage, humanBytes, isNewFile, markdownSnippet, timeAgo, truncate, versionDescription, versionLabel } from "../src/format";
import { PawEvent } from "../src/types";

const base: PawEvent = {
  v: 1,
  id: "01JB4X9M",
  ts: "2026-10-22T14:32:07+03:00",
  session: "s",
  turn: "t",
  tool: "Edit",
  kind: "edit",
  path: "src/a.go",
  before: "aaa",
  after: "bbb",
  size_before: 1,
  size_after: 2,
  binary: false,
  lines_added: 12,
  lines_removed: 3,
  project_root: "/p",
};

describe("format", () => {
  it("renders relative times", () => {
    const now = Date.parse("2026-10-22T12:00:00Z");
    assert.equal(timeAgo("2026-10-22T11:59:50Z", now), "just now");
    assert.equal(timeAgo("2026-10-22T11:57:00Z", now), "3 min ago");
    assert.equal(timeAgo("2026-10-22T09:00:00Z", now), "3 h ago");
    assert.equal(timeAgo("2026-10-21T09:00:00Z", now), "yesterday");
    assert.equal(timeAgo("2026-10-01T09:00:00Z", now), "21 days ago");
    assert.equal(timeAgo(undefined, now), "");
    assert.equal(timeAgo("garbage", now), "");
  });

  it("labels versions with time, tool and counts", () => {
    assert.equal(countsLabel(12, 3), "+12 -3");
    assert.match(versionLabel(base), /^\d\d:\d\d {2}Edit {2}\+12 -3$/);
    assert.match(versionLabel({ ...base, kind: "external", tool: "Bash" }), / {2}outside Claude {2}/);
    assert.match(versionLabel({ ...base, kind: "background", tool: "Bash" }), / {2}background {2}/);
    assert.match(versionLabel({ ...base, tool: "mcp__files__write_file" }), / {2}files:write_file {2}/);
    assert.match(versionLabel({ ...base, tool: "Bash", agent_type: "Explore" }), / {2}Bash · Explore {2}/);
    assert.equal(versionDescription(base), "");
    assert.equal(versionDescription({ ...base, after: "aaa" }), "unchanged");
    assert.equal(versionDescription({ ...base, before: null }), "new");
    assert.equal(versionDescription({ ...base, after: null }), "deleted");
    assert.equal(versionDescription({ ...base, kind: "external", tool: "external" }), "");
  });

  it("builds a fenced markdown snippet with a header line", () => {
    const snippet = markdownSnippet(base, "package main\n");
    const lines = snippet.split("\n");
    assert.match(lines[0], /^src\/a\.go @ 2026-10-22 \d\d:\d\d \(pawprints:01JB4X9M\)$/);
    assert.equal(lines[1], "```go");
    assert.equal(lines[2], "package main");
    assert.equal(lines[3], "```");
  });

  it("uses a longer fence when the content has backticks", () => {
    const snippet = markdownSnippet({ ...base, path: "README.md" }, "```sh\nls\n```\n");
    assert.match(snippet, /\n````markdown\n/);
  });

  it("maps extensions to fence languages", () => {
    assert.equal(fenceLanguage("a/b.ts"), "ts");
    assert.equal(fenceLanguage("Makefile"), "makefile");
    assert.equal(fenceLanguage("noext"), "");
    assert.equal(fenceLanguage("x.unknownext"), "");
  });

  it("formats bytes and truncates text", () => {
    assert.equal(humanBytes(512), "512 B");
    assert.equal(humanBytes(401234), "392 KB");
    assert.equal(humanBytes(2 * 1024 * 1024), "2.0 MB");
    assert.equal(truncate("a  long\nprompt here", 8), "a long …");
    assert.equal(truncate("short", 8), "short");
  });

  it("calls a first seen file first seen, not new", () => {
    const seen: PawEvent = { ...base, before: null, after: "b", before_unknown: true };
    assert.equal(isNewFile(seen), false);
    assert.match(versionDescription(seen), /first seen/);
    assert.match(versionDescription({ ...base, before: null, after: "b" }), /new/);
  });
});
