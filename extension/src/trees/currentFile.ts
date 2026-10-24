import * as vscode from "vscode";
import { BaseTree, versionNodes } from "./base";
import { MessageNode, PawNode } from "./nodes";

/** Versions of the file in the active editor. */
export class CurrentFileTree extends BaseTree {
  private editorUri: vscode.Uri | undefined = vscode.window.activeTextEditor?.document.uri;

  setEditor(editor: vscode.TextEditor | undefined): void {
    if (editor && editor.document.uri.scheme !== "file") {
      return;
    }
    const next = editor?.document.uri;
    if (next?.toString() === this.editorUri?.toString()) {
      return;
    }
    this.editorUri = next;
    this.refresh();
  }

  protected override groupsByFolder(): boolean {
    return false;
  }

  protected async rootChildren(): Promise<PawNode[]> {
    if (!this.editorUri) {
      return [new MessageNode("No file open", "file")];
    }
    const location = this.ext.locate(this.editorUri);
    if (!location) {
      return [new MessageNode("File is outside the workspace", "file")];
    }
    const data = this.ext.data(location.root);
    const events = await data.events({ path: location.relativePath, limit: this.ext.recentLimit });
    if (events.length === 0) {
      return [new MessageNode("No Claude edits recorded for this file", "info")];
    }
    return versionNodes(data, events);
  }
}
