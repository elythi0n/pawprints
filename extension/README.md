# Pawprints for VS Code and Cursor

A sidebar over the `pawprints` binary: every file edit Claude Code makes, with diffs, restore, undo, pins, and live session status.

## Requirements

The extension is a thin client. It needs the `pawprints` binary, found in this order:

1. the `pawprints.binaryPath` setting,
2. `pawprints` on `PATH`,
3. `<global storage>/bin/pawprints`.

Run `install.sh` from the GitHub release to install both the binary and this extension, or set `pawprints.binaryPath` by hand. The extension never downloads anything.

On first activation it checks whether the Claude Code hook is registered and offers to run `pawprints install`. The answer is remembered in `pawprints.autoInstallHook`.

## Views

| View | Contents |
| :- | :- |
| Live | Running sessions with status and last prompt. Click reveals the session in the Sessions view |
| Recent | The last `pawprints.recentLimit` events, newest first |
| Sessions | Session → turn → file → version |
| Files | Every touched file, expanding to its versions |
| Pins | Versions kept forever with `pawprints pin` |
| Current file | Versions of the file in the active editor |

Clicking a version diffs it against the previous version. The context menu offers diff against the current file, compare two selected versions, open, restore, pin, copy as content, snippet, or `pawprints:<id>` reference, and send the reference to the terminal.

The status bar shows `● 2 working · 1 waiting` while sessions are running; the activity bar badge counts sessions waiting for input.

`Ctrl+Alt+P` (`Cmd+Alt+P` on macOS) opens the diff of Claude's last change in the current file.

## Settings

| Setting | Default | Meaning |
| :- | :- | :- |
| `pawprints.binaryPath` | `""` | Explicit path to the binary |
| `pawprints.home` | `""` | Overrides `~/.pawprints`, passed as `PAWPRINTS_HOME` |
| `pawprints.recentLimit` | `200` | Events loaded into the Recent and Files views |
| `pawprints.showGutterMarks` | `false` | Mark lines of Claude's latest edit in the gutter |
| `pawprints.showCodeLens` | `true` | "Claude · 3 min ago · diff · revert" above each changed hunk |
| `pawprints.autoInstallHook` | `"ask"` | `ask`, `always`, or `never` |
| `pawprints.showAllProjects` | `false` | Live sessions from every project, not only open folders |

## Development

```
npm install
npm run build      # bundle to dist/extension.js
npm run lint
npm test           # unit tests, no VS Code needed
npm run package    # pawprints-0.1.0.vsix
```

Install the vsix with `code --install-extension pawprints-0.1.0.vsix` or `cursor --install-extension ...`.
