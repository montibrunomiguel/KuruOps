import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import type { WebhookEndpoint } from "../../types/api";
import { formatDateTime } from "../../lib/format";

function expiryOptions(t: (k: string) => string): { value: string; label: string }[] {
  return [
    { value: "30", label: t("settings.webhooks.expiry.30") },
    { value: "90", label: t("settings.webhooks.expiry.90") },
    { value: "180", label: t("settings.webhooks.expiry.180") },
    { value: "365", label: t("settings.webhooks.expiry.365") },
    { value: "never", label: t("settings.webhooks.expiry.never") },
  ];
}

function expiryToDays(value: string): number | undefined {
  // undefined -> let the backend apply its own default (90d); "never" -> 0,
  // which service.resolveExpiry treats as an explicit opt-out.
  if (value === "never") return 0;
  return Number(value);
}

export function WebhooksPanel() {
  const { t } = useTranslation();
  const { data: endpoints, loading, error, reload } = useList<WebhookEndpoint>(
    (tk) => api.get<WebhookEndpoint[]>("/api/v1/settings/webhooks", tk),
  );

  const [showCreate, setShowCreate] = useState(false);
  const [newToken, setNewToken] = useState<{ name: string; token: string } | null>(null);

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.webhooks.title")}</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
          {t("settings.webhooks.newEndpoint")}
        </button>
      </div>

      {error && <div className="error-banner">{error}</div>}

      {newToken && (
        <div className="panel" style={{ background: "var(--accent-soft)", borderColor: "var(--accent)", marginBottom: 14 }}>
          <p style={{ margin: "0 0 8px", fontSize: 12.5 }}>
            {t("settings.webhooks.tokenGenerated", { name: newToken.name })}
          </p>
          <div className="token-reveal">
            <code style={{ fontSize: 12 }}>{newToken.token}</code>
            <button
              className="btn btn-sm"
              onClick={() => {
                navigator.clipboard.writeText(newToken.token);
              }}
            >
              {t("common.copy")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setNewToken(null)}>
              {t("common.close")}
            </button>
          </div>
        </div>
      )}

      {showCreate && (
        <CreateWebhookForm
          onCancel={() => setShowCreate(false)}
          onCreated={(name, tok) => {
            setShowCreate(false);
            setNewToken({ name, token: tok });
            reload();
          }}
        />
      )}

      {loading && <div className="empty-state">{t("common.loading")}</div>}
      {!loading && endpoints && endpoints.length === 0 && (
        <div className="empty-state">{t("settings.webhooks.noEndpoints")}</div>
      )}

      {!loading &&
        endpoints &&
        endpoints.map((ep) => (
          <WebhookRow
            key={ep.id}
            endpoint={ep}
            onChanged={reload}
            // Regenerate's onChanged() triggers this same reload -- which
            // sets loading:true and (per the !loading guard above) briefly
            // unmounts every row, including the one that just called
            // setRegenerated on itself. A regenerated token held in local
            // row state would be destroyed before ever being painted, so
            // instead it's lifted here to survive the reload, the same way
            // the create flow's newToken already does.
            onRegenerated={(tok) => setNewToken({ name: ep.name, token: tok })}
          />
        ))}
    </div>
  );
}

