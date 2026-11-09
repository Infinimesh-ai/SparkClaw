import { aiPlatformURL, inspectAILoginPage, classifyAILogin } from "./ai-platform-login.mjs";
import { captureAIChat } from "./ai-chat-export.mjs";
import { exactArgs, optionalPageID } from "./mcp-arguments.mjs";

export const backendOperationHandlers = Object.freeze({
  async "ai_platform.check"(args, client) {
    exactArgs(args, ["provider"], ["page_id"]);
    const url = aiPlatformURL(args.provider);
    const page = await client.selectPage(optionalPageID(args.page_id));
    await client.navigate(page.pageID, url);
    let state = "unconfirmed";
    for (let attempt = 0; attempt < 5; attempt++) {
      const evidence = await client.evaluate(inspectAILoginPage.toString());
      state = classifyAILogin(args.provider, evidence);
      if (state !== "unconfirmed" || attempt === 4) break;
      await client.callJSONTool("browser_wait_for", { time: 0.75 });
    }
    return { provider: args.provider, state };
  },
  async "ai_chat.export"(args, client) {
    exactArgs(args, ["provider", "url"], ["page_id"]);
    await client.selectPage(optionalPageID(args.page_id));
    return captureAIChat({ provider: args.provider, url: args.url, outputDir: client.outputDir,
      runCode: code => client.callJSONTool("browser_run_code_unsafe", { code }, 60000) });
  },
});
