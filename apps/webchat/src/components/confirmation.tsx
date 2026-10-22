import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import type { Copy } from "../i18n";
import { useDismissibleLayer } from "../hooks/useDismissibleLayer";

type ConfirmationLabels = { cancel: string; continue: string };
type ConfirmationOptions = { danger?: boolean };
type ConfirmationRequest = {
  id: number;
  owner: symbol;
  message: string;
  labels: ConfirmationLabels;
  danger: boolean;
  restoreFocusRef: { current: HTMLElement | null };
  resolve: (accepted: boolean) => void;
};
type ConfirmationContextValue = {
  request: (owner: symbol, message: string, labels: ConfirmationLabels, options?: ConfirmationOptions) => Promise<boolean>;
  cancel: (owner: symbol) => void;
};

const ConfirmationContext = createContext<ConfirmationContextValue | null>(null);

export function ConfirmationProvider({ children }: { children: ReactNode }) {
  const [active, setActive] = useState<ConfirmationRequest | null>(null);
  const activeRef = useRef<ConfirmationRequest | null>(null);
  const queue = useRef<ConfirmationRequest[]>([]);
  const mounted = useRef(false);
  const sequence = useRef(0);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      const pending = [...(activeRef.current ? [activeRef.current] : []), ...queue.current];
      activeRef.current = null;
      queue.current = [];
      for (const item of pending) item.resolve(false);
    };
  }, []);

  const request = useCallback((owner: symbol, message: string, labels: ConfirmationLabels, options: ConfirmationOptions = {}) => new Promise<boolean>((resolve) => {
    if (!mounted.current) { resolve(false); return; }
    const restoreFocusRef = activeRef.current?.restoreFocusRef ?? {
      current: document.activeElement instanceof HTMLElement ? document.activeElement : null
    };
    const item = { id: ++sequence.current, owner, message, labels, danger: Boolean(options.danger), restoreFocusRef, resolve };
    if (activeRef.current) queue.current.push(item);
    else { activeRef.current = item; setActive(item); }
  }), []);

  const settle = useCallback((accepted: boolean) => {
    const current = activeRef.current;
    if (!current) return;
    const next = queue.current.shift() ?? null;
    activeRef.current = next;
    setActive(next);
    current.resolve(accepted);
  }, []);

  const cancel = useCallback((owner: symbol) => {
    const canceled = queue.current.filter(item => item.owner === owner);
    queue.current = queue.current.filter(item => item.owner !== owner);
    for (const item of canceled) item.resolve(false);
    if (activeRef.current?.owner === owner) settle(false);
  }, [settle]);

  const context = useMemo(() => ({ request, cancel }), [request, cancel]);
  return <ConfirmationContext.Provider value={context}>
    {children}
    {active && createPortal(<ConfirmationDialog key={active.id} request={active} onResolve={settle} />, document.body)}
  </ConfirmationContext.Provider>;
}

/** Components mounted outside the application shell retain native confirmation. */
export function useConfirm(text: Pick<Copy, "common">) {
  const context = useContext(ConfirmationContext);
  const owner = useRef(Symbol("confirmation-owner"));
  useEffect(() => () => context?.cancel(owner.current), [context]);
  return useCallback((message: string, options?: ConfirmationOptions) => context
    ? context.request(owner.current, message, { cancel: text.common.cancel, continue: text.common.continue }, options)
    : Promise.resolve(window.confirm(message)), [context, text.common.cancel, text.common.continue]);
}

function ConfirmationDialog({ request, onResolve }: { request: ConfirmationRequest; onResolve: (accepted: boolean) => void }) {
  const titleId = useId();
  const dialogRef = useDismissibleLayer<HTMLDivElement>({
    open: true,
    modal: true,
    initialFocus: ".confirmationCancel",
    restoreFocusRef: request.restoreFocusRef,
    onDismiss: () => onResolve(false)
  });
  return <div
    ref={dialogRef}
    className="confirmationOverlay"
    tabIndex={-1}
    role="dialog"
    aria-modal="true"
    aria-labelledby={titleId}
    onMouseDown={event => { if (event.target === event.currentTarget) onResolve(false); }}
  >
    <div className="confirmationDialog">
      <h2 id={titleId}>{request.message}</h2>
      <div className="confirmationActions">
        <button className="secondaryButton confirmationCancel" type="button" onClick={() => onResolve(false)}>{request.labels.cancel}</button>
        <button className={`${request.danger ? "dangerButton" : "primaryButton"} confirmationAccept${request.danger ? " isDanger" : ""}`} type="button" onClick={() => onResolve(true)}>{request.labels.continue}</button>
      </div>
    </div>
  </div>;
}
