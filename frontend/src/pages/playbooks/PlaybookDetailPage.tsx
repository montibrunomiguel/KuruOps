import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { useList, mutationErrorMessage } from "../../api/hooks";
import { useConfirm } from "../../hooks/useConfirm";
import { WEBHOOK_PAYLOAD_PLACEHOLDERS } from "../../types/api";
import type { IncidentPhase } from "../../types/incidents";
import { NIST_PHASE_ORDER } from "../../types/incidents";
import type { Playbook } from "../../types/playbooks";

// EditableStep drops PlaybookStep's `id` -- a step being edited (including
// every brand-new one added via "+ Add Step") has no id yet, since
// PlaybookRepository.replaceSteps always deletes-and-reinserts on save,
// generating fresh ids server-side. The edit form never needs to know a
// step's id; only the read-only PlaybookViewModal's "Run automation"
// button does, reading it straight off the freshly-fetched Playbook.
interface EditableStep {
  text: string;
  webhookUrl: string;
  webhookPayloadTemplate: string;
}

type StepsState = Partial<Record<IncidentPhase, EditableStep[]>>;

function emptySteps(): StepsState {
  return {};
}

function cleanSteps(steps: StepsState): StepsState {
  const out: StepsState = {};
  for (const phase of NIST_PHASE_ORDER) {
    const values = (steps[phase] ?? [])
      .map((s) => ({ text: s.text.trim(), webhookUrl: s.webhookUrl.trim(), webhookPayloadTemplate: s.webhookPayloadTemplate }))
      .filter((s) => s.text);
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
    ["playbook-detail", id],
    async (t) => {
      if (isNew || !id) return [];
      return [await api.get<Playbook>(`/api/v1/playbooks/${id}`, t)];
    },
  );
  const playbook = pbData?.[0];

  const [editing, setEditing] = useState(isNew);
  const [title, setTitle] = useState("");
  const [category, setCategory] = useState("");
  const [description, setDescription] = useState("");
  const [keywords, setKeywords] = useState("");
  const [alertNamePattern, setAlertNamePattern] = useState("");
  const [isDefault, setIsDefault] = useState(false);
  const [steps, setSteps] = useState<StepsState>(emptySteps);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const { confirming: confirmingDelete, confirm: confirmDelete, cancel: cancelDelete } = useConfirm();

  function loadFromPlaybook(pb: Playbook) {
    setTitle(pb.title);
    setCategory(pb.category);
    setDescription(pb.description);
    setKeywords(pb.keywords.join(", "));
    setAlertNamePattern(pb.alertNamePattern ?? "");
    setIsDefault(pb.isDefault ?? false);
    const loaded: StepsState = {};
    for (const phase of NIST_PHASE_ORDER) {
      const phaseSteps = pb.steps[phase];
      if (phaseSteps) {
        loaded[phase] = phaseSteps.map((s) => ({
          text: s.text,
          webhookUrl: s.webhookUrl ?? "",
          webhookPayloadTemplate: s.webhookPayloadTemplate ?? "",
        }));
      }
    }
    setSteps(loaded);
  }

  useEffect(() => {
    if (playbook) loadFromPlaybook(playbook);
  }, [playbook]);

  function addStep(phase: IncidentPhase) {
    setSteps((s) => ({ ...s, [phase]: [...(s[phase] ?? []), { text: "", webhookUrl: "", webhookPayloadTemplate: "" }] }));
  }

  function updateStep(phase: IncidentPhase, idx: number, patch: Partial<EditableStep>) {
    setSteps((s) => {
      const arr = [...(s[phase] ?? [])];
      arr[idx] = { ...arr[idx], ...patch };
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
      alertNamePattern: alertNamePattern.trim(),
      isDefault,
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
      cancelDelete();
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
          {!editing && playbook && (
            <p className="page-sub" style={{ marginBottom: 0 }}>
              {playbook.isDefault
                ? t("playbooks.detail.defaultBadge")
                : playbook.alertNamePattern
                  ? t("playbooks.detail.patternSummary", { pattern: playbook.alertNamePattern })
                  : t("playbooks.detail.noPatternSummary")}
            </p>
          )}
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
                  <button className="btn btn-ghost btn-sm" onClick={cancelDelete}>
                    {t("common.cancel")}
                  </button>
                </>
              ) : (
                <button className="btn btn-danger btn-sm" onClick={() => confirmDelete()} disabled={submitting}>
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
                    if (playbook) loadFromPlaybook(playbook);
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
            <div className="field">
              <label htmlFor="pb-alert-name-pattern">{t("playbooks.detail.alertNamePattern")}</label>
              <input
                id="pb-alert-name-pattern"
                className="input"
                placeholder={t("playbooks.detail.alertNamePatternPlaceholder")}
                value={alertNamePattern}
                onChange={(e) => setAlertNamePattern(e.target.value)}
              />
              <p className="helper-text" style={{ marginTop: 4 }}>{t("playbooks.detail.alertNamePatternHelp")}</p>
            </div>
            <div className="field">
              <label htmlFor="pb-is-default" style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer" }}>
                <input
                  id="pb-is-default"
                  type="checkbox"
                  checked={isDefault}
                  onChange={(e) => setIsDefault(e.target.checked)}
                />
                {t("playbooks.detail.isDefault")}
              </label>
              <p className="helper-text" style={{ marginTop: 4 }}>{t("playbooks.detail.isDefaultHelp")}</p>
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
          const phaseSteps = (editing ? steps[phase] : playbook?.steps[phase]?.map((s) => ({ text: s.text, webhookUrl: s.webhookUrl ?? "", webhookPayloadTemplate: s.webhookPayloadTemplate ?? "" }))) ?? [];
          if (!editing && phaseSteps.length === 0) return null;
          const isContainment = phase === "containment";
          return (
            <div key={phase} style={{ marginBottom: 18 }}>
              <p style={{ fontSize: 12, fontWeight: 700, textTransform: "uppercase", letterSpacing: "0.03em", color: "var(--text-muted)", margin: "0 0 8px" }}>
                {t(`common.phase.${phase}`)}
              </p>
              {editing ? (
                <>
                  {phaseSteps.map((step, idx) => (
                    <div key={idx} style={{ marginBottom: 10, padding: isContainment ? 10 : 0, borderRadius: 7, border: isContainment ? "1px solid var(--border)" : "none" }}>
                      <div className="step-editor-row">
                        <input
                          className="input"
                          value={step.text}
                          onChange={(e) => updateStep(phase, idx, { text: e.target.value })}
                          placeholder={t("playbooks.detail.stepPlaceholder", { num: idx + 1 })}
                        />
                        <button type="button" className="btn btn-ghost btn-sm" onClick={() => removeStep(phase, idx)}>
                          {t("playbooks.detail.removeStep")}
                        </button>
                      </div>
                      {isContainment && (
                        <div style={{ marginTop: 6 }}>
                          <input
                            className="input"
                            style={{ marginBottom: step.webhookUrl ? 6 : 0 }}
                            placeholder={t("playbooks.detail.webhookUrlPlaceholder")}
                            value={step.webhookUrl}
                            onChange={(e) => updateStep(phase, idx, { webhookUrl: e.target.value })}
                          />
                          {step.webhookUrl && (
                            <>
                              <textarea
                                className="textarea"
                                style={{ minHeight: 60, fontSize: 12 }}
                                placeholder={t("playbooks.detail.webhookPayloadTemplatePlaceholder")}
                                value={step.webhookPayloadTemplate}
                                onChange={(e) => updateStep(phase, idx, { webhookPayloadTemplate: e.target.value })}
                              />
                              <p className="helper-text" style={{ marginTop: 4 }}>
                                {t("playbooks.detail.webhookPayloadTemplateHelp")} {WEBHOOK_PAYLOAD_PLACEHOLDERS.join(", ")}
                              </p>
                            </>
                          )}
                        </div>
                      )}
                    </div>
                  ))}
                  <button type="button" className="btn btn-sm" onClick={() => addStep(phase)}>
                    {t("playbooks.detail.addStep")}
                  </button>
                </>
              ) : (
                <ol style={{ margin: 0, paddingLeft: 20, fontSize: 13, display: "flex", flexDirection: "column", gap: 6 }}>
                  {phaseSteps.map((step, idx) => (
                    <li key={idx}>
                      {step.text}
                      {step.webhookUrl && (
                        <span className="badge badge-muted" style={{ marginLeft: 6 }}>
                          {t("playbooks.detail.webhookConfiguredBadge")}
                        </span>
                      )}
                    </li>
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
