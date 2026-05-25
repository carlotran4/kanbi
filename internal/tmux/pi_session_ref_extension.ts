import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

export default function (_pi: ExtensionAPI) {
  _pi.on("session_start", async (_event, ctx) => {
    const refFile = process.env.AGENT_KANBAN_SESSION_REF_FILE;
    if (!refFile) return;

    const payload = {
      sessionId: ctx.sessionManager.getSessionId(),
      sessionFile: ctx.sessionManager.getSessionFile(),
      cwd: ctx.cwd,
      header: ctx.sessionManager.getHeader(),
    };

    mkdirSync(dirname(refFile), { recursive: true });
    writeFileSync(refFile, `${JSON.stringify(payload)}\n`, "utf8");
  });
}
