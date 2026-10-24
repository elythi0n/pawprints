import { ChildProcess, spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import * as vscode from "vscode";
import type { Ext } from "./ext";

// The metrics panel shows the page `pawprints ui` serves, in an iframe. The
// binary is started once, on a free port, and stopped when the panel closes.

let panel: vscode.WebviewPanel | undefined;
let server: ChildProcess | undefined;
let serverUrl: string | undefined;
// opening is set while the panel is being opened, so a second click waits
// for the same server instead of starting another.
let opening: Promise<void> | undefined;

/** Reads the first line `pawprints ui` prints: its URL. */
function startServer(path: string, env: NodeJS.ProcessEnv): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = spawn(path, ["ui", "--no-open", "--keep", "--port", "0"], { env, stdio: ["ignore", "pipe", "pipe"] });
    server = child;
    let out = "";
    let err = "";
    const fail = (e: Error) => {
      clearTimeout(timer);
      // A server that never answered is stopped, so none is left running.
      if (server === child) {
        server = undefined;
        serverUrl = undefined;
      }
      child.kill();
      reject(e);
    };
    const timer = setTimeout(() => fail(new Error("pawprints ui did not start within 10 seconds")), 10_000);
    child.stdout.on("data", (chunk: Buffer) => {
      out += chunk.toString();
      const line = out.split("\n")[0].trim();
      if (out.includes("\n") && line.startsWith("http")) {
        clearTimeout(timer);
        resolve(line);
      }
    });
    child.stderr.on("data", (chunk: Buffer) => {
      err += chunk.toString();
    });
    child.on("error", (e) => fail(e));
    child.on("exit", (code) => fail(new Error(err.trim() || `pawprints ui exited with code ${code}`)));
  });
}

/** Stops the server the panel started. */
export function stopMetricsServer(): void {
  server?.kill();
  server = undefined;
  serverUrl = undefined;
}

/** Opens the metrics panel, or brings it to the front when it is open. */
export async function openMetrics(ext: Ext, extensionUri: vscode.Uri): Promise<void> {
  if (panel) {
    panel.reveal();
    return;
  }
  if (!opening) {
    opening = open(ext, extensionUri).finally(() => {
      opening = undefined;
    });
  }
  return opening;
}

async function open(ext: Ext, extensionUri: vscode.Uri): Promise<void> {
  const binary = ext.requireBinary();
  if (!serverUrl) {
    serverUrl = await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Window, title: "Starting Pawprints metrics" },
      () => startServer(binary.path, binary.env())
    );
  }
  const url = `${serverUrl}&embed=vscode`;
  const origin = new URL(url).origin;
  panel = vscode.window.createWebviewPanel("pawprints.metrics", "Pawprints Metrics", vscode.ViewColumn.Active, {
    enableScripts: true,
    retainContextWhenHidden: true,
  });
  panel.iconPath = vscode.Uri.joinPath(extensionUri, "media", "pawprints.svg");
  panel.webview.html = page(url, origin, randomBytes(16).toString("base64"));
  const themeChange = vscode.window.onDidChangeActiveColorTheme(() => {
    void panel?.webview.postMessage({ type: "theme-changed" });
  });
  panel.onDidDispose(() => {
    themeChange.dispose();
    panel = undefined;
    stopMetricsServer();
  });
}

/**
 * The webview page: one iframe on the local server, plus a script that sends
 * the editor's theme colors into it, so the charts match the editor.
 */
function page(url: string, origin: string, nonce: string): string {
  const csp = [
    "default-src 'none'",
    `frame-src ${origin}`,
    "style-src 'unsafe-inline'",
    `script-src 'nonce-${nonce}'`,
  ].join("; ");
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="${csp}">
<style>
  html, body { margin: 0; padding: 0; height: 100%; overflow: hidden; background: var(--vscode-editor-background); }
  iframe { border: 0; width: 100%; height: 100%; display: block; }
</style>
</head>
<body>
<iframe id="page" src="${url}" title="Pawprints Metrics"></iframe>
<script nonce="${nonce}">
  const frame = document.getElementById("page");
  const origin = ${JSON.stringify(origin)};
  const pick = (...names) => {
    const cs = getComputedStyle(document.documentElement);
    for (const n of names) {
      const v = cs.getPropertyValue(n).trim();
      if (v) return v;
    }
    return "";
  };
  function send() {
    const light = document.body.classList.contains("vscode-light") || document.body.classList.contains("vscode-high-contrast-light");
    frame.contentWindow.postMessage({
      type: "pawprints-theme",
      kind: light ? "light" : "dark",
      colors: {
        "--bg": pick("--vscode-editor-background"),
        "--surface": pick("--vscode-sideBar-background", "--vscode-editor-background"),
        "--raise": pick("--vscode-editorWidget-background", "--vscode-sideBar-background"),
        "--ink": pick("--vscode-editor-foreground", "--vscode-foreground"),
        "--muted": pick("--vscode-descriptionForeground"),
        "--line": pick("--vscode-panel-border", "--vscode-widget-border", "--vscode-editorGroup-border"),
        "--grid": pick("--vscode-editorIndentGuide-background1", "--vscode-editorIndentGuide-background"),
        "--paw": pick("--vscode-charts-orange"),
        "--accent": pick("--vscode-charts-orange", "--vscode-textLink-foreground"),
        "--cache": pick("--vscode-charts-purple"),
        "--teal": pick("--vscode-charts-blue"),
        "--add": pick("--vscode-charts-green", "--vscode-gitDecoration-addedResourceForeground"),
        "--del": pick("--vscode-charts-red", "--vscode-gitDecoration-deletedResourceForeground"),
        "--warn": pick("--vscode-charts-yellow"),
      },
    }, origin);
  }
  window.addEventListener("message", (ev) => {
    if (ev.data && (ev.data.type === "pawprints-ready" || ev.data.type === "theme-changed")) send();
  });
  new MutationObserver(send).observe(document.body, { attributes: true, attributeFilter: ["class"] });
  frame.addEventListener("load", send);
</script>
</body>
</html>`;
}
