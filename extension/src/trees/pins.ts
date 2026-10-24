import { BaseTree } from "./base";
import { MessageNode, PawNode, PinNode } from "./nodes";

export class PinsTree extends BaseTree {
  protected async rootChildren(root: string, parent: PawNode | undefined): Promise<PawNode[]> {
    const pins = await this.ext.data(root).pins();
    if (pins.length === 0) {
      return [new MessageNode("No pinned versions", "pin")];
    }
    return [...pins].sort((a, b) => b.at.localeCompare(a.at)).map((pin) => new PinNode(root, pin, parent));
  }
}
