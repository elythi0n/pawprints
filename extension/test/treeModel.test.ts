import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { buildDirTree, combineVersions, groupByFile, isInsideAny, splitByWho, liveCounts, netChanges, relativeTo, rootFor, sortLiveSessions, statusBarText, turnTextMap } from "../src/treeModel";
import { PawEvent, Session } from "../src/types";

function event(overrides: Partial<PawEvent>): PawEvent {
  return {
    v: 1,
    id: "01",
    ts: "2026-10-22T14:32:07+03:00",
    session: "s",
    turn: "t",
    tool: "Edit",
    kind: "edit",
    path: "a.go",
    before: "aaa",
    after: "bbb",
    size_before: 1,
    size_after: 2,
    binary: false,
    lines_added: 1,
    lines_removed: 0,
    project_root: "/p",
    ...overrides,
  };
}

function session(overrides: Partial<Session>): Session {
  return {
    id: "x",
    name: "x",
    project: "/p",
    project_id: "id",
    cwd: "/p",
    alive: true,
    status: "busy",
    edits: 0,
    files: [],
    lines_added: 0,
    lines_removed: 0,
    ...overrides,
  };
}

describe("groupByFile", () => {
  it("keeps newest-first order for files and their versions", () => {
    const events = [
      event({ id: "3", path: "b.go" }),
      event({ id: "2", path: "a.go" }),
      event({ id: "1", path: "b.go" }),
    ];
    const groups = groupByFile(events);
    assert.deepEqual(groups.map((g) => g.path), ["b.go", "a.go"]);
    assert.deepEqual(groups[0].events.map((e) => e.id), ["3", "1"]);
  });
});

describe("netChanges", () => {
  it("diffs the oldest before against the newest after per file", () => {
    const events = [
      event({ id: "3", path: "a.go", before: "v2", after: "v3" }),
      event({ id: "2", path: "new.go", before: null, after: "n1" }),
      event({ id: "1", path: "a.go", before: "v1", after: "v2" }),
    ];
    const changes = netChanges(events);
    assert.equal(changes.length, 2);
    assert.deepEqual(changes[0], { path: "a.go", before: "v1", after: "v3", firstEvent: events[2], lastEvent: events[0] });
    assert.equal(changes[1].before, null);
  });

  it("drops files that end up unchanged", () => {
    const events = [event({ id: "2", before: "v2", after: "v1" }), event({ id: "1", before: "v1", after: "v2" })];
    assert.deepEqual(netChanges(events), []);
  });
});

describe("live sessions", () => {
  it("counts only alive sessions and renders the status bar text", () => {
    const sessions = [
      session({ id: "1", status: "busy" }),
      session({ id: "2", status: "busy" }),
      session({ id: "3", status: "waiting" }),
      session({ id: "4", status: "idle" }),
      session({ id: "5", status: "busy", alive: false }),
    ];
    const counts = liveCounts(sessions);
    assert.deepEqual(counts, { working: 2, waiting: 1, idle: 1, total: 4 });
    assert.equal(statusBarText(counts), "$(circle-filled) 2 working · 1 waiting");
    assert.equal(statusBarText(liveCounts([])), undefined);
    assert.equal(statusBarText(liveCounts([session({ status: "idle" })])), "$(circle-filled) 1 idle");
  });

  it("sorts waiting first, then working, then by recency", () => {
    const sorted = sortLiveSessions([
      session({ id: "idle", status: "idle" }),
      session({ id: "busy-old", status: "busy", last_event_at: "2026-10-22T10:00:00Z" }),
      session({ id: "waiting", status: "waiting" }),
      session({ id: "busy-new", status: "busy", last_event_at: "2026-10-22T12:00:00Z" }),
    ]);
    assert.deepEqual(sorted.map((s) => s.id), ["waiting", "busy-new", "busy-old", "idle"]);
  });

  it("maps turn ids to prompts", () => {
    const map = turnTextMap([session({ turns: [{ id: "t1", at: "", text: "add retry" }] }), session({ id: "y" })]);
    assert.equal(map.get("t1"), "add retry");
    assert.equal(map.size, 1);
  });
});

describe("paths", () => {
  it("matches folders by prefix on a path boundary", () => {
    assert.equal(isInsideAny("/home/me/proj/sub", ["/home/me/proj"]), true);
    assert.equal(isInsideAny("/home/me/proj", ["/home/me/proj/"]), true);
    assert.equal(isInsideAny("/home/me/project", ["/home/me/proj"]), false);
  });

  it("picks the longest matching root", () => {
    assert.equal(rootFor("/a/b/c/file.go", ["/a", "/a/b"]), "/a/b");
    assert.equal(rootFor("/elsewhere/x", ["/a"]), undefined);
    assert.equal(relativeTo("/a/b", "/a/b/c/file.go"), "c/file.go");
    assert.equal(relativeTo("/a/b", "/a/b"), "");
  });
});

