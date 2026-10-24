import * as vscode from "vscode";
import { Ext } from "./ext";
import { Info, InstallResult } from "./types";

export async function installHook(ext: Ext, root: string | undefined): Promise<InstallResult> {
  const result = await ext.requireBinary().jsonOne<InstallResult>(["install"], root);
  ext.log(`hook installed: ${JSON.stringify(result)}`);
  return result;
}

/** On activation: if no hook is registered, offer to install it, honouring pawprints.autoInstallHook. */
export async function promptForHook(ext: Ext): Promise<void> {
  const mode = ext.autoInstallHook;
  if (mode === "never" || !ext.binary) {
    return;
  }
  const root = ext.roots()[0];
  let info: Info;
  try {
    info = await ext.binary.jsonOne<Info>(["info"], root);
  } catch (e) {
    ext.log(`info failed: ${e instanceof Error ? e.message : String(e)}`);
    return;
  }
  if (info.hook_settings || info.hook_plugin) {
    return;
  }
  if (mode === "always") {
    await ext.guard(async () => {
      await installHook(ext, root);
    });
    return;
  }
  const choice = await vscode.window.showInformationMessage(
    "Pawprints: the Claude Code hook is not installed, so edits are not being recorded.",
    "Install hook",
    "Not now",
    "Never"
  );
  if (choice === "Install hook") {
    await ext.guard(async () => {
      const result = await installHook(ext, root);
      void vscode.window.showInformationMessage(`Pawprints hook installed in ${result.settings_path}`);
    });
  } else if (choice === "Never") {
    await vscode.workspace.getConfiguration("pawprints").update("autoInstallHook", "never", vscode.ConfigurationTarget.Global);
  }
}
