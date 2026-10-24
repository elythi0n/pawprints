import { PawEvent, Session } from "./types";

export interface FileGroup {
  path: string;
  events: PawEvent[];
}

/** Groups events by path, keeping the input order (newest first) for both files and versions. */
export function groupByFile(events: PawEvent[]): FileGroup[] {
  const groups = new Map<string, FileGroup>();
  for (const event of events) {
    let group = groups.get(event.path);
    if (!group) {
      group = { path: event.path, events: [] };
      groups.set(event.path, group);
    }
    group.events.push(event);
  }
  return [...groups.values()];
}

/**
 * Folds several versions of one file (newest first) into one event that
 * spans them: the newest event with the oldest one's "before". Diffing it
 * shows the whole change; restoring it gives the newest version.
 */
export function combineVersions(events: PawEvent[]): PawEvent {
  const newest = events[0];
  if (events.length < 2) {
    return newest;
  }
  const oldest = events[events.length - 1];
  return {
    ...newest,
    before: oldest.before,
    size_before: oldest.size_before,
    lines_added: events.reduce((n, e) => n + e.lines_added, 0),
    lines_removed: events.reduce((n, e) => n + e.lines_removed, 0),
    combined: events.length,
    first_id: oldest.id,
  };
}

export interface NetChange {
  path: string;
  before: string | null;
  after: string | null;
  firstEvent: PawEvent;
  lastEvent: PawEvent;
}

/** Net change per file across a set of events (newest first): oldest before to newest after. */
export function netChanges(events: PawEvent[]): NetChange[] {
  return groupByFile(events)
    .map((group) => {
      const newest = group.events[0];
      const oldest = group.events[group.events.length - 1];
      return {
        path: group.path,
        before: oldest.before,
        after: newest.after,
        firstEvent: oldest,
        lastEvent: newest,
      };
    })
    .filter((change) => change.before !== change.after);
}

export interface LiveCounts {
  working: number;
  waiting: number;
  idle: number;
  total: number;
}

export function liveCounts(sessions: Session[]): LiveCounts {
  const counts: LiveCounts = { working: 0, waiting: 0, idle: 0, total: 0 };
  for (const s of sessions) {
    if (!s.alive) {
      continue;
    }
    counts.total += 1;
    if (s.status === "busy") {
      counts.working += 1;
    } else if (s.status === "waiting") {
      counts.waiting += 1;
    } else {
      counts.idle += 1;
    }
  }
  return counts;
}

/** Status bar text, or undefined when nothing is running. */
export function statusBarText(counts: LiveCounts): string | undefined {
  if (counts.total === 0) {
    return undefined;
  }
  const parts: string[] = [];
  if (counts.working > 0) {
    parts.push(`${counts.working} working`);
  }
  if (counts.waiting > 0) {
    parts.push(`${counts.waiting} waiting`);
  }
  if (parts.length === 0) {
    parts.push(`${counts.idle} idle`);
  }
  return `$(circle-filled) ${parts.join(" · ")}`;
}

const statusOrder: Record<string, number> = { waiting: 0, busy: 1, idle: 2, unknown: 3, ended: 4 };

/** Waiting sessions first, then working, then by most recent activity. */
export function sortLiveSessions(sessions: Session[]): Session[] {
  return [...sessions].sort((a, b) => {
    const byStatus = (statusOrder[a.status] ?? 3) - (statusOrder[b.status] ?? 3);
    if (byStatus !== 0) {
      return byStatus;
    }
    return (b.last_event_at ?? b.started_at ?? "").localeCompare(a.last_event_at ?? a.started_at ?? "");
  });
}

export function isInsideAny(cwd: string, roots: string[]): boolean {
  const norm = cwd.replace(/\/+$/, "");
  return roots.some((root) => {
    const r = root.replace(/\/+$/, "");
    return norm === r || norm.startsWith(r + "/");
  });
}

export function turnTextMap(sessions: Session[]): Map<string, string> {
  const map = new Map<string, string>();
  for (const session of sessions) {
    for (const turn of session.turns ?? []) {
      map.set(turn.id, turn.text);
    }
  }
  return map;
}

/** Workspace folder that contains an absolute path, longest match wins. */
export function rootFor(absolutePath: string, roots: string[]): string | undefined {
  let best: string | undefined;
  for (const root of roots) {
    const r = root.replace(/\/+$/, "");
    if (absolutePath === r || absolutePath.startsWith(r + "/")) {
      if (!best || r.length > best.length) {
        best = r;
      }
    }
  }
  return best;
}

