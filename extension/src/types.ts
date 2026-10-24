export interface PawEvent {
  v: number;
  id: string;
  ts: string;
  session: string;
  turn: string;
  tool: string;
  /** edit: a Claude tool; background: while a backgrounded command ran; external: outside Claude. */
  kind: "edit" | "external" | "background" | "restore" | "undo" | string;
  path: string;
  before: string | null;
  after: string | null;
  size_before: number;
  size_after: number;
  binary: boolean;
  lines_added: number;
  lines_removed: number;
  project_root: string;
  pinned?: boolean;
  /** The shell command or MCP call that made the change, when known. */
  command?: string;
  /** The subagent that made the change; absent for the main conversation. */
  agent_id?: string;
  agent_type?: string;
  /** First seen after a shell command, with no earlier copy to compare. */
  before_unknown?: boolean;
  /** Set on a combined row: how many edits it folds together. */
  combined?: number;
  /** Set on a combined row: the oldest folded edit, whose "before" it starts from. */
  first_id?: string;
}

export interface TurnSummary {
  id: string;
  at: string;
  text: string;
}

export interface Turn extends TurnSummary {
  edits: number;
  files: string[];
}

export type SessionStatus = "busy" | "waiting" | "idle" | "ended" | "unknown" | string;

/** A subagent a session started. */
export interface SubagentInfo {
  id: string;
  type: string;
  started_at?: string;
  stopped_at?: string;
  /** The prompt the subagent was started in. */
  turn?: string;
  /** The short task it was given. */
  description?: string;
  /** The start of its final report, and the report itself. */
  summary?: string;
  report?: string;
}

export interface Session {
  id: string;
  agents?: SubagentInfo[];
  name: string;
  project: string;
  project_id: string;
  cwd: string;
  pid?: number;
  alive: boolean;
  status: SessionStatus;
  status_since?: string;
  waiting_for?: string;
  touching?: boolean;
  started_at?: string;
  first_prompt?: string;
  last_prompt?: string;
  last_prompt_at?: string;
  last_event_at?: string;
  edits: number;
  files: string[];
  lines_added: number;
  lines_removed: number;
  turns?: TurnSummary[];
  sources?: string[];
}

export interface Pin {
  event: string;
  hash: string;
  path: string;
  message: string;
  at: string;
}

export interface Hunk {
  old_start: number;
  old_lines: number;
  new_start: number;
  new_lines: number;
  added: number;
  removed: number;
}

export interface HunksResult {
  event: string;
  path: string;
  hunks: Hunk[];
}

export interface ShowResult {
  event: PawEvent;
  before: string;
  after: string;
  diff: string;
  project_root: string;
}

export interface PlannedChange {
  path: string;
  from: string;
  to: string;
  event: string;
  conflicts: number;
  modified: boolean;
}

export interface ChangePlan {
  kind: "restore" | "undo";
  applied: boolean;
  changes: PlannedChange[];
}

export interface ClearPlan {
  scope: string;
  projects: number;
  sessions: number;
  events: number;
  files: number;
  bytes: number;
  cut_at?: string;
  kept_pins: number;
}

export interface Usage {
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
  thinking: number;
  messages: number;
}

export interface Stats {
  projects: number;
  since?: string;
  sessions: number;
  live_sessions: number;
  turns: number;
  edits: number;
  external_edits: number;
  files: number;
  lines_added: number;
  lines_removed: number;
  usage: Record<string, Usage>;
  usage_total: Usage;
  /** What the tokens would cost at API prices. */
  cost_estimate?: number;
  /** False when some model has no price, so the cost leaves it out. */
  cost_complete?: boolean;
  top_files: { path: string; edits: number }[];
  days: { date: string; edits: number; sessions: number; files: number; lines_added: number; lines_removed: number }[];
}

export interface Info {
  version: string;
  home: string;
  project_root: string;
  project_id: string;
  tracked: boolean;
  hook_settings: boolean;
  hook_plugin: boolean;
}

export interface InstallResult {
  settings_path: string;
  settings_hooks: boolean;
  plugin_installed: boolean;
}

export type WatchLine =
  | { kind: "event"; event: PawEvent; project: string }
  | { kind: "session"; session: Session; project: string }
  | { kind: "heartbeat" };
