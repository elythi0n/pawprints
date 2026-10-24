import * as vscode from "vscode";
import { Ext } from "./ext";
import { renderStats } from "./statsText";
import { Stats } from "./types";
import { STATS_SCHEME, VERSION_SCHEME, VersionRef, parseVersionQuery, versionQuery, versionUriPath } from "./uris";

export function versionUri(ref: VersionRef): vscode.Uri {
  return vscode.Uri.from({ scheme: VERSION_SCHEME, path: versionUriPath(ref.path), query: versionQuery(ref) });
}

/** Serves stored versions as read-only documents; the content comes from `pawprints cat <hash>`. */
export class VersionContentProvider implements vscode.TextDocumentContentProvider {
  constructor(private readonly ext: Ext) {}

  async provideTextDocumentContent(uri: vscode.Uri): Promise<string> {
    const query = parseVersionQuery(uri.query);
    if (!query.hash) {
      return "";
    }
    const bytes = await this.ext.requireBinary().raw(["cat", query.hash], query.root || undefined);
    return bytes.toString("utf8");
  }
}

export function statsUri(root: string | undefined, allProjects: boolean): vscode.Uri {
  const params = new URLSearchParams();
  params.set("root", root ?? "");
  params.set("all", allProjects ? "1" : "0");
  params.set("t", String(Date.now()));
  return vscode.Uri.from({ scheme: STATS_SCHEME, path: "/Pawprints Stats", query: params.toString() });
}

export class StatsContentProvider implements vscode.TextDocumentContentProvider {
  constructor(private readonly ext: Ext) {}

  async provideTextDocumentContent(uri: vscode.Uri): Promise<string> {
    const params = new URLSearchParams(uri.query);
    const root = params.get("root") || undefined;
    const args = params.get("all") === "1" ? ["stats", "--all"] : ["stats"];
    const stats = await this.ext.requireBinary().jsonOne<Stats>(args, root);
    const title = root && params.get("all") !== "1" ? `Pawprints Stats: ${root}` : "Pawprints Stats: all projects";
    return renderStats(stats, title);
  }
}