export function relativeTo(root: string, absolutePath: string): string {
  const r = root.replace(/\/+$/, "");
  if (absolutePath === r) {
    return "";
  }
  return absolutePath.startsWith(r + "/") ? absolutePath.slice(r.length + 1) : absolutePath;
}

/** A folder touched by a set of edits, with what changed inside it. */
export interface DirTree {
  /** Shown name; compacted chains read "pawprints/internal". */
  name: string;
  /** Full path from the project root, or an absolute path outside it. */
  path: string;
  /** One combined event per file directly in this folder, newest first. */
  files: PawEvent[];
  dirs: DirTree[];
  /** Totals for this folder and everything below it. */
  fileCount: number;
  edits: number;
  added: number;
  removed: number;
}

/**
 * Builds the folder tree of everything a set of edits touched. Each file
 * appears once, as its combined change. A folder holding no files and a
 * single subfolder merges with it, so deep but narrow paths read as one row.
 */
export function buildDirTree(events: PawEvent[]): DirTree {
  const root: DirTree = { name: "", path: "", files: [], dirs: [], fileCount: 0, edits: 0, added: 0, removed: 0 };
  for (const group of groupByFile(events)) {
    const change = combineVersions(group.events);
    const absolute = group.path.startsWith("/");
    const parts = group.path.split("/").filter(Boolean);
    parts.pop();
    let node = root;
    let path = "";
    for (const [i, part] of parts.entries()) {
      path = i === 0 ? (absolute ? `/${part}` : part) : `${path}/${part}`;
      let child = node.dirs.find((d) => d.path === path);
      if (!child) {
        child = { name: i === 0 && absolute ? `/${part}` : part, path, files: [], dirs: [], fileCount: 0, edits: 0, added: 0, removed: 0 };
        node.dirs.push(child);
      }
      node = child;
    }
    node.files.push(change);
  }
  total(root);
  root.dirs = root.dirs.map(compact);
  sortTree(root);
  return root;
}

function total(node: DirTree): void {
  node.fileCount = node.files.length;
  node.edits = node.files.reduce((n, f) => n + (f.combined ?? 1), 0);
  node.added = node.files.reduce((n, f) => n + f.lines_added, 0);
  node.removed = node.files.reduce((n, f) => n + f.lines_removed, 0);
  for (const d of node.dirs) {
    total(d);
    node.fileCount += d.fileCount;
    node.edits += d.edits;
    node.added += d.added;
    node.removed += d.removed;
  }
}

function compact(node: DirTree): DirTree {
  let n = node;
  while (n.files.length === 0 && n.dirs.length === 1) {
    const child = n.dirs[0];
    n = { ...child, name: `${n.name}/${child.name}` };
  }
  n.dirs = n.dirs.map(compact);
  return n;
}

/** Folders by most files changed, then by name; project folders before absolute ones. */
function sortTree(node: DirTree): void {
  node.dirs.sort((a, b) => {
    const outside = Number(a.path.startsWith("/")) - Number(b.path.startsWith("/"));
    return outside || b.fileCount - a.fileCount || a.name.localeCompare(b.name);
  });
  node.dirs.forEach(sortTree);
}

/** A prompt's events split by who changed them. */
export interface ByWho {
  main: PawEvent[];
  agents: { id: string; type: string; events: PawEvent[] }[];
  /** Changed outside Claude, between its commands. */
  outside: PawEvent[];
}

/** Splits events (newest first) into the main conversation, each subagent in order of first appearance, and changes outside Claude. */
export function splitByWho(events: PawEvent[]): ByWho {
  const out: ByWho = { main: [], agents: [], outside: [] };
  const byId = new Map<string, { id: string; type: string; events: PawEvent[] }>();
  for (const e of events) {
    if (e.kind === "external") {
      out.outside.push(e);
    } else if (e.agent_id) {
      let group = byId.get(e.agent_id);
      if (!group) {
        group = { id: e.agent_id, type: e.agent_type ?? "", events: [] };
        byId.set(e.agent_id, group);
        out.agents.push(group);
      }
      group.events.push(e);
    } else {
      out.main.push(e);
    }
  }
  return out;
}

export function fileCount(events: PawEvent[]): number {
  return new Set(events.map((e) => e.path)).size;
}
