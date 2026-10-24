# pawprints for agents

`pawprints` records every file edit Claude Code makes: the content before and
after, which session and prompt caused it, and line counts. It can show, diff,
restore, undo, and export that history.

Without arguments `pawprints` launches a full-screen terminal UI. **Never run
it bare from a tool call**; always pass a subcommand.

## Always do this first

- Pass `--json` to every command. Listings print one JSON object per line.
  Errors come back as `{"error":"...","exit":N}` on stdout, so read one stream.
- Use `--session current` to mean "the session running in this directory".
- Anything that writes files (`restore`, `undo`) or deletes history (`clear`)
  asks for confirmation on a TTY. From an agent, run it with `--dry-run` first,
  show the plan to the user, then re-run with `--yes` only after they agree.
- If a prompt contains a reference like `pawprints:01JB4X9M...` followed by a
  path, resolve it with `pawprints show <id> --json` and use the `before`,
  `after`, and `diff` fields. The id may be a prefix.

## Commands and shapes

```jsonc
// pawprints log --json [--limit N] [--session ID|current] [--turn ID] [--path P] [--since 2h] [--kind edit,external]
// one line per event, newest first
{"v":1,"id":"01JB...","ts":"2026-10-22T14:32:07+03:00","session":"abc","turn":"01JB...","tool":"Edit","kind":"edit",
 "path":"internal/store/store.go","before":"9f86d0...","after":"2c26b4...","size_before":4120,"size_after":4188,
 "binary":false,"lines_added":12,"lines_removed":3,"project_root":"/home/me/proj","pinned":false}
// kind is edit (Claude: an edit tool, a Bash command, or an MCP tool), background (written while a
// backgrounded command ran), external (changed outside Claude, between its commands, in a file of this
// project: usually the person; never another Claude session's work),
// restore, or undo. before/after are blob hashes; null means the file did not exist / was deleted.
// Optional: command (the Bash command), agent_id and agent_type (a subagent made it), before_unknown
// (first seen with no earlier copy, so no line counts). Files outside the project have absolute paths.

// pawprints show <event> --json
{"event":{...as above...},"before":"<full content>","after":"<full content>","diff":"--- a/...\n+++ b/...\n@@ ...","project_root":"..."}

// pawprints diff <event> --json [--against previous|current|<event>] [--from <event>]
// --from <first>: several edits of one file as one change, from before <first> to after <event>
{"event":"01JB...","against":"previous","diff":"...","stats":{"added":12,"removed":3}}

// pawprints hunks <event> --json [--against previous|current]
{"event":"01JB...","path":"...","hunks":[{"old_start":41,"old_lines":7,"new_start":41,"new_lines":16,"added":9,"removed":0}]}

// pawprints cat <hash|event> [--before]      raw bytes of a stored version, no JSON

// pawprints sessions --json [--live] [--all] [--since 24h]
{"id":"abc","name":"api-a9","project":"/home/me/proj","project_id":"3f9a...","cwd":"...","pid":39397,"alive":true,
 "status":"busy","status_since":"...","waiting_for":"","touching":true,"started_at":"...","first_prompt":"add retry",
 "last_prompt":"now the tests","last_prompt_at":"...","last_event_at":"...","edits":14,"files":["a.go"],
 "lines_added":40,"lines_removed":3,"turns":[{"id":"01JB...","at":"...","text":"add retry"}],
 "usage":{"claude-fable-5-1":{"input":1,"output":324,"cache_read":215743,"cache_write":1807,"thinking":179,"messages":12}},
 "agents":[{"id":"a5f0...","type":"Explore","turn":"01JB...","description":"Find the retry loop","started_at":"...","stopped_at":"...",
   "summary":"Found the retry loop","report":"<full report>"}],   // a subagent's changes carry the turn it was started in
 "sources":["registry","hook"]}
// status: busy | waiting | idle | ended | unknown. touching: an edit landed in the last 30 s.

// pawprints turns --json [--session ID|current]
{"id":"01JB...","at":"...","text":"add retry to the webhook client","account":"3f2a...","edits":3,"files":["a.go","a_test.go"],
 "tokens":182000,"api_cost":1.42}
// tokens: what the prompt spent toward usage limits (input + cache writes + output); api_cost: what that costs at API prices

// pawprints restore <event> --json [--before] [--three-way|--force] [--dry-run] [--yes]
// pawprints undo --json [--turn [ID]] [--session ID] [--three-way|--force] [--dry-run] [--yes]
{"kind":"undo","applied":true,"changes":[{"path":"a.go","from":"2c26b4...","to":"9f86d0...","event":"01JB...","conflicts":0,"modified":false}]}
// exit 3 with {"error":"<path> changed since the last recorded edit ..."} when a person edited the file after
// Claude did. --three-way merges with git merge-file (conflicts become markers); --force overwrites.

// pawprints export --json --session ID | --turn ID | --since 2h
{"files":2,"patch":"diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ ..."}   // without --json the patch is printed raw

// pawprints pin <event> -m "why" --json / unpin <event> --json / pins --json
{"event":"01JB...","hash":"2c26b4...","path":"a.go","message":"before the refactor","at":"..."}

// pawprints stats --json [--since 7d] [--all] [--top 10]
{"projects":1,"sessions":4,"live_sessions":1,"turns":19,"edits":88,"external_edits":3,"files":21,"lines_added":900,
 "lines_removed":210,"usage":{"<model>":{...}},"usage_total":{...},"cost_estimate":1.23,"top_files":[{"path":"a.go","edits":12}],
 "days":[{"date":"2026-10-22","edits":40,"sessions":2,"files":9,"lines_added":300,"lines_removed":40}]}

// pawprints metrics --json [--since 24h] [--until T] [--bucket 1m|5m|15m|1h|6h|1d] [--account ID|email] [--session ID] [--all]
// tokens, edits, and limits over time for this project (or --all); the bucket is picked from the range when left out
{"v":1,"range":{"from":"...","to":"...","bucket":"15m","bucket_seconds":900},
 "totals":{"sessions":4,"live_sessions":1,"turns":19,"edits":88,"files":21,"lines_added":900,"lines_removed":210,
   "tokens":{"input":1,"output":1,"cache_read":1,"cache_write":1,"thinking":1,"counted":3},"api_cost":13.94,"api_cost_complete":true,
   "peak_per_minute":52000,"peak_at":"...","peak_session":"api-a9"},
 "timeline":[{"at":"...","counted":120000,"cache_read":2000000,"per_minute":8000,"cache_read_per_minute":133000,"api_cost":0.9,
   "edits":3,"lines_added":40,"lines_removed":2,"sessions_active":2,"session":"abc"}],
 "spikes":[{"at":"...","counted":800000,"per_minute":53000,"times_normal":4.1,"session":"abc","session_name":"api-a9","project":"...","turn":"01JB...","prompt":"rewrite the parser"}],
 "projects":[{"id":"3f9a...","root":"/home/me/proj","name":"proj","tokens":{...},"api_cost":9.2,"edits":60,"files":12,"sessions":3,"share":0.72,"accounts":["3f2a..."],"series":[0,1200,...]}],
 "sessions":[{"id":"abc","name":"api-a9","project":"...","project_id":"3f9a...","account":"3f2a...","model":"claude-opus-5-5","status":"busy","alive":true,
   "turns":4,"tokens":{...},"api_cost":3.1,"edits":14,"peak_per_minute":52000,"series":[...]}],
 "models":[{"model":"claude-opus-5-5","tokens":{...},"api_cost":12.1,"priced":true,"share":0.8}],
 "tools":[{"tool":"Edit","edits":40}],
 "top_files":[{"path":"a.go","project":"...","project_id":"...","edits":12,"lines_added":80,"lines_removed":9,"sessions":2,"last_at":"...","last_event":"01JB...","activity":[0,1,...]}],
 "accounts":[{"id":"3f2a...","email":"me@example.com","plan":"Max 5x","tokens":{...},"api_cost":9.1,"sessions":3,"share":0.74}],
 "limits":[{"account":"3f2a...","email":"...","plan":"...","kind":"five_hour","latest":24,"resets_at":"...","points":[{"at":"...","percent":24,"reset":false}]}],
 "missing_history":0}
// counted = input + cache writes + output, what counts toward usage limits; cache reads are shown apart.
// api_cost is what the tokens would cost at Anthropic's API prices, not what a subscription pays.
// limits are per account and never summed; missing_history counts sessions from before metrics that
// `pawprints usage backfill` can fill in.

// pawprints limits record --session ID --kind five_hour|seven_day --percent N [--resets-at TIME] --json
// Whiskers calls this as a usage-limit window moves; the account is taken from the session
// pawprints usage backfill [--here] --json   rebuilds token history from the transcripts still on disk
{"sessions":20,"missing_transcripts":10,"minutes":6092}

// pawprints ui [--port 7878] [--range 24h] [--no-open] [--keep]   serves the charts and history in the browser;
// prints its URL on the first line and exits a minute after the last page closes. Not for tool calls.

// pawprints clear --json --older-than 3m | --session ID | --project | --all [--pins] [--dry-run] [--yes]
{"scope":"history older than 3m","projects":1,"sessions":3,"events":120,"files":30,"bytes":401234,"cut_at":"2026-07-01T00:00:00+03:00","kept_pins":1}

// pawprints theme list --json          one line per theme
{"id":"tokyo-night","name":"Tokyo Night","source":"builtin","dark":true,"current":false,"error":""}
// pawprints theme show <id> --json      the palette by role
// pawprints theme set <id> --json       {"theme":"tokyo-night"}
// pawprints theme import <file-or-dir>  .json role files or kitty .conf files
// pawprints theme new <name> [--from <id>] --json   {"id":"my-theme","path":".../themes/my-theme.json"}

// pawprints setup --json               the integrations checklist, one line per row; no changes
{"id":"cursor","title":"Cursor extension","detail":"cursor found","state":"outdated","note":"v0.0.9 installed → update to v0.1.0","default":true}
// state: missing | outdated | current | provided | unavailable
// pawprints setup --yes --json         applies every row with default true
{"id":"cursor","ok":true,"result":"installed v0.1.0 in Cursor; reload its window to load it"}

// pawprints info --json
{"version":"v0.1.0","home":"/home/me/.pawprints","project_root":"...","project_id":"...","tracked":true,"hook_settings":false,"hook_plugin":true,...}

// pawprints doctor --json
{"checks":[{"name":"git","ok":true,"detail":"git version 2.55.0"},{"name":"hooks","ok":false,"detail":"...","fix":"pawprints install"}]}

// pawprints watch --json [--all]        streams until killed; do not run from a tool call without a timeout
{"kind":"event","event":{...},"project":"..."}
{"kind":"session","session":{...},"project":"..."}
{"kind":"heartbeat"}

// errors
{"error":"no event matches \"01JX\"","exit":1}
```

