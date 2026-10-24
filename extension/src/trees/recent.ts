import { BaseTree, versionNodes } from "./base";
import { MessageNode, PawNode } from "./nodes";

/** The last N events, newest first, one node per version with its path as description. */
export class RecentTree extends BaseTree {
  protected async rootChildren(root: string, parent: PawNode | undefined): Promise<PawNode[]> {
    const data = this.ext.data(root);
    const events = await data.events({ limit: this.ext.recentLimit });
    if (events.length === 0) {
      return [new MessageNode("No Claude edits recorded yet", "info")];
    }
    return versionNodes(data, events, { showPath: true, parent });
  }
}
