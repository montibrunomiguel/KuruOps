import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import { useConfirm } from "../../hooks/useConfirm";
import type { LLMProvider, LLMProviderKind } from "../../types/api";

const KIND_ORDER: LLMProviderKind[] = ["anthropic", "openai_compatible", "azure_openai", "self_hosted"];

export function LLMProvidersPanel() {
  const { t } = useTranslation();
  const { data: providers, loading, error, reload } = useList<LLMProvider>((tk) =>
    api.get<LLMProvider[]>("/api/v1/settings/llm-providers", tk),
  );
  const [showCreate, setShowCreate] = useState(false);

  return (
    <div className="panel">
      <div className="panel-header">
        <h2 className="panel-title">{t("settings.llm.title")}</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowCreate(true)}>
          {t("settings.llm.newProvider")}
        </button>
      </div>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.llm.helperTextPre")}
        <code>openai_compatible</code>
        {t("settings.llm.helperTextPost")}
      </p>

      {error && <div className="error-banner">{error}</div>}

      {showCreate && (
        <ProviderForm
          onCancel={() => setShowCreate(false)}
          onSaved={() => {
            setShowCreate(false);
            reload();
          }}
        />
      )}

      {loading && <div className="empty-state">{t("common.loading")}</div>}
      {!loading && providers && providers.length === 0 && (
        <div className="empty-state">{t("settings.llm.noProviders")}</div>
      )}
      {!loading &&
        providers &&
        providers.map((p) => <ProviderRow key={p.id} provider={p} onChanged={reload} />)}
    </div>
  );
}

function ProviderForm({ onCancel, onSaved }: { onCancel: () => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [name, setName] = useState("");
  const [kind, setKind] = useState<LLMProviderKind>("anthropic");
  const [baseUrl, setBaseUrl] = useState("");
  const [model, setModel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [showKey, setShowKey] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const needsBaseUrl = kind !== "anthropic";
  const kindLabel = (k: LLMProviderKind) => t(`settings.llm.kind.${k}`);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await api.post(
        "/api/v1/settings/llm-providers",
        { name, kind, baseUrl: needsBaseUrl ? baseUrl : undefined, model, apiKey },
        token,
      );
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="panel" style={{ marginBottom: 14 }}>
      {error && <div className="error-banner">{error}</div>}

      <div className="field">
        <label>{t("settings.llm.form.provider")}</label>
        <div className="pill-tabs">
          {KIND_ORDER.map((k) => (
            <button
              key={k}
              type="button"
              className="pill-tab"
              data-active={kind === k}
              onClick={() => setKind(k)}
            >
              {kindLabel(k).split(" ")[0]}
            </button>
          ))}
        </div>
        <span className="field-hint">{kindLabel(kind)}</span>
      </div>

      <div className="form-grid">
        <div className="field">
          <label htmlFor="llm-name">{t("settings.llm.form.name")}</label>
          <input id="llm-name" className="input" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="field">
          <label htmlFor="llm-model">{t("settings.llm.form.model")}</label>
          <input
            id="llm-model"
            className="input"
            placeholder={t("settings.llm.form.modelPlaceholder")}
            value={model}
            onChange={(e) => setModel(e.target.value)}
            required
          />
        </div>
        {needsBaseUrl && (
          <div className="field field-full">
            <label htmlFor="llm-baseurl">{t("settings.llm.form.baseUrl")}</label>
            <input
              id="llm-baseurl"
              className="input"
              placeholder={t("settings.llm.form.baseUrlPlaceholder")}
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              required
            />
          </div>
        )}
        <div className="field field-full">
          <label htmlFor="llm-apikey">{t("settings.llm.form.apiKey")}</label>
          <div className="token-reveal">
            <input
              id="llm-apikey"
              className="input"
              type={showKey ? "text" : "password"}
              style={{ flex: 1 }}
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              required
            />
            <button type="button" className="btn btn-sm" onClick={() => setShowKey((s) => !s)}>
              {showKey ? t("settings.llm.form.hide") : t("settings.llm.form.show")}
            </button>
          </div>
        </div>
      </div>

      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
          {submitting ? t("common.saving") : t("common.save")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}

function ProviderRow({ provider, onChanged }: { provider: LLMProvider; onChanged: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { confirming, confirm, cancel } = useConfirm();

  async function setDefault() {
    setBusy(true);
    setError(null);
    try {
      await api.post(`/api/v1/settings/llm-providers/${provider.id}/default`, {}, token);
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    setBusy(true);
    setError(null);
    try {
      await api.del(`/api/v1/settings/llm-providers/${provider.id}`, token);
      cancel();
      onChanged();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="row">
      <div className="row-main">
        <p className="row-title">
          {provider.name}{" "}
          {provider.isDefault && <span className="badge badge-success">{t("settings.llm.defaultBadge")}</span>}
        </p>
        <p className="row-sub">
          {t(`settings.llm.kind.${provider.kind}`)} · {provider.model}
          {provider.baseUrl ? ` · ${provider.baseUrl}` : ""}
        </p>
        {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
      </div>
      <div className="row-actions">
        {!provider.isDefault && (
          <button className="btn btn-sm" onClick={setDefault} disabled={busy}>
            {t("settings.llm.setDefault")}
          </button>
        )}
        {confirming ? (
          <>
            <span className="helper-text" style={{ flexBasis: "100%" }}>
              {t("settings.llm.removeConfirm", { name: provider.name })}
            </span>
            <button className="btn btn-danger btn-sm" onClick={remove} disabled={busy}>
              {busy ? t("common.saving") : t("common.confirmDelete")}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={cancel}>
              {t("common.cancel")}
            </button>
          </>
        ) : (
          <button className="btn btn-danger btn-sm" onClick={() => confirm()} disabled={busy}>
            {t("common.remove")}
          </button>
        )}
      </div>
    </div>
  );
}
