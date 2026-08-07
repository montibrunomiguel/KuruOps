import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import type { IncidentPhase } from "../../types/incidents";
import { NIST_PHASE_ORDER } from "../../types/incidents";
import type { Playbook } from "../../types/playbooks";

type StepsState = Partial<Record<IncidentPhase, string[]>>;

function emptySteps(): StepsState {
  return {};
}

function cleanSteps(steps: StepsState): StepsState {
  const out: StepsState = {};
  for (const phase of NIST_PHASE_ORDER) {
    const values = (steps[phase] ?? []).map((s) => s.trim()).filter(Boolean);
    if (values.length > 0) out[phase] = values;
  }
  return out;
}

export function PlaybookDetailPage() {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const isNew = id === "new";
  const { token } = useAuth();
  const navigate = useNavigate();

  const { data: pbData, loading, error } = useList<Playbook>(
    async (t) => {
      if (isNew || !id) return [];
      return [await api.get<Playbook>(`/api/v1/playbooks/${id}`, t)];
    },
    [id],
  );
  const playbook = pbData?.[0];

  const [editing, setEditing] = useState(isNew);
  const [title, setTitle] = useState("");
  const [category, setCategory] = useState("");
  const [description, setDescription] = useState("");
  const [keywords, setKeywords] = useState("");
  const [steps, setSteps] = useState<StepsState>(emptySteps);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  useEffect(() => {
    if (playbook) {
      setTitle(playbook.title);
      setCategory(playbook.category);
      setDescription(playbook.description);
      setKeywords(playbook.keywords.join(", "));
      setSteps(playbook.steps);
    }
  }, [playbook]);

  function addStep(phase: IncidentPhase) {
    setSteps((s) => ({ ...s, [phase]: [...(s[phase] ?? []), ""] }));
  }

  function updateStep(phase: IncidentPhase, idx: number, value: string) {
    setSteps((s) => {
      const arr = [...(s[phase] ?? [])];
      arr[idx] = value;
      return { ...s, [phase]: arr };
    });
  }

  function removeStep(phase: IncidentPhase, idx: number) {
    setSteps((s) => {
      const arr = [...(s[phase] ?? [])];
      arr.splice(idx, 1);
      return { ...s, [phase]: arr };
    });
  }

  async function handleSave() {
    if (!title.trim()) {
      setFormError(t("playbooks.detail.titleRequired"));
      return;
    }
    setSubmitting(true);
    setFormError(null);
    const body = {
      title,
      category,
      description,
      keywords: keywords.split(",").map((k) => k.trim()).filter(Boolean),
      steps: cleanSteps(steps),
    };
    try {
      if (isNew) {
        const created = await api.post<Playbook>("/api/v1/playbooks", body, token);
        navigate(`/playbooks/${created.id}`, { replace: true });
      } else if (id) {
        await api.put<Playbook>(`/api/v1/playbooks/${id}`, body, token);
        setEditing(false);
      }
    } catch (err) {
      setFormError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleDelete() {
    if (!id || isNew) return;
    setSubmitting(true);
    try {
      await api.del(`/api/v1/playbooks/${id}`, token);
      navigate("/playbooks", { replace: true });
    } catch (err) {
      setFormError(mutationErrorMessage(err));
      setConfirmingDelete(false);
      setSubmitting(false);
    }
  }

  if (!isNew && loading) return <div className="empty-state">{t("common.loading")}</div>;
  if (!isNew && error) return <div className="error-banner">{error}</div>;
  if (!isNew && !playbook) return <div className="empty-state">{t("playbooks.detail.notFound")}</div>;

  return (
    <div>
      <Link to="/playbooks" className="back-link">
        {t("playbooks.detail.backToPlaybooks")}
      </Link>

      <div className="detail-header">
        <div style={{ flex: 1 }}>
          {editing ? (
            <input
              className="input"
              style={{ fontSize: 17, fontWeight: 700, width: "100%", marginBottom: 4 }}
              placeholder={t("playbooks.detail.titlePlaceholder")}
              value={title}
              onChange={(e) => setTitle(e.target.value)}
            />
          ) : (
            <h1 className="page-title">{playbook?.title}</h1>
          )}
          {!editing && <p className="page-sub" style={{ marginBottom: 0 }}>{playbook?.category}</p>}
        </div>
        <div className="toolbar-actions">
          {!editing && !isNew && (
            <>
              <button className="btn btn-sm" onClick={() => setEditing(true)}>
                {t("playbooks.detail.edit")}
              </button>
              {confirmingDelete ? (
                <>
                  <span className="helper-text" style={{ flexBasis: "100%" }}>
                    {t("playbooks.detail.deleteConfirm")}
                  </span>
                  <button className="btn btn-danger btn-sm" onClick={handleDelete} disabled={submitting}>
                    {submitting ? t("common.saving") : t("common.confirmDelete")}
                  </button>
                  <button className="btn btn-ghost btn-sm" onClick={() => setConfirmingDelete(false)}>
                    {t("common.cancel")}
                  </button>
                </>
              ) : (
                <button className="btn btn-danger btn-sm" onClick={() => setConfirmingDelete(true)} disabled={submitting}>
                  {t("playbooks.detail.delete")}
                </button>
              )}
            </>
          )}
          {editing && (
            <>
              <button className="btn btn-primary btn-sm" onClick={handleSave} disabled={submitting}>
                {submitting ? t("common.saving") : t("common.save")}
              </button>
              {!isNew && (
                <button
                  className="btn btn-ghost btn-sm"
                  onClick={() => {
                    setEditing(false);
                    if (playbook) {
                      setTitle(playbook.title);
                      setCategory(playbook.category);
                      setDescription(playbook.description);
                      setKeywords(playbook.keywords.join(", "));
                      setSteps(playbook.steps);
                    }
                  }}
                >
                  {t("common.cancel")}
                </button>
              )}
            </>
          )}
        </div>
      </div>

      {formError && <div className="error-banner">{formError}</div>}

      {editing && (
        <div className="panel" style={{ marginBottom: 16 }}>
          <div className="form-grid">
            <div className="field">
              <label htmlFor="pb-category">{t("playbooks.detail.category")}</label>
              <input
                id="pb-category"
                className="input"
                placeholder={t("playbooks.detail.categoryPlaceholder")}
                value={category}
                onChange={(e) => setCategory(e.target.value)}
              />
            </div>
            <div className="field">
              <label htmlFor="pb-keywords">{t("playbooks.detail.keywords")}</label>
              <input
                id="pb-keywords"
                className="input"
                value={keywords}
                onChange={(e) => setKeywords(e.target.value)}
              />
            </div>
            <div className="field field-full">
              <label htmlFor="pb-description">{t("playbooks.detail.description")}</label>
              <textarea
                id="pb-description"
                className="textarea"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
            </div>
          </div>
        </div>
      )}

      {!editing && playbook?.description && (
        <div className="panel" style={{ marginBottom: 16 }}>
          <p style={{ margin: 0, fontSize: 13, whiteSpace: "pre-wrap" }}>{playbook.description}</p>
        </div>
      )}

      <div className="panel">
        <h2 className="panel-title" style={{ marginBottom: 12 }}>
          {t("playbooks.detail.stepsByPhaseTitle")}
        </h2>
        {NIST_PHASE_ORDER.map((phase) => {
          const phaseSteps = (editing ? steps[phase] : playbook?.steps[phase]) ?? [];
          if (!editing && phaseSteps.length === 0) return null;
          return (
            <div key={phase} style={{ marginBottom: 18 }}>
              <p style={{ fontSize: 12, fontWeight: 700, textTransform: "uppercase", letterSpacing: "0.03em", color: "var(--text-muted)", margin: "0 0 8px" }}>
                {t(`common.phase.${phase}`)}
              </p>
              {editing ? (
                <>
                  {phaseSteps.map((step, idx) => (
                    <div className="step-editor-row" key={idx}>
                      <input
                        className="input"
                        value={step}
                        onChange={(e) => updateStep(phase, idx, e.target.value)}
                        placeholder={t("playbooks.detail.stepPlaceholder", { num: idx + 1 })}
                      />
                      <button type="button" className="btn btn-ghost btn-sm" onClick={() => removeStep(phase, idx)}>
                        {t("playbooks.detail.removeStep")}
                      </button>
                    </div>
                  ))}
                  <button type="button" className="btn btn-sm" onClick={() => addStep(phase)}>
                    {t("playbooks.detail.addStep")}
                  </button>
                </>
              ) : (
                <ol style={{ margin: 0, paddingLeft: 20, fontSize: 13, display: "flex", flexDirection: "column", gap: 6 }}>
                  {phaseSteps.map((step, idx) => (
                    <li key={idx}>{step}</li>
                  ))}
                </ol>
              )}
            </div>
          );
        })}
        {!editing && playbook && Object.keys(playbook.steps).length === 0 && (
          <div className="empty-state">{t("playbooks.detail.noSteps")}</div>
        )}
      </div>
    </div>
  );
}