describe("combineVersions", () => {
  it("spans from the oldest before to the newest after", () => {
  const newest = event({ id: "3", before: "h2", after: "h3", lines_added: 2, lines_removed: 1 });
  const middle = event({ id: "2", before: "h1", after: "h2", lines_added: 1, lines_removed: 0 });
  const oldest = event({ id: "1", before: "h0", after: "h1", lines_added: 5, lines_removed: 0 });
  const c = combineVersions([newest, middle, oldest]);
  assert.equal(c.id, "3");
  assert.equal(c.before, "h0");
  assert.equal(c.after, "h3");
  assert.equal(c.combined, 3);
  assert.equal(c.first_id, "1");
  assert.equal(c.lines_added, 8);
  assert.equal(combineVersions([oldest]), oldest);
  });
});

describe("buildDirTree", () => {
  it("builds folders with totals at every level and compacts narrow chains", () => {
    const events = [
      event({ id: "6", path: "pawprints/internal/tui/root.go", before: "a", after: "b", lines_added: 3, lines_removed: 1 }),
      event({ id: "5", path: "pawprints/internal/tui/root.go", before: "0", after: "a", lines_added: 2, lines_removed: 0 }),
      event({ id: "4", path: "pawprints/internal/tui/settings.go", before: null, after: "c", lines_added: 10, lines_removed: 0 }),
      event({ id: "3", path: "pawprints/internal/theme/theme.go", before: null, after: "d", lines_added: 7, lines_removed: 0 }),
      event({ id: "2", path: "pawprints/plugin/hooks.json", before: null, after: "e", lines_added: 4, lines_removed: 0 }),
      event({ id: "1", path: "/tmp/scratch/gen.py", before: null, after: "f", lines_added: 1, lines_removed: 0 }),
    ];
    const tree = buildDirTree(events);
    assert.equal(tree.fileCount, 5);
    assert.equal(tree.edits, 6);
    assert.equal(tree.added, 27);
    assert.deepEqual(
      tree.dirs.map((d) => d.name),
      ["pawprints", "/tmp/scratch"],
      "project folders first, then absolute ones"
    );
    const pawprints = tree.dirs[0];
    assert.equal(pawprints.fileCount, 4);
    assert.deepEqual(
      pawprints.dirs.map((d) => d.name),
      ["internal", "plugin"],
      "sorted by files changed"
    );
    const internal = pawprints.dirs[0];
    assert.deepEqual(
      internal.dirs.map((d) => [d.name, d.fileCount, d.added]),
      [
        ["tui", 2, 15],
        ["theme", 1, 7],
      ]
    );
    const root = internal.dirs[0].files.find((f) => f.path.endsWith("root.go"));
    assert.equal(root?.combined, 2, "a file appears once, as its combined change");
    assert.equal(root?.before, "0");
  });

  it("merges a chain of single folders into one row", () => {
    const tree = buildDirTree([
      event({ id: "2", path: "go/services/billing/testdata/parity/a.json" }),
      event({ id: "1", path: "go/services/billing/testdata/parity/b.json" }),
    ]);
    assert.deepEqual(
      tree.dirs.map((d) => d.name),
      ["go/services/billing/testdata/parity"]
    );
    assert.equal(tree.dirs[0].files.length, 2);
  });

  it("keeps files at the project root on the root", () => {
    const tree = buildDirTree([event({ id: "1", path: "README.md" })]);
    assert.equal(tree.dirs.length, 0);
    assert.equal(tree.files.length, 1);
  });
});

describe("splitByWho", () => {
  it("separates the main conversation, each subagent, and changes outside Claude", () => {
    const w = splitByWho([
      event({ id: "5", path: "notes.txt", kind: "external" }),
      event({ id: "4", path: "gen/api.go", agent_id: "a1", agent_type: "Explore" }),
      event({ id: "3", path: "main.go" }),
      event({ id: "2", path: "gen/types.go", agent_id: "a1", agent_type: "Explore" }),
      event({ id: "1", path: "out.txt", kind: "background" }),
    ]);
    assert.deepEqual(w.main.map((e) => e.path), ["main.go", "out.txt"]);
    assert.equal(w.agents.length, 1);
    assert.equal(w.agents[0].type, "Explore");
    assert.deepEqual(w.agents[0].events.map((e) => e.path), ["gen/api.go", "gen/types.go"]);
    assert.deepEqual(w.outside.map((e) => e.path), ["notes.txt"]);
  });
});
