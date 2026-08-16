// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { DeliveryEndpoint } from "../api/types";
import { useDeliveryTarget } from "./useDeliveryTarget";

const endpoint = { id: "endpoint-1", channel: "testchat" } as DeliveryEndpoint;

let latest: ReturnType<typeof useDeliveryTarget>;
function Probe({ session }: { session: string }) {
  latest = useDeliveryTarget(session);
  return null;
}

describe("useDeliveryTarget", () => {
  afterEach(() => vi.restoreAllMocks());

  it("keeps the last-good endpoint list on transient failure and clears on an authoritative empty response", async () => {
    const endpoints = vi.spyOn(api, "deliveryEndpoints").mockResolvedValue({ endpoints: [endpoint] });
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(<Probe session="session-1" />));

    await act(async () => { await latest.refreshDeliverySurface(); });
    act(() => latest.selectDeliveryTarget(endpoint.id));
    expect(latest.deliveryEndpoints).toHaveLength(1);
    expect(latest.activeDeliveryEndpoint?.id).toBe(endpoint.id);
    expect(latest.externalDeliveryIntent).toBe(true);

    endpoints.mockRejectedValueOnce(new Error("gateway unreachable"));
    await act(async () => { await latest.refreshDeliverySurface(); });
    expect(latest.deliveryEndpoints).toHaveLength(1);
    expect(latest.externalDeliveryIntent).toBe(true);

    endpoints.mockResolvedValueOnce({ endpoints: [] });
    await act(async () => { await latest.refreshDeliverySurface(); });
    expect(latest.deliveryEndpoints).toHaveLength(0);
    expect(latest.activeDeliveryEndpoint).toBeUndefined();
    expect(latest.externalDeliveryIntent).toBe(false);

    await act(async () => root.unmount());
  });

  it("reports no external intent while the selected target is unresolvable", async () => {
    vi.spyOn(api, "deliveryEndpoints").mockResolvedValue({ endpoints: [] });
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(<Probe session="session-1" />));

    act(() => latest.selectDeliveryTarget("endpoint-gone"));
    expect(latest.activeTargetEndpointID).toBe("endpoint-gone");
    expect(latest.activeDeliveryEndpoint).toBeUndefined();
    expect(latest.externalDeliveryIntent).toBe(false);

    await act(async () => root.unmount());
  });
});
