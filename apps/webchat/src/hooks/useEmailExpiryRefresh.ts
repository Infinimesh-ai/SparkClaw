import { useEffect, useRef } from "react";
import type { EmailMessage } from "../api/email";

// Expiry cursors bind a server observation time. Start a fresh scope when a
// loaded member expires, so historical pages cannot retain now-expired codes.
export function useEmailExpiryRefresh(enabled: boolean, items: EmailMessage[], serverNow: string | undefined, refreshScope: () => void) {
  const clock = useRef({ source: "", base: NaN, start: 0 });
  if (serverNow && serverNow !== clock.current.source) clock.current = { source: serverNow, base: Date.parse(serverNow), start: performance.now() };
  const deadlines = items.map((m) => m.verification?.expires_at ?? "").filter(Boolean).sort().join("\n");
  useEffect(() => {
    if (!enabled || !deadlines || !Number.isFinite(clock.current.base)) return;
    const deadline = Math.min(...deadlines.split("\n").map(Date.parse).filter(Number.isFinite));
    if (!Number.isFinite(deadline)) return;
    let fired = false;
    let timer: ReturnType<typeof setTimeout>;
    const remaining = () => deadline - clock.current.base - (performance.now() - clock.current.start);
    const reset = () => {
      clearTimeout(timer);
      if (fired) return;
      if (remaining() <= 0) { fired = true; refreshScope(); }
      else timer = setTimeout(reset, Math.max(0, Math.min(2147483647, remaining() + 10)));
    };
    reset();
    window.addEventListener("focus", reset); document.addEventListener("visibilitychange", reset);
    return () => { clearTimeout(timer); window.removeEventListener("focus", reset); document.removeEventListener("visibilitychange", reset); };
  }, [enabled, deadlines, serverNow, refreshScope]);
}
