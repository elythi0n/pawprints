# Changelog

## Unreleased

- Accounts screen in `pawprints ui` (key 5): per account, whether it is limited and until when, the 5-hour and weekly resets, the pace, and when it fills at that pace, plus a timeline of when each account unlocks. `pawprints limits` shows the same in the terminal, `limits --json` for scripts.
- Prompts in the Sessions screen are split by day, so a session that runs past midnight or picks up days later reads in order.
- Metrics: `pawprints metrics` and `pawprints ui`, a page on localhost with token rate, spikes, lines changed, usage limits, API cost, and breakdowns by project, session, model, account, and file, plus the sidebar's history with diffs, restore, undo, and pin. Live while Claude works.
- The page keeps its place: the link carries the range, project, account, and the open session or file, so bookmarks and the back button work; live updates wait while a tooltip is open or text is selected. Sessions and Files have search and a sort dropdown, a file opens in a drawer beside the page with its change or every version, files outside every project fold into one row, stat tiles compare with the period before, Where it went splits API cost by model, each prompt shows its API cost, diffs open in the editor, and 1–4, /, [ and ] are keyboard shortcuts.
- Editor command **Pawprints: Open Metrics** shows the page in a panel in the editor's colors; `o` in the TUI Stats pane opens it, and the pane gains token and line charts.
- Token usage is counted once per reply: Claude Code writes one reply as several lines, and older versions counted each. `pawprints usage backfill` rebuilds the totals and the per-minute history.
- Subagents' tokens are counted, from their own transcripts.
- Each prompt records the Claude account it ran under, and sessions from every Claude Code config folder are read, so several accounts show apart or together.
- Cost is the API cost from built-in prices per model; `prices` in config.json still overrides.
- `diff --from` shows several edits of a file as one change; `turns --json` has tokens and API cost per prompt.
- `pawprints limits record` stores usage-limit readings, which Whiskers sends.

- Hooks record every Edit, Write, MultiEdit, and NotebookEdit with before and after content.
- Edits made outside the hooks (Bash, sed, formatters) are captured on the next hook call.
- Sessions and turns: prompts, status from Claude Code's session registry, token usage from transcripts.
- CLI: log, show, diff, cat, sessions, turns, pins, stats, watch, restore, undo, pin, export, clear, prune, install, doctor, completion, agent-guide.
- Terminal UI with Live, Recent, Sessions, Files, Pins, and Stats panes.
- Claude Code plugin with hooks and a `/pawprints` skill.
- VS Code and Cursor extension.
