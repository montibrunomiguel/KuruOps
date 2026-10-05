import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../../auth/AuthContext";
import { api } from "../../../api/client";
import { mutationErrorMessage } from "../../../api/hooks";
import type { MCPServer } from "../../../types/api";
import { AuthFields, authDraftFor, authPayload, type AuthDraft } from "./AuthFields";

// AuthEditPanel changes (or rotates) a saved server's authentication. The
// update endpoint replaces the whole server, so the unchanged fields are sent
// back as they are; secrets are never loaded into the form, so a blank secret
// field means "keep the stored one" (see AuthFields).
export function AuthEditPanel({
  server,
  onClose,
  onSaved,
}: {
  server: MCPServer;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const [auth, setAuth] = useState<AuthDraft>(() => authDraftFor(server));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      await api.put(
        `/api/v1/settings/mcp-servers/${server.id}`,
        {
          name: server.name,
          transport: server.transport,
          endpointOrCommand: server.endpointOrCommand,
          allowedTools: server.allowedTools,
          sideEffectingTools: server.sideEffectingTools,
          enabledFor: server.enabledFor,
          ...authPayload(auth),
        },
        token,
      );
      onSaved();
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="panel" style={{ width: "100%", marginTop: 12 }}>
      {error && <div className="error-banner">{error}</div>}
      <div className="form-grid">
        <AuthFields value={auth} onChange={setAuth} saved={server} />
      </div>
      <div className="row-actions">
        <button type="submit" className="btn btn-primary btn-sm" disabled={saving}>
          {saving ? t("common.saving") : t("common.save")}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClose}>
          {t("common.cancel")}
        </button>
      </div>
    </form>
  );
}
