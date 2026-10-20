import { useEffect, useId, useRef, useState } from "react";
import { CheckCircle2, X } from "lucide-react";
import { QRCodeSVG } from "qrcode.react";
import { api, APIError } from "../../api/client";
import type { NotificationBinding } from "../../api/types";
import type { Copy } from "../../i18n";
import { useDismissibleLayer } from "../../hooks/useDismissibleLayer";
import { bindingStatusLabel } from "../../lib/format";

export function WeixinBindingDialog({ binding, text, busy, error, onClose, onRestart, onRefresh }: {
  binding: NotificationBinding;
  text: Copy;
  busy: boolean;
  error: string;
  onClose: () => void;
  onRestart: () => void;
  onRefresh: (signal: AbortSignal) => Promise<NotificationBinding>;
}) {
  const dialogRef = useDismissibleLayer<HTMLDialogElement>({ open: true, onDismiss: onClose });
  const titleID = useId();
  const [qr, setQR] = useState<{ url: string; expires_at: string }>();
  const [qrError, setQRError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [now, setNow] = useState(Date.now);
  const expiresAt = Date.parse(qr?.expires_at ?? binding.expires_at ?? "");
  const expired = binding.status === "expired" || Number.isFinite(expiresAt) && expiresAt <= now;
  const failed = binding.status === "failed";
  const waitingScan = binding.status === "waiting_scan" && !expired;
  const onRefreshRef = useRef(onRefresh); onRefreshRef.current = onRefresh;

  useEffect(() => {
    const dialog = dialogRef.current;
    dialog?.showModal();
    return () => dialog?.close();
  }, [dialogRef]);

  useEffect(() => {
    if (!Number.isFinite(expiresAt)) return;
    const timer = window.setTimeout(() => setNow(Date.now()), Math.max(0, expiresAt - Date.now()));
    return () => window.clearTimeout(timer);
  }, [expiresAt]);

  useEffect(() => {
    if (!waitingScan) return;
    const controller = new AbortController();
    setQR(undefined);
    setQRError("");
    void api.notificationBindingQR(binding.id, controller.signal).then(value => {
      if (!controller.signal.aborted) setQR(value);
    }).catch((reason: unknown) => {
      if (controller.signal.aborted) return;
      // 409: the binding left waiting_scan on the server (scanned or expired).
      // Refresh it so the dialog follows the new state instead of offering a
      // QR retry that can only conflict again.
      if (!(reason instanceof APIError && reason.status === 409)) { setQRError(text.settings.bindingQrUnavailable); return; }
      void onRefreshRef.current(controller.signal).then(current => {
        if (!controller.signal.aborted && current.status === "waiting_scan") setQRError(text.settings.bindingQrUnavailable);
      }).catch(() => {
        if (!controller.signal.aborted) setQRError(text.settings.bindingQrUnavailable);
      });
    });
    return () => controller.abort();
  }, [binding.id, waitingScan, attempt, text.settings.bindingQrUnavailable]);

  return <dialog ref={dialogRef} className="weixinBindingDialog" aria-labelledby={titleID}
    onCancel={event => { event.preventDefault(); onClose(); }}>
    <div className="taskInspectorHeader">
      <strong id={titleID}>{text.settings.bindWeixin}</strong>
      <button type="button" className="iconButton" onClick={onClose} aria-label={text.common.close}><X size={18} /></button>
    </div>
    <div className="weixinBindingDialogBody">
      {waitingScan && !qrError && (qr
        ? <QRCodeSVG value={qr.url} size={256} level="M" marginSize={4} role="img" aria-label={text.settings.weixinQrImage} />
        : <p>{text.settings.weixinQrLoading}</p>)}
      <p role="status">{expired ? text.settings.weixinQrExpired : failed ? bindingStatusLabel(binding.status, text)
        : binding.status === "waiting_confirm" ? <><CheckCircle2 size={20} /> {text.settings.scannedWeixin}</>
        : waitingScan ? text.settings.scanWeixin : text.settings.weixinQrLoading}</p>
      {(qrError || error || binding.last_error) && <p className="compactError" role="alert">{qrError || error || binding.last_error}</p>}
      {waitingScan && qrError && <button type="button" className="secondaryButton" onClick={() => setAttempt(value => value + 1)}>{text.common.refresh}</button>}
      {(expired || failed) && <button type="button" className="secondaryButton" disabled={busy} onClick={onRestart}>{text.settings.weixinQrRestart}</button>}
    </div>
  </dialog>;
}