function CreateWebhookForm({
  onCancel,
  onCreated,
}: {
  onCancel: () => void;
  onCreated: (name: string, token: string) => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [name, setName] = useState("");
  const [source, setSource] = useState("");
  const [expiresInDays, setExpiresInDays] = useState("90");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const res = await api.post<{ endpoint: WebhookEndpoint; token: string }>(
        "/api/v1/settings/webhooks",
        { name, source, expiresInDays: expiryToDays(expiresInDays) },
        token,
      );
      onCreated(name, res.token);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  const options = expiryOptions(t);

  return (
    <form onSubmit={handleSubmit} className="panel" style={{ marginBottom: 14 }}>
      {error && <div className="error-banner">{error}</div>}
      <div className="form-grid">
        <div className="field">
          <label htmlFor="wh-name">{t("settings.webhooks.form.name")}</label>
          <input id="wh-name" className="input" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="field">
          <label htmlFor="wh-source">{t("settings.webhooks.form.source")}</label>
          <input
            id="wh-source"
            className="input"
            placeholder={t("settings.webhooks.form.sourcePlaceholder")}
            value={source}
            onChange={(e) => setSource(e.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="wh-expiry">{t("settings.webhooks.form.tokenExpiry")}</label>
          <select id="wh-expiry" className="select" value={expiresInDays} onChange={(e) => setExpiresInDays(e.target.value)}>
            {options.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
      </div>
      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("common.creating") : t("common.create")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}

// EXPIRING_SOON_DAYS controls when a live token starts showing the
// "expiring soon" warning instead of the plain expiry date -- gives an
// admin a heads-up window to regenerate before ingest starts rejecting it.
const EXPIRING_SOON_DAYS = 14;

function expiryBadge(t: (k: string, opts?: Record<string, unknown>) => string, expiresAt?: string) {
  if (!expiresAt) return <span className="badge badge-muted">{t("settings.webhooks.badge.neverExpires")}</span>;
  const diffDays = (new Date(expiresAt).getTime() - Date.now()) / 86_400_000;
  if (diffDays < 0) {
    return (
      <span className="badge badge-critical">
        {t("settings.webhooks.badge.expiredOn", { date: formatDateTime(expiresAt) })}
      </span>
    );
  }
  if (diffDays <= EXPIRING_SOON_DAYS) {
    return (
      <span className="badge badge-sev-high">
        {t("settings.webhooks.badge.expiringSoon", { date: formatDateTime(expiresAt) })}
      </span>
    );
  }
  return (
    <span className="badge badge-muted">
      {t("settings.webhooks.badge.expiresOn", { date: formatDateTime(expiresAt) })}
    </span>
  );
}

function WebhookRow({
  endpoint,
  onChanged,
  onRegenerated,
}: {
  endpoint: WebhookEndpoint;
  onChanged: () => void;
  onRegenerated: (token: string) => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [busy, setBusy] = useState(false);
  const [regenExpiry, setRegenExpiry] = useState("90");
  const [error, setError] = useState<string | null>(null);

  async function toggleStatus() {
    setBusy(true);
    setError(null);
    try {
      const action = endpoint.status === "active" ? "disable" : "enable";
      await api.post(`/api/v1/settings/webhooks/${endpoint.id}/${action}`, {}, token);
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function regenerate() {
    setBusy(true);
    setError(null);
    try {
      const res = await api.post<{ token: string }>(
        `/api/v1/settings/webhooks/${endpoint.id}/regenerate`,
        { expiresInDays: expiryToDays(regenExpiry) },
        token,
      );
      onRegenerated(res.token);
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const options = expiryOptions(t);

  return (
    <div className="row">
      <div className="row-main">
        <p className="row-title">
          {endpoint.name}{" "}
          <span className={`badge ${endpoint.status === "active" ? "badge-success" : "badge-muted"}`}>
            <span className="badge-status-dot" />
            {endpoint.status}
          </span>{" "}
          {expiryBadge(t, endpoint.expiresAt)}
        </p>
        <p className="row-sub">
          {endpoint.source} · token whk_••••••••{endpoint.tokenLast4}
        </p>
        {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
      </div>
      <div className="row-actions">
        <select
          className="select"
          style={{ padding: "4px 8px", fontSize: 11.5 }}
          value={regenExpiry}
          onChange={(e) => setRegenExpiry(e.target.value)}
          title={t("settings.webhooks.regenExpiryTitle")}
        >
          {options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <button className="btn btn-sm" onClick={regenerate} disabled={busy}>
          {t("settings.webhooks.regenerate")}
        </button>
        <button className="btn btn-sm" onClick={toggleStatus} disabled={busy}>
          {endpoint.status === "active" ? t("common.disable") : t("common.enable")}
        </button>
      </div>
    </div>
  );
}
