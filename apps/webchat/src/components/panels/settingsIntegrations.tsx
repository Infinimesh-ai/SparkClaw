import { useEffect, useState } from "react";
import { CheckCircle2, CircleAlert, KeyRound, LoaderCircle, Plus, Power, RefreshCw, ServerCog, Trash2 } from "lucide-react";
import { api } from "../../api/client";
import type { IntegrationID, IntegrationStatus } from "../../api/types";
import type { Copy, Language } from "../../i18n";
import { integrationStateLabel } from "./settingsIntegrationState";

type CredentialFeedback = {
  tone: "success" | "error";
  title: string;
  message: string;
};

type RunOptions = {
  clearInputs?: boolean;
  successTitle?: string;
  successMessage?: string;
  errorTitle?: string;
};

export function IntegrationCredentialSettings({
  id,
  status,
  text,
  language,
  onStatus
}: {
  id: IntegrationID;
  status: IntegrationStatus | null;
  text: Copy;
  language: Language;
  onStatus: (status: IntegrationStatus) => void;
}) {
  const [label, setLabel] = useState("");
  const [licenseId, setLicenseId] = useState("");
  const [licenseKey, setLicenseKey] = useState("");
  const [endpoint, setEndpoint] = useState("");
  const [bearerToken, setBearerToken] = useState("");
  const [busy, setBusy] = useState("");
  const [feedback, setFeedback] = useState<CredentialFeedback | null>(null);

  useEffect(() => {
    if (status) return;
    let active = true;
    setBusy("load");
    api.integration(id)
      .then((next) => { if (active) onStatus(next); })
      .catch((reason) => {
        if (active) {
          setFeedback({ tone: "error", title: text.errors.integration, message: errorMessage(reason, text.errors.integration) });
        }
      })
      .finally(() => { if (active) setBusy(""); });
    return () => { active = false; };
  }, [id, onStatus, status, text.errors.integration]);

  function clearCredentialInputs() {
    setLabel("");
    setLicenseId("");
    setLicenseKey("");
    setEndpoint("");
    setBearerToken("");
  }

  async function run(key: string, action: () => Promise<IntegrationStatus>, options: RunOptions = {}) {
    if (busy) return;
    setBusy(key);
    setFeedback(null);
    try {
      onStatus(await action());
      if (options.successTitle && options.successMessage) {
        setFeedback({ tone: "success", title: options.successTitle, message: options.successMessage });
      }
    } catch (reason) {
      setFeedback({
        tone: "error",
        title: options.errorTitle ?? text.errors.integration,
        message: errorMessage(reason, text.errors.integration)
      });
    } finally {
      if (options.clearInputs) clearCredentialInputs();
      setBusy("");
    }
  }

  async function addCredential() {
    const trimmedLabel = label.trim();
    if (!trimmedLabel) {
      setFeedback({ tone: "error", title: text.settings.validationNotStarted, message: text.settings.credentialLabelRequired });
      return;
    }
    if (id === "infinimesh-info") {
      if (!licenseId.trim() || !licenseKey.trim()) {
        setFeedback({ tone: "error", title: text.settings.validationNotStarted, message: text.settings.credentialFieldsRequired });
        return;
      }
      await run("add", () => api.addInfoCredential(trimmedLabel, licenseId.trim(), licenseKey.trim()), {
        clearInputs: true,
        successTitle: text.settings.validationSucceeded,
        successMessage: text.settings.credentialSaved,
        errorTitle: text.settings.validationFailed
      });
      return;
    }
    if (!endpoint.trim() || !bearerToken.trim()) {
      setFeedback({ tone: "error", title: text.settings.validationNotStarted, message: text.settings.credentialFieldsRequired });
      return;
    }
    await run("add", () => api.addLocalMindCredential(trimmedLabel, endpoint.trim(), bearerToken.trim()), {
      clearInputs: true,
      successTitle: text.settings.validationSucceeded,
      successMessage: text.settings.credentialSaved,
      errorTitle: text.settings.validationFailed
    });
  }

  function activateCredential(credentialId: string) {
    if (!status || status.active_credential_id === credentialId) return;
    if (status.configured && !window.confirm(text.settings.switchCredentialConfirm)) return;
    void run(`activate:${credentialId}`, () => api.activateIntegrationCredential(id, credentialId));
  }

  function activateOperator() {
    if (!status || status.source === "operator") return;
    if (status.configured && !window.confirm(text.settings.switchCredentialConfirm)) return;
    void run("activate:operator", () => api.activateIntegrationOperator(id));
  }

  function deleteCredential(credentialId: string) {
    if (!window.confirm(text.settings.deleteCredentialConfirm)) return;
    void run(`delete:${credentialId}`, () => api.deleteIntegrationCredential(id, credentialId));
  }

  const integrationTitle = id === "infinimesh-info" ? text.settings.info : text.settings.localMind;
  if (!status) {
    return <span className="muted">{busy ? text.settings.loadingIntegrations : text.settings.integrationUnavailable}</span>;
  }

  const validationActivity = busy === "add"
    ? { tone: "progress" as const, title: text.settings.validationInProgress, message: text.settings.validationInProgressDetail }
    : busy.startsWith("check:")
      ? { tone: "progress" as const, title: text.settings.connectionCheckInProgress, message: text.settings.connectionCheckInProgressDetail }
      : feedback;
  const displayedState = validationActivity?.tone === "progress" ? "checking" : status.state;

  return (
    <div className="integrationDetail" aria-busy={validationActivity?.tone === "progress"}>
      <div className="integrationStatusBar">
        <div>
          <strong>{integrationTitle}</strong>
          <span className="muted">{integrationStateLabel(displayedState, text)}</span>
        </div>
        <span className={`integrationState ${displayedState}`}>{integrationStateLabel(displayedState, text)}</span>
      </div>

      <div className="credentialList" aria-label={text.settings.savedCredentials}>
        {status.operator_available && (
          <div className={`credentialRow ${status.source === "operator" ? "selected" : ""}`}>
            <span className="credentialIcon"><ServerCog size={16} /></span>
            <div className="credentialIdentity">
              <strong>{text.settings.operatorConfiguration}</strong>
              <small>{status.source === "operator" ? text.settings.inUse : text.settings.available}</small>
            </div>
            <button
              className="miniIconButton"
              type="button"
              onClick={activateOperator}
              disabled={Boolean(busy) || status.source === "operator"}
              title={text.settings.useCredential}
            >
              {status.source === "operator" ? <CheckCircle2 size={15} /> : <Power size={15} />}
            </button>
          </div>
        )}
        {status.credentials.map((credential) => (
          <div className={`credentialRow ${credential.active ? "selected" : ""}`} key={credential.id}>
            <span className="credentialIcon"><KeyRound size={16} /></span>
            <div className="credentialIdentity">
              <strong>{credential.label}</strong>
              <small>
                {integrationStateLabel(credential.state, text)} · {formatIntegrationTime(credential.validated_at, language)}
              </small>
            </div>
            <div className="credentialActions">
              <button
                className="miniIconButton"
                type="button"
                onClick={() => void run(`check:${credential.id}`, () => api.checkIntegrationCredential(id, credential.id), {
                  successTitle: text.settings.connectionCheckSucceeded,
                  successMessage: text.settings.connectionCheckSucceededDetail,
                  errorTitle: text.settings.validationFailed
                })}
                disabled={Boolean(busy)}
                title={text.settings.checkConnection}
              >
                <RefreshCw size={14} className={busy === `check:${credential.id}` ? "spin" : ""} />
              </button>
              <button
                className="miniIconButton"
                type="button"
                onClick={() => activateCredential(credential.id)}
                disabled={Boolean(busy) || credential.active}
                title={text.settings.useCredential}
              >
                {credential.active ? <CheckCircle2 size={14} /> : <Power size={14} />}
              </button>
              <button
                className="miniIconButton dangerIcon"
                type="button"
                onClick={() => deleteCredential(credential.id)}
                disabled={Boolean(busy) || credential.active}
                title={credential.active ? text.settings.activeCredentialDeleteBlocked : text.settings.deleteCredential}
              >
                <Trash2 size={14} />
              </button>
            </div>
          </div>
        ))}
        {status.credentials.length === 0 && !status.operator_available && (
          <span className="integrationEmpty muted">{text.settings.noSavedCredentials}</span>
        )}
      </div>

      <form className="credentialForm" onSubmit={(event) => { event.preventDefault(); void addCredential(); }}>
        <label className="inputGroup compact">
          <span>{text.settings.credentialLabel}</span>
          <input
            value={label}
            onChange={(event) => { setLabel(event.target.value); setFeedback(null); }}
            maxLength={80}
            autoComplete="off"
            disabled={Boolean(busy)}
          />
        </label>
        {id === "infinimesh-info" ? (
          <>
            <label className="inputGroup compact">
              <span>{text.settings.licenseId}</span>
              <input
                value={licenseId}
                onChange={(event) => { setLicenseId(event.target.value); setFeedback(null); }}
                autoComplete="off"
                spellCheck={false}
                disabled={Boolean(busy)}
              />
            </label>
            <label className="inputGroup compact">
              <span>{text.settings.licenseKey}</span>
              <input
                type="password"
                value={licenseKey}
                onChange={(event) => { setLicenseKey(event.target.value); setFeedback(null); }}
                autoComplete="new-password"
                spellCheck={false}
                disabled={Boolean(busy)}
              />
            </label>
          </>
        ) : (
          <>
            <label className="inputGroup compact">
              <span>{text.settings.workspaceEndpoint}</span>
              <input
                type="url"
                value={endpoint}
                onChange={(event) => { setEndpoint(event.target.value); setFeedback(null); }}
                autoComplete="off"
                spellCheck={false}
                disabled={Boolean(busy)}
              />
            </label>
            <label className="inputGroup compact">
              <span>{text.settings.bearerToken}</span>
              <input
                type="password"
                value={bearerToken}
                onChange={(event) => { setBearerToken(event.target.value); setFeedback(null); }}
                autoComplete="new-password"
                spellCheck={false}
                disabled={Boolean(busy)}
              />
            </label>
          </>
        )}
        <button className="secondaryButton credentialSubmit" type="submit" disabled={Boolean(busy)}>
          {busy === "add" ? <LoaderCircle size={15} className="spin" /> : <Plus size={15} />}
          <span>{busy === "add" ? text.settings.validatingAndSaving : text.settings.validateAndSave}</span>
        </button>
      </form>
      {validationActivity && (
        <div
          className={`credentialValidationFeedback ${validationActivity.tone}`}
          role={validationActivity.tone === "error" ? "alert" : "status"}
          aria-live={validationActivity.tone === "error" ? "assertive" : "polite"}
        >
          <span className="credentialValidationIcon" aria-hidden="true">
            {validationActivity.tone === "progress" && <LoaderCircle size={17} className="spin" />}
            {validationActivity.tone === "success" && <CheckCircle2 size={17} />}
            {validationActivity.tone === "error" && <CircleAlert size={17} />}
          </span>
          <span className="credentialValidationCopy">
            <strong>{validationActivity.title}</strong>
            <small>{validationActivity.message}</small>
          </span>
        </div>
      )}
    </div>
  );
}

function formatIntegrationTime(value: string, language: Language) {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en-US", {
    year: "numeric", month: "short", day: "numeric"
  }).format(parsed);
}

function errorMessage(reason: unknown, fallback: string) {
  return reason instanceof Error && reason.message ? reason.message : fallback;
}
