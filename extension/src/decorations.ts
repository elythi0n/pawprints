import * as vscode from "vscode";
import { Ext } from "./ext";
import { countsLabel, timeAgo } from "./format";
import { Hunk, HunksResult, PawEvent } from "./types";

interface FileMarks {
  event: PawEvent;
  hunks: Hunk[];
}

export interface LensTarget {
  root: string;
  event: PawEvent;
}

function hunkRange(hunk: Hunk, lineCount: number): vscode.Range {
  const last = Math.max(0, lineCount - 1);
  const start = Math.min(last, Math.max(0, hunk.new_start - 1));
  const end = hunk.new_lines > 0 ? Math.min(last, start + hunk.new_lines - 1) : start;
  return new vscode.Range(start, 0, end, 0);
}

/** Gutter marks and CodeLenses for the hunks of Claude's latest edit of each open file. */
export class ChangeMarks implements vscode.CodeLensProvider, vscode.Disposable {
  private readonly cache = new Map<string, Promise<FileMarks | undefined>>();
  private readonly lensEmitter = new vscode.EventEmitter<void>();
  private readonly gutter: vscode.TextEditorDecorationType;
  readonly onDidChangeCodeLenses = this.lensEmitter.event;

  constructor(private readonly ext: Ext) {
    this.gutter = vscode.window.createTextEditorDecorationType({
      gutterIconPath: vscode.Uri.joinPath(ext.context.extensionUri, "media", "gutter-mark.svg"),
      gutterIconSize: "contain",
      overviewRulerColor: new vscode.ThemeColor("editorOverviewRuler.modifiedForeground"),
      overviewRulerLane: vscode.OverviewRulerLane.Left,
    });
  }

  private key(root: string, relativePath: string): string {
    return `${root}\u0000${relativePath}`;
  }

  private load(root: string, relativePath: string): Promise<FileMarks | undefined> {
    const key = this.key(root, relativePath);
    let p = this.cache.get(key);
    if (!p) {
      p = this.fetch(root, relativePath).catch((e) => {
        this.cache.delete(key);
        this.ext.log(`marks: ${e instanceof Error ? e.message : String(e)}`);
        return undefined;
      });
      this.cache.set(key, p);
    }
    return p;
  }

  private async fetch(root: string, relativePath: string): Promise<FileMarks | undefined> {
    const data = this.ext.data(root);
    const event = await data.latestEventFor(relativePath);
    if (!event || event.after === null || event.binary) {
      return undefined;
    }
    const result = await this.ext.requireBinary().jsonOne<HunksResult>(["hunks", event.id, "--against", "previous"], root);
    return { event, hunks: result.hunks ?? [] };
  }

  /** Drops cached marks for a file (or a whole root) and redraws the editors showing it. */
  invalidate(root?: string, relativePath?: string): void {
    if (root === undefined) {
      this.cache.clear();
    } else if (relativePath === undefined) {
      for (const key of [...this.cache.keys()]) {
        if (key.startsWith(root + "\u0000")) {
          this.cache.delete(key);
        }
      }
    } else {
      this.cache.delete(this.key(root, relativePath));
    }
    this.lensEmitter.fire();
    this.decorateVisible();
  }

  decorateVisible(): void {
    for (const editor of vscode.window.visibleTextEditors) {
      void this.decorate(editor);
    }
  }

  async decorate(editor: vscode.TextEditor): Promise<void> {
    if (!this.ext.showGutterMarks || !this.ext.binary) {
      editor.setDecorations(this.gutter, []);
      return;
    }
    const location = this.ext.locate(editor.document.uri);
    if (!location) {
      editor.setDecorations(this.gutter, []);
      return;
    }
    const marks = await this.load(location.root, location.relativePath);
    if (!marks) {
      editor.setDecorations(this.gutter, []);
      return;
    }
    const hover = new vscode.MarkdownString(
      `Changed by Claude ${timeAgo(marks.event.ts)} (${marks.event.tool || marks.event.kind}, ${countsLabel(marks.event.lines_added, marks.event.lines_removed)})`
    );
    const decorations: vscode.DecorationOptions[] = marks.hunks.map((hunk) => ({
      range: hunkRange(hunk, editor.document.lineCount),
      hoverMessage: hover,
    }));
    editor.setDecorations(this.gutter, decorations);
  }

  async provideCodeLenses(document: vscode.TextDocument): Promise<vscode.CodeLens[]> {
    if (!this.ext.showCodeLens || !this.ext.binary) {
      return [];
    }
    const location = this.ext.locate(document.uri);
    if (!location) {
      return [];
    }
    const marks = await this.load(location.root, location.relativePath);
    if (!marks || marks.hunks.length === 0) {
      return [];
    }
    const target: LensTarget = { root: location.root, event: marks.event };
    const lenses: vscode.CodeLens[] = [];
    for (const hunk of marks.hunks) {
      const range = hunkRange(hunk, document.lineCount);
      const line = new vscode.Range(range.start.line, 0, range.start.line, 0);
      lenses.push(
        new vscode.CodeLens(line, { title: `Claude · ${timeAgo(marks.event.ts)}`, command: "" }),
        new vscode.CodeLens(line, { title: "diff", command: "pawprints.diffPrevious", arguments: [target] }),
        new vscode.CodeLens(line, { title: "revert", command: "pawprints.restoreBefore", arguments: [target] })
      );
    }
    return lenses;
  }

  dispose(): void {
    this.gutter.dispose();
    this.lensEmitter.dispose();
  }
}
