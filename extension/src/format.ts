import { PawEvent, Session } from "./types";

export function timeAgo(iso: string | undefined, now: number = Date.now()): string {
  if (!iso) {
    return "";
  }
  const then = Date.parse(iso);
  if (Number.isNaN(then)) {
    return "";
  }
  const seconds = Math.max(0, Math.round((now - then) / 1000));
  if (seconds < 45) {
    return "just now";
  }
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) {
    return `${minutes} min ago`;
  }
  const hours = Math.round(minutes / 60);
  if (hours < 24) {
    return `${hours} h ago`;
  }
  const days = Math.round(hours / 24);
  if (days < 30) {
    return days === 1 ? "yesterday" : `${days} days ago`;
  }
  const months = Math.round(days / 30);
  if (months < 12) {
    return `${months} mo ago`;
  }
  return `${Math.round(months / 12)} y ago`;
}

export function clockTime(iso: string | undefined): string {
  if (!iso) {
    return "";
  }
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) {
    return "";
  }
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
}

export function dateTime(iso: string | undefined): string {
  if (!iso) {
    return "";
  }
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) {
    return iso;
  }
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())} ${clockTime(iso)}`;
}

function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

export function countsLabel(added: number, removed: number): string {
  return `+${added} -${removed}`;
}

export function isUnchanged(event: PawEvent): boolean {
  return event.before === event.after;
}

export function isNewFile(event: PawEvent): boolean {
  return event.before === null && event.after !== null && !event.before_unknown;
}

export function isDeletion(event: PawEvent): boolean {
  return event.before !== null && event.after === null;
}

/** "files:write_file" for an MCP tool's mcp__files__write_file; other names as they are. */
export function shortToolName(tool: string): string {
  if (!tool.startsWith("mcp__")) {
    return tool;
  }
  const rest = tool.slice("mcp__".length);
  const at = rest.indexOf("__");
  return at < 0 ? rest : `${rest.slice(0, at)}:${rest.slice(at + 2)}`;
}

/** Who or what made a change, with the subagent type when a subagent made it. */
export function toolName(event: PawEvent): string {
  if (event.kind === "external") {
    return "outside Claude";
  }
  if (event.kind === "restore" || event.kind === "undo") {
    return event.kind;
  }
  const base = event.kind === "background" ? "background" : shortToolName(event.tool || event.kind);
  return event.agent_type ? `${base} · ${event.agent_type}` : base;
}

/** Label of a version node: "14:32  Edit  +12 -3". */
export function versionLabel(event: PawEvent): string {
  return `${clockTime(event.ts)}  ${toolName(event)}  ${countsLabel(event.lines_added, event.lines_removed)}`;
}

export function versionDescription(event: PawEvent): string {
  const parts: string[] = [];
  if (isUnchanged(event)) {
    parts.push("unchanged");
  } else if (event.before_unknown) {
    parts.push("first seen");
  } else if (isNewFile(event)) {
    parts.push("new");
  } else if (isDeletion(event)) {
    parts.push("deleted");
  }
  return parts.join(" · ");
}

export function humanBytes(n: number): string {
  if (n < 1024) {
    return `${n} B`;
  }
  const units = ["KB", "MB", "GB", "TB"];
  let value = n / 1024;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i += 1;
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[i]}`;
}

export function basename(path: string): string {
  const i = path.lastIndexOf("/");
  return i < 0 ? path : path.slice(i + 1);
}

export function dirname(path: string): string {
  const i = path.lastIndexOf("/");
  return i < 0 ? "" : path.slice(0, i);
}

const fenceLanguages: Record<string, string> = {
  ts: "ts",
  tsx: "tsx",
  js: "js",
  jsx: "jsx",
  mjs: "js",
  cjs: "js",
  go: "go",
  py: "python",
  rb: "ruby",
  rs: "rust",
  java: "java",
  kt: "kotlin",
  c: "c",
  h: "c",
  cc: "cpp",
  cpp: "cpp",
  hpp: "cpp",
  cs: "csharp",
  php: "php",
  sh: "sh",
  bash: "bash",
  zsh: "zsh",
  fish: "fish",
  json: "json",
  yaml: "yaml",
  yml: "yaml",
  toml: "toml",
  md: "markdown",
  html: "html",
  css: "css",
  scss: "scss",
  sql: "sql",
  xml: "xml",
  swift: "swift",
  lua: "lua",
  vue: "vue",
  svelte: "svelte",
  dart: "dart",
  ex: "elixir",
  exs: "elixir",
  hs: "haskell",
  tf: "hcl",
};

export function fenceLanguage(path: string): string {
  const name = basename(path);
  if (name === "Makefile") {
    return "makefile";
  }
  if (name === "Dockerfile") {
    return "dockerfile";
  }
  const dot = name.lastIndexOf(".");
  if (dot < 0) {
    return "";
  }
  return fenceLanguages[name.slice(dot + 1).toLowerCase()] ?? "";
}

/** Markdown block for copySnippet: a header line, then the content fenced. */
export function markdownSnippet(event: PawEvent, content: string): string {
  const header = `${event.path} @ ${dateTime(event.ts)} (pawprints:${event.id})`;
  const fence = longestFence(content);
  const body = content.endsWith("\n") ? content : content + "\n";
  return `${header}\n${fence}${fenceLanguage(event.path)}\n${body}${fence}\n`;
}

function longestFence(content: string): string {
  let longest = 0;
  for (const m of content.matchAll(/`{3,}/g)) {
    longest = Math.max(longest, m[0].length);
  }
  return "`".repeat(Math.max(3, longest + 1));
}

export function statusWord(status: string): string {
  switch (status) {
    case "busy":
      return "working";
    case "waiting":
      return "waiting";
    case "idle":
      return "idle";
    case "ended":
      return "ended";
    default:
      return status || "unknown";
  }
}

export function sessionTitle(session: Session): string {
  return session.name || session.first_prompt || session.id.slice(0, 8);
}

export function truncate(text: string, max: number): string {
  const oneLine = text.replace(/\s+/g, " ").trim();
  return oneLine.length <= max ? oneLine : oneLine.slice(0, max - 1) + "…";
}

export function formatNumber(n: number): string {
  return n.toLocaleString("en-US");
}
