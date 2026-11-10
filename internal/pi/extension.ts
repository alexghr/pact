import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as sleep } from "node:timers/promises";
import { builtinProviders } from "@earendil-works/pi-ai/providers/all";
import type { OAuthCredential } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

async function renewOpenAI(url: string, signal: AbortSignal): Promise<OAuthCredential> {
  const unavailable = "Host Pi could not renew OpenAI credentials. Check the host network and pi auth check --provider openai; reconnect ChatGPT with host Pi /login if needed, then retry here. The conversation is preserved.";
  if (!url) throw new Error(unavailable);
  for (let attempt = 0; attempt < 2; attempt++) {
    let response: Response | undefined;
    try {
      response = await fetch(url, { method: "POST", signal, redirect: "error" });
      if (response.ok) {
        const value = await response.json();
        if (value.type === "oauth" && typeof value.access === "string" && value.access &&
            Number.isFinite(value.expires) && value.expires > Date.now() + 5 * 60 * 1000) {
          return { type: "oauth", access: value.access, refresh: "", expires: value.expires,
            clientId: value.clientId, scopes: value.scopes };
        }
      }
    } catch {
      // Fetch and JSON errors can contain bridge URLs or credential values.
    } finally {
      await response?.body?.cancel().catch(() => {});
    }
    if (signal.aborted) break;
    if (attempt === 0) {
      try { await sleep(1000, undefined, { signal }); } catch { break; }
    }
  }
  throw new Error(unavailable);
}

export default function (pi: ExtensionAPI) {
  const dir = dirname(fileURLToPath(import.meta.url));
  const options = JSON.parse(readFileSync(join(dir, "options.json"), "utf8"));
  // Keep this invocation's binding across native /new, /resume and /reload.
  const bound = join(dir, "bound");

  const provider = builtinProviders().find((provider) => provider.id === "openai")!;
  pi.registerProvider({
    ...provider,
    auth: {
      ...provider.auth,
      oauth: {
        ...provider.auth.oauth!,
        async login() { throw new Error("Sign in with ChatGPT using host Pi /login, then retry here."); },
        refresh: (_credential, signal) => renewOpenAI(options.authURL, signal),
      },
    },
  });

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
  pi.on("session_start", async (event, ctx) => {
    await bind(ctx);
    // Native navigation and /reload also start sessions, but are not launches.
    // sendUserMessage treats the assignment as task input, not CLI files or commands.
    if (event.reason === "startup" && options.prompt) {
      pi.sendUserMessage(options.prompt);
    }
  });
}
