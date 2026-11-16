// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { MCDConnectionSettings } from "./MCDConnectionSettings";
import type { SparkClawDesktop } from "../desktop/types";

afterEach(() => { delete window.sparkclawDesktop; vi.restoreAllMocks(); });

it("keeps disconnect available while the backend is offline", async () => {
  const disconnect=vi.fn(async()=>{});
  window.sparkclawDesktop={runtimeKind:"electron",capabilityVersion:1,mcdStatus:async()=>({available:false,disconnect_available:true,state:"offline",checkout_enabled:false}),mcdDisconnect:disconnect} as unknown as SparkClawDesktop;
  const container=document.createElement("div"),root=createRoot(container);
  await act(async()=>root.render(<MCDConnectionSettings language="en"/>));
  const button=[...container.querySelectorAll("button")].find(b=>b.textContent==="Disconnect")!;
  expect(button.disabled).toBe(false);await act(async()=>button.click());expect(disconnect).toHaveBeenCalledWith();
  await act(async()=>root.unmount());
});

it("preserves the connection action but disables it without a native qualified capability", async () => {
  const container = document.createElement("div"), root = createRoot(container);
  await act(async () => root.render(<MCDConnectionSettings language="zh" />));
  expect(container.querySelector("button")?.textContent).toBe("连接账号");
  expect(container.querySelector("button")?.disabled).toBe(true);
  expect(container.querySelector('input[type="password"]')).toBeNull();
  await act(async () => root.unmount());
});

it("connects and disconnects through native metadata methods with no renderer credential input", async () => {
  let state = "disconnected";
  const connect = vi.fn(async () => { state = "ready"; });
  const disconnect = vi.fn(async () => { state = "disconnected"; });
  window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
    mcdStatus: async () => ({ available: true, state, checkout_enabled: false }), mcdConnect: connect, mcdDisconnect: disconnect } as unknown as SparkClawDesktop;
  const container = document.createElement("div"), root = createRoot(container);
  await act(async () => root.render(<MCDConnectionSettings language="en" />));
  await act(async () => container.querySelector("button")!.click());
  expect(connect).toHaveBeenCalledWith();
  expect(container.textContent).toContain("Connected");
  const button = [...container.querySelectorAll("button")].find(item => item.textContent === "Disconnect")!;
  await act(async () => button.click());
  expect(disconnect).toHaveBeenCalledWith();
  expect(container.textContent).not.toContain("Connected");
  expect(container.querySelectorAll("input")).toHaveLength(0);
  await act(async () => root.unmount());
});
