import { useCallback, useMemo, useState } from "react";
import { api } from "../api/client";
import type { DeliveryEndpoint } from "../api/types";

export function useDeliveryTarget(activeSession: string) {
  const [deliveryEndpoints, setDeliveryEndpoints] = useState<DeliveryEndpoint[]>([]);
  const [targetsBySession, setTargetsBySession] = useState<Record<string, string>>({});

  const refreshDeliverySurface = useCallback(async () => {
    try {
      const result = await api.deliveryEndpoints();
      // An authoritative response replaces the list, including an empty one.
      setDeliveryEndpoints(result.endpoints ?? []);
    } catch {
      // Transient refresh failure: keep the last-good endpoint list instead
      // of wiping the picker (and the owner's selection) on every blip.
    }
  }, []);

  const activeTargetEndpointID = activeSession ? targetsBySession[activeSession] ?? "" : "";
  const activeDeliveryEndpoint = useMemo(
    () => deliveryEndpoints.find((endpoint) => endpoint.id === activeTargetEndpointID),
    [deliveryEndpoints, activeTargetEndpointID]
  );

  const selectDeliveryTarget = useCallback((endpointId: string) => {
    if (!activeSession) return;
    setTargetsBySession((current) => ({ ...current, [activeSession]: endpointId }));
  }, [activeSession]);

  const clearSessionTarget = useCallback((sessionId: string) => {
    setTargetsBySession((current) => {
      const next = { ...current };
      delete next[sessionId];
      return next;
    });
  }, []);

  return {
    deliveryEndpoints,
    activeTargetEndpointID,
    activeDeliveryEndpoint,
    // Intent is defined by a resolvable endpoint, so it can never disagree
    // with what the composer will actually target.
    externalDeliveryIntent: Boolean(activeDeliveryEndpoint),
    refreshDeliverySurface,
    selectDeliveryTarget,
    clearSessionTarget
  };
}
