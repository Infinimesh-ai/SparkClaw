import { useEffect, useState } from "react";
import { api } from "../api/client";

// This hook lives on the entry button, not inside the conditionally mounted dialog.
export function useEmailLoginAlert() {
  const [required, setRequired] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    let busy = false;
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      if (!active || busy || document.visibilityState === "hidden") return;
      busy = true;
      try {
        const status = await api.emailSyncStatus(controller.signal);
        if (active) setRequired(status.mailboxes.some((mailbox) => mailbox.intake_enabled && mailbox.state === "login_required"));
      } catch {
        // An unavailable status endpoint is not proof that a login recovered.
      } finally { busy = false; }
    }
    async function poll() {
      await refresh();
      if (active) timer = setTimeout(() => void poll(), 5000);
    }
    const onVisible = () => { void refresh(); };
    document.addEventListener("visibilitychange", onVisible);
    void poll();
    return () => { active = false; clearTimeout(timer); controller.abort(); document.removeEventListener("visibilitychange", onVisible); };
  }, []);
  return required;
}
