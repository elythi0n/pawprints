export const VERSION_SCHEME = "pawprints";
export const STATS_SCHEME = "pawprints-stats";

export interface VersionRef {
  root: string;
  path: string;
  hash: string | null;
  event: string;
  label: string;
}

export interface ParsedVersionQuery {
  root: string;
  hash: string | null;
  event: string;
  label: string;
}

/** Path component of a version URI: the project-relative path with a leading slash. */
export function versionUriPath(relativePath: string): string {
  const clean = relativePath.replace(/^\/+/, "");
  return "/" + clean;
}

export function relativePathFromUriPath(uriPath: string): string {
  return uriPath.replace(/^\/+/, "");
}

export function versionQuery(ref: VersionRef): string {
  const params = new URLSearchParams();
  params.set("hash", ref.hash ?? "");
  params.set("event", ref.event);
  params.set("label", ref.label);
  params.set("root", ref.root);
  return params.toString();
}

export function parseVersionQuery(query: string): ParsedVersionQuery {
  const params = new URLSearchParams(query);
  const hash = params.get("hash") ?? "";
  return {
    root: params.get("root") ?? "",
    hash: hash.length > 0 ? hash : null,
    event: params.get("event") ?? "",
    label: params.get("label") ?? "",
  };
}

export function referenceText(eventId: string, path: string): string {
  return `pawprints:${eventId} ${path}`;
}
