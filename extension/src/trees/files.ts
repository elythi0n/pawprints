import { BaseTree, fileNodes } from "./base";
import { MessageNode, PawNode } from "./nodes";

/** Every touched file in the recent window, each expanding to its versions. */
export class FilesTree extends BaseTree {
  protected async rootChildren(root: string, parent: PawNode | undefined): Promise<PawNode[]> {
    const limit = this.ext.recentLimit;
    const events = await this.ext.data(root).events({ limit });
    if (events.length === 0) {
      return [new MessageNode("No Claude edits recorded yet", "info")];
    }
    return fileNodes(root, events, { limit }, parent).sort((a, b) => a.path.localeCompare(b.path));
  }
}
