import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { dictionaries } from "../i18n";
import { SessionSidebar } from "./sidebar";

describe("SessionSidebar MCP conversations", () => {
  it("keeps the managed title visible without ordinary rename or delete controls", () => {
    const markup = renderToStaticMarkup(
      <SessionSidebar
        text={dictionaries.en}
        language="en"
        ready={null}
        sessions={[{
          id: "s_mcp_binding_a",
          title: "AI · device-a",
          source: "mcp",
          created_at: "2026-08-18T00:00:00Z",
          updated_at: "2026-08-18T00:00:00Z"
        }]}
        activeSession="s_mcp_binding_a"
        pendingApprovalCount={0}
        pendingCandidateCount={0}
        editingSession=""
        sessionTitleDraft=""
        sessionActionId=""
        onLanguageChange={() => {}}
        onCreateSession={() => {}}
        onSelectSession={() => {}}
        onStartRename={() => {}}
        onCancelRename={() => {}}
        onRenameSubmit={() => {}}
        onTitleDraftChange={() => {}}
        onDeleteSession={() => {}}
      />
    );

    expect(markup).toContain("AI · device-a");
    expect(markup).not.toContain(dictionaries.en.nav.renameSession);
    expect(markup).not.toContain(dictionaries.en.nav.deleteSession);
  });
});
