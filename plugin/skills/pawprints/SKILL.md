---
name: pawprints
description: Browse, diff, undo, and export the history of files Claude edited in this project. Use when the user asks what Claude changed, wants an edit reverted or restored, pastes a pawprints:<id> reference, or asks for a patch of a session or turn.
---

# Pawprints

Pawprints records every file edit made through Claude's edit tools, with the
content before and after, the session, and the prompt (turn) that caused it.
The `pawprints` binary answers questions about that history and can write
versions back.

## First

Run `pawprints agent-guide` once per session and follow it. It documents every
command and the exact JSON shapes. The short version:

- Always pass `--json`. Listings are one object per line; errors are
  `{"error":"...","exit":N}` on stdout.
- `--session current` means the session running in this directory.
- Never run bare `pawprints` (it opens a full-screen UI) or `pawprints watch`
  (it never exits).

## References

When a prompt contains `pawprints:<id>` (often followed by a path), the user is
pointing at one recorded version. Resolve it with:

```
pawprints show <id> --json
```

and use the `before`, `after`, and `diff` fields. The id may be a prefix.

## What did I change?

```
pawprints turns --json --session current
pawprints log --json --session current --limit 50
pawprints show <event-id> --json
```

## Reverting

`undo` and `restore` write files. Plan first, show the plan, then apply only
after the user agrees:

```
pawprints undo --turn --dry-run --json      # the latest prompt's edits
pawprints undo --turn --yes --json
pawprints restore <event-id> --dry-run --json
pawprints restore <event-id> --yes --json
```

Exit code 3 means the user edited the file after Claude did. Ask whether to
`--three-way` (keeps their edit, may leave conflict markers) or `--force`.

## Patches

```
pawprints export --session current > /tmp/session.patch
pawprints export --turn <turn-id> > /tmp/turn.patch
```

## Pins

`pawprints pin <event-id> -m "why"` keeps a version through retention. Offer
it before a risky refactor.
