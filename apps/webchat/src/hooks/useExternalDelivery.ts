// External delivery composer state: per-session send-to-software drafts,
// idempotency keys, last delivery receipts, plus the review/confirm/retry flow.
// Extracted from App.tsx so the root component stays below the size baseline.
import { useCallback, useMemo, useReducer, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { APIError, api } from "../api/client";
import {
  deliveryDraftParts,
  deliveryPartFromAttachment,
  deliveryPartIDFromAttachment,
  emptyExternalDeliveryDraft,
  endpointsForSoftware,
  selectDeliverySoftware,
  validateDeliveryDraft
} from "../lib/deliveryDraft";
import type { ExternalDeliveryDraft } from "../lib/deliveryDraft";
import type { DeliveryEndpoint, DeliveryPart, MessageAttachment, MessageDelivery } from "../api/types";

// The three per-session delivery maps move together on every transition
// (draft edits invalidate idempotency keys, deliveries clear drafts), so they
// live in a single reducer keyed by session id instead of three useState maps.
type DeliverySessionState = {
  draftsBySession: Record<string, ExternalDeliveryDraft>;
  idempotencyBySession: Record<string, string>;
  lastDeliveriesBySession: Record<string, MessageDelivery>;
};

type DeliverySessionAction =
  | { type: "draft.update"; sessionId: string; update: (draft: ExternalDeliveryDraft) => ExternalDeliveryDraft }
  | { type: "draft.reset"; sessionId: string }
  | { type: "review.prepare"; sessionId: string; idempotencyKey: string }
  | { type: "delivery.recorded"; sessionId: string; delivery: MessageDelivery; clearDraft: boolean }
  | { type: "session.clear"; sessionId: string };

const initialDeliverySessionState: DeliverySessionState = {
  draftsBySession: {},
  idempotencyBySession: {},
  lastDeliveriesBySession: {}
};

function deliverySessionReducer(state: DeliverySessionState, action: DeliverySessionAction): DeliverySessionState {
  switch (action.type) {
    case "draft.update":
      return {
        ...state,
        draftsBySession: {
          ...state.draftsBySession,
          [action.sessionId]: action.update(state.draftsBySession[action.sessionId] ?? emptyExternalDeliveryDraft())
        },
        idempotencyBySession: omitSession(state.idempotencyBySession, action.sessionId)
      };
    case "draft.reset":
      return {
        ...state,
        draftsBySession: { ...state.draftsBySession, [action.sessionId]: emptyExternalDeliveryDraft() }
      };
    case "review.prepare":
      return {
        ...state,
        idempotencyBySession: {
          ...state.idempotencyBySession,
          [action.sessionId]: state.idempotencyBySession[action.sessionId] || action.idempotencyKey
        }
      };
    case "delivery.recorded": {
      const recorded = {
        ...state,
        lastDeliveriesBySession: { ...state.lastDeliveriesBySession, [action.sessionId]: action.delivery }
      };
      if (!action.clearDraft) return recorded;
      return {
        ...recorded,
        draftsBySession: { ...recorded.draftsBySession, [action.sessionId]: emptyExternalDeliveryDraft() },
        idempotencyBySession: omitSession(recorded.idempotencyBySession, action.sessionId)
      };
    }
    case "session.clear":
      return {
        draftsBySession: omitSession(state.draftsBySession, action.sessionId),
        idempotencyBySession: omitSession(state.idempotencyBySession, action.sessionId),
        lastDeliveriesBySession: omitSession(state.lastDeliveriesBySession, action.sessionId)
      };
  }
}

type Options = {
  activeSession: string;
  activeInput: string;
  activeAttachments: MessageAttachment[];
  setDraftsBySession: Dispatch<SetStateAction<Record<string, string>>>;
  setAttachmentsBySession: Dispatch<SetStateAction<Record<string, MessageAttachment[]>>>;
  setError: (message: string) => void;
  deliveryErrorFallback: string;
};

export function useExternalDelivery({
  activeSession,
  activeInput,
  activeAttachments,
  setDraftsBySession,
  setAttachmentsBySession,
  setError,
  deliveryErrorFallback
}: Options) {
  const [sessionState, dispatch] = useReducer(deliverySessionReducer, initialDeliverySessionState);
  const [deliveryEndpoints, setDeliveryEndpoints] = useState<DeliveryEndpoint[]>([]);
  const [deliveryReviewOpen, setDeliveryReviewOpen] = useState(false);
  const [deliveryBusy, setDeliveryBusy] = useState(false);

  const refreshDeliverySurface = useCallback(async () => {
    try {
      const result = await api.deliveryEndpoints();
      setDeliveryEndpoints(result.endpoints ?? []);
    } catch {
      setDeliveryEndpoints([]);
    }
  }, []);

  const storedExternalDraft = activeSession
    ? sessionState.draftsBySession[activeSession] ?? emptyExternalDeliveryDraft()
    : emptyExternalDeliveryDraft();
  const activeExternalDraft = { ...storedExternalDraft, software: storedExternalDraft.software ?? "", text: activeInput };
  const deliverySoftwareOptions = useMemo(() => {
    const options = new Map<string, string>();
    for (const endpoint of deliveryEndpoints) {
      if (endpoint.channel && !options.has(endpoint.channel)) {
        options.set(endpoint.channel, endpoint.software_display_name || endpoint.channel);
      }
    }
    return [...options].map(([value, label]) => ({ value, label })).sort((a, b) => a.label.localeCompare(b.label));
  }, [deliveryEndpoints]);
  const activeDeliveryCandidates = useMemo(
    () => endpointsForSoftware(deliveryEndpoints, activeExternalDraft.software),
    [deliveryEndpoints, activeExternalDraft.software]
  );
  const activeDeliveryEndpoint = activeDeliveryCandidates.find((endpoint) => endpoint.id === activeExternalDraft.endpointId);
  const externalDeliveryIntent = activeExternalDraft.software !== "";
  const activeDeliveryValidation = validateDeliveryDraft(activeExternalDraft, activeDeliveryEndpoint);
  const activeLastDelivery = activeSession ? sessionState.lastDeliveriesBySession[activeSession] ?? null : null;

  function updateExternalDraft(update: (draft: ExternalDeliveryDraft) => ExternalDeliveryDraft) {
    if (!activeSession) return;
    dispatch({ type: "draft.update", sessionId: activeSession, update });
  }

  function chooseDeliverySoftware(software: string) {
    updateExternalDraft((draft) => {
      const selected = selectDeliverySoftware(draft, software);
      return {
        ...selected,
        parts: software && draft.parts.length === 0
          ? activeAttachments.map((attachment) => deliveryPartFromAttachment(deliveryPartIDFromAttachment(attachment), attachment))
          : draft.parts
      };
    });
    setDeliveryReviewOpen(false);
  }

  function selectDeliveryTarget(endpointId: string) {
    updateExternalDraft((draft) => ({
      ...draft,
      endpointId,
      parts: endpointId && draft.parts.length === 0
        ? activeAttachments.map((attachment) => deliveryPartFromAttachment(deliveryPartIDFromAttachment(attachment), attachment))
        : draft.parts
    }));
    setDeliveryReviewOpen(false);
  }

  function updateExternalPart(id: string, update: Partial<DeliveryPart>) {
    updateExternalDraft((draft) => ({
      ...draft,
      parts: draft.parts.map((part) => (part.id === id ? { ...part, ...update } : part))
    }));
  }

  function removeExternalPart(id: string) {
    updateExternalDraft((draft) => ({ ...draft, parts: draft.parts.filter((part) => part.id !== id) }));
    if (!activeSession) return;
    setAttachmentsBySession((current) => ({
      ...current,
      [activeSession]: (current[activeSession] ?? []).filter((attachment) => deliveryPartIDFromAttachment(attachment) !== id)
    }));
  }

  function openDeliveryReview() {
    if (!activeSession || !activeDeliveryValidation.valid || !activeDeliveryEndpoint) return;
    dispatch({ type: "review.prepare", sessionId: activeSession, idempotencyKey: `web-${crypto.randomUUID()}` });
    setDeliveryReviewOpen(true);
  }

  async function confirmExternalDelivery() {
    if (!activeSession || !activeDeliveryEndpoint || !activeDeliveryValidation.valid || deliveryBusy) return;
    const idempotencyKey = sessionState.idempotencyBySession[activeSession];
    if (!idempotencyKey) return;
    try {
      setDeliveryBusy(true);
      setError("");
      const delivery = await api.createDelivery(
        activeDeliveryEndpoint.id,
        idempotencyKey,
        deliveryDraftParts(activeExternalDraft)
      );
      dispatch({ type: "delivery.recorded", sessionId: activeSession, delivery, clearDraft: true });
      setDraftsBySession((current) => ({ ...current, [activeSession]: "" }));
      setAttachmentsBySession((current) => ({ ...current, [activeSession]: [] }));
      setDeliveryReviewOpen(false);
      await refreshDeliverySurface();
    } catch (err) {
      const failed = err instanceof APIError && err.details && typeof err.details === "object"
        ? (err.details as { delivery?: MessageDelivery }).delivery
        : undefined;
      if (failed) {
        dispatch({ type: "delivery.recorded", sessionId: activeSession, delivery: failed, clearDraft: false });
        setDeliveryReviewOpen(false);
        await refreshDeliverySurface();
      }
      setError(err instanceof Error ? err.message : deliveryErrorFallback);
    } finally {
      setDeliveryBusy(false);
    }
  }

  async function retryExternalDelivery() {
    if (!activeSession || !activeLastDelivery || deliveryBusy) return;
    try {
      setDeliveryBusy(true);
      setError("");
      const delivery = await api.retryDelivery(activeLastDelivery.id);
      dispatch({ type: "delivery.recorded", sessionId: activeSession, delivery, clearDraft: delivery.status === "sent" });
      if (delivery.status === "sent") {
        setDraftsBySession((current) => ({ ...current, [activeSession]: "" }));
        setAttachmentsBySession((current) => ({ ...current, [activeSession]: [] }));
      }
      await refreshDeliverySurface();
    } catch (err) {
      const failed = err instanceof APIError && err.details && typeof err.details === "object"
        ? (err.details as { delivery?: MessageDelivery }).delivery
        : undefined;
      if (failed) dispatch({ type: "delivery.recorded", sessionId: activeSession, delivery: failed, clearDraft: false });
      setError(err instanceof Error ? err.message : deliveryErrorFallback);
      await refreshDeliverySurface();
    } finally {
      setDeliveryBusy(false);
    }
  }

  const resetSessionDraft = useCallback((sessionId: string) => {
    dispatch({ type: "draft.reset", sessionId });
  }, []);

  const clearSessionState = useCallback((sessionId: string) => {
    dispatch({ type: "session.clear", sessionId });
    setDeliveryReviewOpen(false);
  }, []);

  return {
    deliveryBusy,
    deliveryReviewOpen,
    setDeliveryReviewOpen,
    deliverySoftwareOptions,
    activeDeliveryCandidates,
    activeDeliveryEndpoint,
    activeExternalDraft,
    externalDeliveryIntent,
    activeDeliveryValidation,
    activeLastDelivery,
    refreshDeliverySurface,
    updateExternalDraft,
    chooseDeliverySoftware,
    selectDeliveryTarget,
    updateExternalPart,
    removeExternalPart,
    openDeliveryReview,
    confirmExternalDelivery,
    retryExternalDelivery,
    resetSessionDraft,
    clearSessionState
  };
}

function omitSession<T>(current: Record<string, T>, sessionId: string) {
  const next = { ...current };
  delete next[sessionId];
  return next;
}
