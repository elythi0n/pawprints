import { isInsideAny, sortLiveSessions } from "../treeModel";
import { Session } from "../types";
import { BaseTree } from "./base";
import { MessageNode, PawNode, SessionNode } from "./nodes";

/** Running sessions. With showAllProjects on, one flat list from every project. */
export class LiveTree extends BaseTree {
  private lastSessions: Session[] = [];

  protected override groupsByFolder(): boolean {
    return !this.ext.showAllProjects;
  }

  protected override worksWithoutFolder(): boolean {
    return this.ext.showAllProjects;
  }

  get sessions(): Session[] {
    return this.lastSessions;
  }

  /** Loads the live sessions for the status bar and badge, independent of the view being visible. */
  async load(): Promise<Session[]> {
    if (!this.ext.binary) {
      this.lastSessions = [];
      return [];
    }
    const roots = this.ext.roots();
    let sessions: Session[];
    if (this.ext.showAllProjects) {
      sessions = await this.ext.data(roots[0] ?? "").liveSessions(true);
    } else {
      sessions = [];
      for (const root of roots) {
        sessions.push(...(await this.ext.data(root).liveSessions(false)));
      }
      sessions = sessions.filter((s) => isInsideAny(s.cwd || s.project, roots));
    }
    const unique = new Map(sessions.map((s) => [s.id, s]));
    this.lastSessions = sortLiveSessions([...unique.values()]);
    return this.lastSessions;
  }

  protected async rootChildren(root: string, parent: PawNode | undefined): Promise<PawNode[]> {
    const all = await this.load();
    const sessions = this.ext.showAllProjects ? all : all.filter((s) => isInsideAny(s.cwd || s.project, [root]));
    if (sessions.length === 0) {
      return [new MessageNode("No running sessions", "circle-outline")];
    }
    return sessions.map((s) => new SessionNode(this.sessionRoot(s, root), s, true, parent));
  }

  private sessionRoot(session: Session, fallback: string): string {
    const match = this.ext.roots().find((r) => isInsideAny(session.cwd || session.project, [r]));
    return match ?? session.project ?? fallback;
  }
}
