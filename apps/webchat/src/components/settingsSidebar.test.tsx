// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { dictionaries } from "../i18n";
import { WorkspaceSettingsSidebar } from "./settingsSidebar";

describe("WorkspaceSettingsSidebar", () => {
  it("navigates settings, searches labels, and returns to the app", async () => {
    const onTabChange = vi.fn();
    const onBack = vi.fn();
    const host = document.createElement("div");
    const root = createRoot(host);

    try {
      await act(async () => root.render(
        <WorkspaceSettingsSidebar
          text={dictionaries.en}
          language="en"
          tab="settings"
          pendingApprovalCount={2}
          pendingCandidateCount={3}
          onTabChange={onTabChange}
          onBack={onBack}
        />
      ));

      expect(host.textContent).toContain("Back to app");
      expect(host.textContent).toContain("General");
      expect(host.textContent).toContain("Approvals");
      expect(host.textContent).toContain("Memory");
      expect(host.querySelector('[aria-current="page"]')?.textContent).toContain("General");

      const search = host.querySelector('input[type="search"]') as HTMLInputElement;
      await act(async () => {
        const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
        setter?.call(search, "memo");
        search.dispatchEvent(new Event("input", { bubbles: true }));
      });
      expect(host.querySelectorAll(".settingsPageNavigation button")).toHaveLength(1);
      expect(host.textContent).toContain("Memory");

      await act(async () => (host.querySelector(".settingsPageNavigation button") as HTMLButtonElement).click());
      expect(onTabChange).toHaveBeenCalledWith("memory");
      await act(async () => (host.querySelector(".settingsPageBack") as HTMLButtonElement).click());
      expect(onBack).toHaveBeenCalledOnce();
    } finally {
      await act(async () => root.unmount());
    }
  });
});
