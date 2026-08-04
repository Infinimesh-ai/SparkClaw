import { useCallback, useMemo, useState } from "react";
import { api } from "../api/client";
import type { DeliveryEndpoint } from "../api/types";

export function useDeliveryTarget(activeSession: string) {
  const [deliveryEndpoints, setDeliveryEndpoints] = useState<DeliveryEndpoint[]>([]);
  const [targetsBySession, setTargetsBySession] = useState<Record<string, string>>({});

  const refreshDeliverySurface = useCallback(async () => {
    try {
      const result = await api.deliveryEndpoints();
      setDeliveryEndpoints(result.endpoints ?? []);
    } catch {
      setDeliveryEndpoints([]);
    }
  }, []);

  const activeTargetEndpointID = activeSession ? targetsBySession[activeSession] ?? "" : "";
  const activeDeliveryEndpoint = useMemo(
    () => deliveryEndpoints.find((endpoint) => endpoint.id === activeTargetEndpointID),
    [deliveryEndpoints, activeTargetEndpointID]
  );

  function selectDeliveryTarget(endpointId: string) {
    if (!activeSession) return;
    setTargetsBySession((current) => ({ ...current, [activeSession]: endpointId }));
  }

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
    externalDeliveryIntent: activeTargetEndpointID !== "",
    refreshDeliverySurface,
    selectDeliveryTarget,
    clearSessionTarget
  };
}
