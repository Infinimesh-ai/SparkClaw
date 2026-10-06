// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { WorkbenchRequests } from "./workbenchRequests";
import type { WorkbenchRequestStatus } from "../lib/workbenchRequest";

describe("persisted workbench request recovery", () => {
  it("queries an unknown original request without offering another execution", async () => {
    const host = document.createElement("div");
    const root = createRoot(host);
    const refresh = vi.fn();
    const cancel = vi.fn();
    const request: WorkbenchRequestStatus = { schema_version: 1, request_id: "a".repeat(36), input_digest: "b".repeat(64), state: "unknown" };
    await act(async () => root.render(<WorkbenchRequests requests={[request]} language="en" onRefresh={refresh} onCancel={cancel} />));
    expect(host.textContent).toContain("will not run again");
    expect([...host.querySelectorAll("button")].map((button) => button.textContent)).toEqual(["Check status"]);
    await act(async () => host.querySelector("button")!.click());
    expect(refresh).toHaveBeenCalledOnce();
    expect(cancel).not.toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("cancels only the selected live identity and hides completed requests", async () => {
    const host = document.createElement("div");
    const root = createRoot(host);
    const cancel = vi.fn();
    const request: WorkbenchRequestStatus = { schema_version: 1, request_id: "running-request", input_digest: "b".repeat(64), state: "running" };
    await act(async () => root.render(<WorkbenchRequests requests={[request, { ...request, request_id: "finished", state: "completed" }]} language="zh" onRefresh={() => {}} onCancel={cancel} />));
    expect(host.textContent).not.toContain("finished");
    await act(async () => [...host.querySelectorAll("button")].find((button) => button.textContent === "取消")!.click());
    expect(cancel).toHaveBeenCalledExactlyOnceWith("running-request");
    await act(async () => root.unmount());
  });
});
