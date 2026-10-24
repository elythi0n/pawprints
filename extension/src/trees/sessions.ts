import * as vscode from "vscode";
import { BaseTree } from "./base";
import { FolderNode, MessageNode, PawNode, SessionNode } from "./nodes";

/** Session → turn → file → version. */
export class SessionsTree extends BaseTree {
  private view: vscode.TreeView<PawNode> | undefined;
  private folderNodes = new Map<string, FolderNode>();
  private sessionNodes = new Map<string, SessionNode>();

  attach(view: vscode.TreeView<PawNode>): void {
    this.view = view;
  }

  protected async rootChildren(root: string, parent: PawNode | undefined): Promise<PawNode[]> {
    if (parent instanceof FolderNode) {
      this.folderNodes.set(root, parent);
    }
    const sessions = await this.ext.data(root).sessions();
    if (sessions.length === 0) {
      return [new MessageNode("No sessions recorded yet", "info")];
    }
    const sorted = [...sessions].sort((a, b) => (b.started_at ?? "").localeCompare(a.started_at ?? ""));
    return sorted.map((s) => {
      const node = new SessionNode(root, s, false, parent);
      this.sessionNodes.set(`${root}:${s.id}`, node);
      return node;
    });
  }

  async reveal(root: string, sessionId: string): Promise<void> {
    if (!this.view) {
      return;
    }
    let node = this.sessionNodes.get(`${root}:${sessionId}`);
    if (!node) {
      const roots = this.ext.roots();
      const parent = roots.length > 1 ? (this.folderNodes.get(root) ?? new FolderNode(root)) : undefined;
      if (parent) {
        this.folderNodes.set(root, parent);
      }
      await this.rootChildren(root, parent);
      node = this.sessionNodes.get(`${root}:${sessionId}`);
    }
    if (!node) {
      throw new Error(`Session ${sessionId} is not in the history of ${root} yet`);
    }
    await this.view.reveal(node, { select: true, focus: true, expand: true });
  }
}
