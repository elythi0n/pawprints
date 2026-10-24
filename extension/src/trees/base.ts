import * as vscode from "vscode";
import { EventFilter, ProjectData } from "../data";
import { Ext } from "../ext";
import { DirTree, buildDirTree, combineVersions, groupByFile, splitByWho } from "../treeModel";
import { PawEvent } from "../types";
import { DirNode, FileNode, FolderNode, MessageNode, PawNode, SessionNode, TurnNode, VersionNode, WhoNode, binaryNotFoundNode } from "./nodes";

export function noFolderNode(): MessageNode {
  return new MessageNode("Open a folder to see its Pawprints history", "folder-opened");
}

export async function versionNodes(
  data: ProjectData,
  events: PawEvent[],
  options: { showPath?: boolean; parent?: PawNode } = {}
): Promise<VersionNode[]> {
  const nodes: VersionNode[] = [];
  for (const event of events) {
    const prompt = await data.turnText(event.turn);
    nodes.push(new VersionNode(data.root, event, { prompt, showPath: options.showPath, parent: options.parent }));
  }
  return nodes;
}

export function fileNodes(root: string, events: PawEvent[], filter: EventFilter, parent?: PawNode): FileNode[] {
  return groupByFile(events).map((group) => new FileNode(root, group.path, filter, group.events, parent));
}

/**
 * One row per file. A file edited several times is one combined change; a
 * click opens a single diff from before the first edit to after the last.
 */
export async function changeNodes(data: ProjectData, events: PawEvent[], parent?: PawNode): Promise<VersionNode[]> {
  const nodes: VersionNode[] = [];
  for (const group of groupByFile(events)) {
    const event = combineVersions(group.events);
    const prompt = await data.turnText(event.turn);
    nodes.push(new VersionNode(data.root, event, { prompt, parent, asFile: true }));
  }
  return nodes;
}

/** Shared skeleton: binary check, folder grouping for multi-root, and the session/turn/file drill-down. */
export abstract class BaseTree implements vscode.TreeDataProvider<PawNode> {
  private readonly emitter = new vscode.EventEmitter<PawNode | undefined>();
  readonly onDidChangeTreeData = this.emitter.event;

  constructor(protected readonly ext: Ext) {}

  refresh(): void {
    this.emitter.fire(undefined);
  }

  getTreeItem(node: PawNode): vscode.TreeItem {
    return node;
  }

  getParent(node: PawNode): PawNode | undefined {
    return "parent" in node ? node.parent : undefined;
  }

  /** Whether the top level groups by workspace folder when more than one is open. */
  protected groupsByFolder(): boolean {
    return true;
  }

  /** Whether the view has something to show with no folder open. */
  protected worksWithoutFolder(): boolean {
    return false;
  }

  async getChildren(node?: PawNode): Promise<PawNode[]> {
    try {
      if (!this.ext.binary) {
        return [binaryNotFoundNode()];
      }
      if (!node) {
        return this.topLevel();
      }
      if (node instanceof FolderNode) {
        return this.rootChildren(node.root, node);
      }
      return this.childrenOf(node);
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      this.ext.log(`tree error: ${message}`);
      return [new MessageNode(message, "error")];
    }
  }

  private async topLevel(): Promise<PawNode[]> {
    const roots = this.ext.roots();
    if (roots.length === 0) {
      return this.worksWithoutFolder() ? this.rootChildren("", undefined) : [noFolderNode()];
    }
    if (roots.length > 1 && this.groupsByFolder()) {
      return roots.map((root) => new FolderNode(root));
    }
    return this.rootChildren(roots[0], undefined);
  }

  protected abstract rootChildren(root: string, parent: PawNode | undefined): Promise<PawNode[]>;

  protected async childrenOf(node: PawNode): Promise<PawNode[]> {
    if (node instanceof SessionNode) {
      return this.sessionChildren(node);
    }
    if (node instanceof TurnNode) {
      const data = this.ext.data(node.root);
      const filter: EventFilter = { session: node.sessionId, turn: node.turn.id };
      const events = await data.events(filter);
      return this.filesUnder(node, events, filter);
    }
    if (node instanceof FileNode) {
      const data = this.ext.data(node.root);
      const events = node.events ?? (await data.events({ ...node.filter, path: node.path }));
      return versionNodes(data, events, { parent: node });
    }
    if (node instanceof DirNode) {
      return this.treeChildren(node.root, node.tree, node.key, node);
    }
    if (node instanceof WhoNode) {
      return this.layoutRows(node, node.events, {}, node.key);
    }
    return [];
  }

  private async sessionChildren(node: SessionNode): Promise<PawNode[]> {
    const data = this.ext.data(node.root);
    const turns = await data.turns(node.session.id);
    if (turns.length > 0) {
      const newestFirst = [...turns].reverse();
      return newestFirst.map((turn) => new TurnNode(node.root, node.session.id, turn, node));
    }
    const filter: EventFilter = { session: node.session.id };
    const events = await data.events(filter);
    if (events.length === 0) {
      return [new MessageNode("No edits recorded in this session", "info")];
    }
    return this.filesUnder(node, events, filter);
  }

  /**
   * Files under a prompt or session, as the view's layout says: a flat list
   * (combined rows unless set to list each version) or the folder tree.
   */
  private async filesUnder(node: PawNode & { root: string; id?: string }, events: PawEvent[], filter: EventFilter): Promise<PawNode[]> {
    const key = node.id ?? JSON.stringify(filter);
    const who = splitByWho(events);
    const rows = await this.layoutRows(node, who.main, filter, key);
    if (who.agents.length > 0) {
      const sessions = await this.ext.data(node.root).sessions();
      const agents = sessions.flatMap((s) => s.agents ?? []);
      for (const a of who.agents) {
        const info = agents.find((x) => x.id === a.id);
        rows.push(new WhoNode(node.root, "agent", a.events, `${key}:agent:${a.id}`, { type: a.type, agent: info, parent: node }));
      }
    }
    if (who.outside.length > 0) {
      rows.push(new WhoNode(node.root, "outside", who.outside, `${key}:outside`, { parent: node }));
    }
    return rows;
  }

  /** One part of a prompt's files, as a list or as the folder tree. */
  private async layoutRows(node: PawNode & { root: string; id?: string }, events: PawEvent[], filter: EventFilter, key: string): Promise<PawNode[]> {
    if (events.length === 0) {
      return [];
    }
    if (this.ext.config<string>("fileLayout", "list") === "tree") {
      const tree = buildDirTree(events);
      return this.treeChildren(node.root, tree, key, node);
    }
    return this.ext.config<string>("fileVersions", "combined") === "each"
      ? fileNodes(node.root, events, filter, node)
      : changeNodes(this.ext.data(node.root), events, node);
  }

  /** A folder's subfolders, then its own files as combined rows. */
  private async treeChildren(root: string, tree: DirTree, key: string, parent: PawNode): Promise<PawNode[]> {
    const data = this.ext.data(root);
    const dirs = tree.dirs.map((d) => new DirNode(root, d, key, parent));
    const files: PawNode[] = [];
    for (const change of tree.files) {
      const prompt = await data.turnText(change.turn);
      files.push(new VersionNode(root, change, { prompt, parent, asFile: true }));
    }
    return [...dirs, ...files];
  }
}
