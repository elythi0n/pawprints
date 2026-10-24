import { Binary } from "./binary";
import { turnTextMap } from "./treeModel";
import { PawEvent, Pin, Session, Turn } from "./types";

export interface EventFilter {
  session?: string;
  turn?: string;
  path?: string;
  limit?: number;
}

function filterArgs(filter: EventFilter): string[] {
  const args = ["log"];
  if (filter.limit) {
    args.push("--limit", String(filter.limit));
  }
  if (filter.session) {
    args.push("--session", filter.session);
  }
  if (filter.turn) {
    args.push("--turn", filter.turn);
  }
  if (filter.path) {
    args.push("--path", filter.path);
  }
  return args;
}

/** Cached reads for one project root. invalidate() drops everything; the trees refetch lazily. */
export class ProjectData {
  private cache = new Map<string, Promise<unknown>>();

  constructor(
    private readonly binary: Binary,
    readonly root: string
  ) {}

  invalidate(): void {
    this.cache.clear();
  }

  private memo<T>(key: string, load: () => Promise<T>): Promise<T> {
    let p = this.cache.get(key) as Promise<T> | undefined;
    if (!p) {
      p = load().catch((e) => {
        this.cache.delete(key);
        throw e;
      });
      this.cache.set(key, p);
    }
    return p;
  }

  events(filter: EventFilter): Promise<PawEvent[]> {
    return this.memo(`events:${JSON.stringify(filter)}`, () => this.binary.list<PawEvent>(filterArgs(filter), this.root));
  }

  sessions(): Promise<Session[]> {
    return this.memo("sessions", () => this.binary.list<Session>(["sessions"], this.root));
  }

  liveSessions(allProjects: boolean): Promise<Session[]> {
    const args = allProjects ? ["sessions", "--live", "--all"] : ["sessions", "--live"];
    return this.memo(`live:${allProjects}`, () => this.binary.list<Session>(args, this.root));
  }

  turns(sessionId: string): Promise<Turn[]> {
    return this.memo(`turns:${sessionId}`, () => this.binary.list<Turn>(["turns", "--session", sessionId], this.root));
  }

  pins(): Promise<Pin[]> {
    return this.memo("pins", () => this.binary.list<Pin>(["pins"], this.root));
  }

  async turnText(turnId: string): Promise<string | undefined> {
    if (!turnId) {
      return undefined;
    }
    const map = await this.memo("turnTexts", async () => turnTextMap(await this.sessions()));
    return map.get(turnId);
  }

  async latestEventFor(relativePath: string): Promise<PawEvent | undefined> {
    const events = await this.events({ path: relativePath, limit: 1 });
    return events[0];
  }
}
