import { useEffect, useState } from "react";
import { CheckCircle2, CircleAlert, ExternalLink, KeyRound, LoaderCircle, RefreshCw, Trash2 } from "lucide-react";
import { api } from "../../api/client";
import type { BrowserExtensionStatus } from "../../api/types";
import type { Copy, Language } from "../../i18n";
import { formatDateTime } from "../../lib/format";
import { integrationStateLabel } from "./settingsIntegrationState";

const PLAYWRIGHT_EXTENSION_LISTING = "https://chromewebstore.google.com/detail/playwright-mcp-bridge/mmlmfjhmonkocbjadbfplnigmagldckm";

type Feedback = {
  tone: "success" | "error";
  title: string;
  message: string;
};

export function BrowserControlSettings({ text, language }: { text: Copy; language: Language }) {
  const [status, setStatus] = useState<BrowserExtensionStatus | null>(null);
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState("");
  const [feedback, setFeedback] = useState<Feedback | null>(null);

  useEffect(() => {
    let active = true;
    setBusy("load");
    api.browserExtension()
      .then((next) => { if (active) setStatus(next); })
      .catch((reason) => {
        if (active) setFeedback({ tone: "error", title: text.settings.browserControlLoadFailed, message: errorMessage(reason) });
      })
      .finally(() => { if (active) setBusy(""); });
    return () => { active = false; };
  }, [text.settings.browserControlLoadFailed]);

  async function refreshStatus() {
    setStatus(await api.browserExtension());
  }

  async function run(
    key: string,
    action: () => Promise<BrowserExtensionStatus>,
    successTitle: string,
    successMessage: string
  ) {
    if (busy) return;
    setBusy(key);
    setFeedback(null);
    try {
      setStatus(await action());
      setFeedback({ tone: "success", title: successTitle, message: successMessage });
    } catch (reason) {
      setFeedback({ tone: "error", title: text.settings.browserControlActionFailed, message: errorMessage(reason) });
      try {
        await refreshStatus();
      } catch {
        // Keep the action error when the redacted status refresh also fails.
      }
    } finally {
      setToken("");
      setBusy("");
    }
  }

  async function saveToken() {
    if (!token) {
      setFeedback({
        tone: "error",
        title: text.settings.validationNotStarted,
        message: text.settings.browserControlTokenRequired
      });
      return;
    }
    await run(
      "save",
      () => api.saveBrowserExtensionToken(token),
      text.settings.validationSucceeded,
      text.settings.browserControlSaved
    );
  }

  function removeToken() {
    if (!window.confirm(text.settings.browserControlRemoveConfirm)) return;
    void run(
      "remove",
      () => api.removeBrowserExtensionToken(),
      text.settings.browserControlRemoved,
      text.settings.browserControlRemovedDetail
    );
  }

  if (!status) {
    return (
      <div className="emailProviderLoading">
        {feedback ? <BrowserControlFeedback feedback={feedback} /> : <><LoaderCircle className="spin" size={16} /><span>{text.settings.browserControlLoading}</span></>}
      </div>
    );
  }

  const displayedState = busy === "save" || busy === "check" ? "checking" : status.state;
  const clientVersion = joinVersion(status.versions.client, status.versions.client_version);
  const browserVersion = joinVersion(status.versions.browser_channel, status.versions.playwright_version);

  return (
    <div className="integrationDetail browserControlDetail" aria-busy={displayedState === "checking"}>
      <div className="integrationStatusBar">
        <div>
          <strong>{text.settings.playwrightExtensionPreview}</strong>
          <span className="muted">{text.settings.browserControlSharedProfile}</span>
        </div>
        <span className={`integrationState ${displayedState}`}>{integrationStateLabel(displayedState, text)}</span>
      </div>

      <div className="browserControlNotice" role="note">
        <CircleAlert size={16} />
        <span>{text.settings.browserControlDisposableProfileWarning}</span>
      </div>

      <dl className="statusGrid compact browserControlStatus">
        <dt>{text.settings.browserControlProfile}</dt><dd>{status.profile_id}</dd>
        <dt>{text.settings.browserControlCredentialGeneration}</dt><dd>{status.credential_generation}</dd>
        <dt>{text.settings.browserControlClientVersion}</dt><dd>{clientVersion || text.common.none}</dd>
        <dt>{text.settings.browserControlBrowserVersion}</dt><dd>{browserVersion || text.common.none}</dd>
        <dt>{text.settings.browserControlLastValidated}</dt><dd>{status.last_validated_at ? formatDateTime(status.last_validated_at, language) : text.common.none}</dd>
        <dt>{text.settings.browserControlErrorCode}</dt><dd>{status.error_code || text.common.none}</dd>
      </dl>

      <form className="credentialForm browserControlForm" onSubmit={(event) => { event.preventDefault(); void saveToken(); }}>
        <label>
          <span>{text.settings.browserControlExtensionToken}</span>
          <input
            type="password"
            value={token}
            onChange={(event) => setToken(event.target.value)}
            autoComplete="new-password"
            spellCheck={false}
            disabled={Boolean(busy)}
          />
        </label>
        <button className="approve credentialSubmit" type="submit" disabled={Boolean(busy) || !token}>
          {busy === "save" ? <LoaderCircle className="spin" size={15} /> : <KeyRound size={15} />}
          <span>{busy === "save" ? text.settings.validatingAndSaving : status.configured ? text.settings.browserControlReplace : text.settings.validateAndSave}</span>
        </button>
      </form>

      <div className="browserControlActions">
        <button
          className="miniIconButton"
          type="button"
          onClick={() => void run(
            "check",
            () => api.checkBrowserExtension(),
            text.settings.browserControlCheckSucceeded,
            text.settings.browserControlCheckSucceededDetail
          )}
          disabled={Boolean(busy) || !status.configured}
          title={text.settings.checkConnection}
          aria-label={text.settings.checkConnection}
        >
          <RefreshCw className={busy === "check" ? "spin" : ""} size={15} />
        </button>
        <button
          className="miniIconButton dangerIcon"
          type="button"
          onClick={removeToken}
          disabled={Boolean(busy) || !status.configured}
          title={text.settings.browserControlRemove}
          aria-label={text.settings.browserControlRemove}
        >
          <Trash2 size={15} />
        </button>
        <a
          className="browserControlExtensionLink"
          href={PLAYWRIGHT_EXTENSION_LISTING}
          target="_blank"
          rel="noreferrer"
          title={text.settings.browserControlOpenExtension}
        >
          <ExternalLink size={14} />
          <span>{text.settings.browserControlOpenExtension}</span>
        </a>
      </div>

      {feedback && <BrowserControlFeedback feedback={feedback} />}
    </div>
  );
}

function BrowserControlFeedback({ feedback }: { feedback: Feedback }) {
  return (
    <div className={`credentialValidationFeedback ${feedback.tone}`} role="status">
      <span className="credentialValidationIcon">
        {feedback.tone === "success" ? <CheckCircle2 size={16} /> : <CircleAlert size={16} />}
      </span>
      <span className="credentialValidationCopy"><strong>{feedback.title}</strong><small>{feedback.message}</small></span>
    </div>
  );
}

function joinVersion(name?: string, version?: string) {
  return [name, version].filter(Boolean).join(" ");
}

function errorMessage(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason);
}