## Common workflows

### What did I change in this session?
```
pawprints log --json --session current
pawprints turns --json --session current
```

### Only what Claude did, or only what the person did
```
pawprints log --json --session current --kind edit,background
pawprints log --json --session current --kind external
```

### Show the user what a specific edit did
```
pawprints show <event-id> --json
```

### Revert my last turn after the user says it was wrong
```
pawprints undo --turn --dry-run --json      # plan
pawprints undo --turn --yes --json          # after the user agrees
```
If the result is exit 3, the user edited the file since. Ask whether to
`--three-way` (keeps their edit, may leave conflict markers) or `--force`.

### Give the user a patch of everything this session did
```
pawprints export --session current > /tmp/session.patch
```

### Resolve a pawprints: reference the user pasted
```
pawprints show 01JB4X9M --json
```

### How much did this session spend, and when?
```
pawprints metrics --json --session current --since 24h
pawprints turns --json --session current       # tokens and api_cost per prompt
```

## Things that will break

- `pawprints` with no subcommand opens the TUI and takes over the terminal.
- `pawprints setup` without `--yes` or `--json` opens an interactive checklist.
- `pawprints watch` never exits on its own, and neither does `pawprints ui` while a page is open.
- `pawprints hook ...` is for Claude Code's hook system; it reads hook JSON
  from stdin and does nothing useful when invoked by hand.
- `clear --all` deletes history for every project on the machine.
- Hashes are git blob hashes; `cat` accepts a unique prefix.

## Discoverability

`pawprints agent-guide` prints this document.
