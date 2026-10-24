import * as vscode from "vscode";
import { liveCounts, statusBarText } from "./treeModel";
import { Session } from "./types";
import { PawNode } from "./trees/nodes";

/** The "● 2 working · 1 waiting" status bar item and the waiting badge on the activity bar icon. */
export class LiveStatus implements vscode.Disposable {
  private readonly item: vscode.StatusBarItem;

  constructor(private readonly liveView: vscode.TreeView<PawNode>) {
    this.item = vscode.window.createStatusBarItem("pawprints.live", vscode.StatusBarAlignment.Left, 50);
    this.item.name = "Pawprints";
    this.item.command = "pawprints.live.focus";
  }

  update(sessions: Session[]): void {
    const counts = liveCounts(sessions);
    const text = statusBarText(counts);
    if (!text) {
      this.item.hide();
      this.liveView.badge = undefined;
      return;
    }
    this.item.text = text;
    this.item.tooltip = `Claude Code: ${counts.working} working, ${counts.waiting} waiting for input, ${counts.idle} idle`;
    this.item.show();
    this.liveView.badge =
      counts.waiting > 0
        ? { value: counts.waiting, tooltip: `${counts.waiting} session${counts.waiting === 1 ? "" : "s"} waiting for input` }
        : undefined;
  }

  dispose(): void {
    this.item.dispose();
  }
}
