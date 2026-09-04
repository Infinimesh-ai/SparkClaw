import { useEffect, useState } from "react";
import { CheckCircle2, CircleAlert, LoaderCircle, LogIn, Mail, RefreshCw, Star } from "lucide-react";
import { api } from "../../api/client";
import type { EmailProviderStatus } from "../../api/types";
import type { Copy } from "../../i18n";

type Feedback = {
  tone: "success" | "error";
  title: string;
  message: string;
};

export function BrowserEmailSettings({ text }: { text: Copy }) {
  const [providers, setProviders] = useState<EmailProviderStatus[] | null>(null);
  const [busy, setBusy] = useState("");
  const [feedback, setFeedback] = useState<Feedback | null>(null);

  useEffect(() => {
    let active = true;
    api.emailProviders()
      .then((response) => { if (active) setProviders(response.providers); })
      .catch((reason) => {
        if (active) setFeedback({ tone: "error", title: text.settings.browserEmailLoadFailed, message: errorMessage(reason) });
      });
    return () => { active = false; };
  }, [text.settings.browserEmailLoadFailed]);

  function replaceProvider(next: EmailProviderStatus) {
    setProviders((current) => current?.map((item) => item.provider === next.provider ? next : item) ?? [next]);
  }

  async function refreshProviders() {
    const response = await api.emailProviders();
    setProviders(response.providers);
  }

  async function run(
    key: string,
    action: () => Promise<EmailProviderStatus>,
    successTitle: string,
    successMessage: string
  ) {
    if (busy) return;
    setBusy(key);
    setFeedback(null);
    try {
      replaceProvider(await action());
      setFeedback({ tone: "success", title: successTitle, message: successMessage });
    } catch (reason) {
      setFeedback({ tone: "error", title: text.settings.browserEmailActionFailed, message: errorMessage(reason) });
      try {
        await refreshProviders();
      } catch {
        // Keep the action error when refreshing the persisted state also fails.
      }
    } finally {
      setBusy("");
    }
  }

  async function toggleProvider(provider: EmailProviderStatus) {
    await run(
      `toggle:${provider.provider}`,
      () => api.updateEmailProvider(provider.provider, provider.version, { enabled: !provider.enabled }),
      text.settings.browserEmailUpdated,
      text.settings.browserEmailUpdatedDetail
    );
  }

  async function selectDefault(provider: EmailProviderStatus) {
    if (!providers || !provider.enabled || provider.default || busy) return;
    setBusy(`default:${provider.provider}`);
    setFeedback(null);
    try {
      await api.updateEmailProvider(provider.provider, provider.version, { default: true });
      await refreshProviders();
      setFeedback({
        tone: "success",
        title: text.settings.browserEmailUpdated,
        message: text.settings.browserEmailDefaultUpdated
      });
    } catch (reason) {
      setFeedback({ tone: "error", title: text.settings.browserEmailActionFailed, message: errorMessage(reason) });
      try {
        await refreshProviders();
      } catch {
        // Keep the action error when refreshing the persisted state also fails.
      }
    } finally {
      setBusy("");
    }
  }

  if (!providers) {
    return (
      <div className="emailProviderLoading">
        {feedback ? <EmailFeedback feedback={feedback} /> : <><LoaderCircle className="spin" size={16} /><span>{text.settings.browserEmailLoading}</span></>}
      </div>
    );
  }

  return (
    <div className="integrationDetail">
      <div className="integrationStatusBar">
        <div>
          <strong>{text.settings.browserEmail}</strong>
          <span className="muted">{text.settings.browserEmailProviders}</span>
        </div>
      </div>

      <div className="emailProviderList">
        {providers.map((provider) => {
          const state = emailStateLabel(provider.state, text);
          const actionBusy = busy.endsWith(`:${provider.provider}`);
          const toggleTitle = provider.enabled ? text.settings.browserEmailDisable : text.settings.browserEmailEnable;
          return (
            <div className={`emailProviderRow ${provider.default ? "selected" : ""}`} key={provider.provider}>
              <span className="credentialIcon"><Mail size={16} /></span>
              <div className="credentialIdentity">
                <strong>{provider.display_name}</strong>
                <small>{state}{provider.account_hint ? ` · ${provider.account_hint}` : ""}</small>
              </div>
              <div className="emailProviderActions">
                <button
                  className={`miniIconButton ${provider.default ? "selected" : ""}`}
                  type="button"
                  onClick={() => void selectDefault(provider)}
                  disabled={Boolean(busy) || !provider.enabled || provider.default}
                  title={provider.default ? text.settings.browserEmailDefault : text.settings.browserEmailSetDefault}
                  aria-label={provider.default ? text.settings.browserEmailDefault : text.settings.browserEmailSetDefault}
                >
                  <Star size={14} fill={provider.default ? "currentColor" : "none"} />
                </button>
                <button
                  className="miniIconButton"
                  type="button"
                  onClick={() => void run(
                    `login:${provider.provider}`,
                    () => api.openEmailLoginBrowser(provider.provider),
                    text.settings.browserEmailLoginOpened,
                    text.settings.browserEmailLoginOpenedDetail
                  )}
                  disabled={Boolean(busy)}
                  title={text.settings.browserEmailOpenLogin}
                  aria-label={text.settings.browserEmailOpenLogin}
                >
                  <LogIn size={14} />
                </button>
                <button
                  className="miniIconButton"
                  type="button"
                  onClick={() => void run(
                    `check:${provider.provider}`,
                    () => api.checkEmailProvider(provider.provider),
                    text.settings.browserEmailCheckSucceeded,
                    text.settings.browserEmailCheckSucceededDetail
                  )}
                  disabled={Boolean(busy) || !provider.enabled}
                  title={text.settings.checkConnection}
                  aria-label={text.settings.checkConnection}
                >
                  <RefreshCw size={14} className={actionBusy ? "spin" : ""} />
                </button>
                <label className="connectorToggle" title={toggleTitle}>
                  <input
                    type="checkbox"
                    checked={provider.enabled}
                    onChange={() => void toggleProvider(provider)}
                    disabled={Boolean(busy)}
                    aria-label={toggleTitle}
                  />
                  <span aria-hidden="true" />
                </label>
              </div>
            </div>
          );
        })}
      </div>

      {feedback && <EmailFeedback feedback={feedback} />}
    </div>
  );
}

function EmailFeedback({ feedback }: { feedback: Feedback }) {
  return (
    <div className={`credentialValidationFeedback ${feedback.tone}`}>
      <span className="credentialValidationIcon">
        {feedback.tone === "success" ? <CheckCircle2 size={16} /> : <CircleAlert size={16} />}
      </span>
      <span className="credentialValidationCopy"><strong>{feedback.title}</strong><small>{feedback.message}</small></span>
    </div>
  );
}

function emailStateLabel(state: string, text: Copy) {
  switch (state) {
    case "ready": return text.settings.integrationReady;
    case "login_required": return text.settings.browserEmailLoginRequired;
    case "needs_attention": return text.settings.integrationNeedsAttention;
    case "temporarily_unavailable": return text.settings.integrationTemporarilyUnavailable;
    default: return text.settings.integrationNotConfigured;
  }
}

function errorMessage(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason);
}
