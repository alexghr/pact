import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  const dir = dirname(fileURLToPath(import.meta.url));
  const options = JSON.parse(readFileSync(join(dir, "options.json"), "utf8"));
  // Keep this invocation's binding across native /new, /resume and /reload.
  const bound = join(dir, "bound");

  for (const [name, url] of Object.entries(options.mcp ?? {})) {
    pi.registerMcpServer(name, {
      url: url as string,
      timeout: (options.mcpApprovalTimeoutSeconds ?? 0) + 60,
    });
  }

  async function bind(ctx: ExtensionContext) {
    const ref = ctx.sessionManager.getSessionFile();
    if (existsSync(bound) || !ref || !existsSync(ref)) return;
    try {
      const response = await fetch(options.bindingURL, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ backendRef: ref }),
        signal: AbortSignal.timeout(10000),
        redirect: "error",
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      await response.body?.cancel();
      writeFileSync(bound, ref, { mode: 0o600 });
    } catch (error) {
      // A failed binding would make this conversation unreachable by Pact.
      // Extension exceptions alone are logged by Pi and do not stop a run.
      process.stderr.write(`Pact could not save the Pi session binding: ${error}\n`);
      process.exit(1);
    }
  }

  // Pi persists the first user message before constructing the provider request.
  // Empty sessions have no file and must not become Pact's resume target.
  pi.on("before_provider_request", async (_event, ctx) => { await bind(ctx); });
  pi.on("session_start", async (_event, ctx) => { await bind(ctx); });
}
