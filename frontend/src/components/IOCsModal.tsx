import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { useList, mutationErrorMessage } from "../api/hooks";
import type { IOC } from "../types/incidents";
import { IOC_TYPE_ORDER } from "../types/incidents";
import { formatDate } from "../lib/format";
import { Modal } from "./Modal";

function todayDateInputValue(): string {
  const d = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

// IOCsModal is the popup opened from the incident detail page's "IOCs"
// button -- a list of every Indicator of Compromise recorded against this
// incident, with an inline form (toggled, not a nested modal) to record a
// new one. IOCs are incident-scoped and append-only (no edit/delete route
// -- see backend domain.IOC's doc comment), and are pulled into both the
// Markdown postmortem and the PDF report automatically once recorded here
// (PostmortemService.Generate/IncidentReportService.GeneratePDF both call
// IncidentService.IOCs), so nothing further is needed to have a new IOC
// show up in either document.
export function IOCsModal({ incidentId, onClose }: { incidentId: string; onClose: () => void }) {
  const { t } = useTranslation();
  const { token } = useAuth();

  const { data: iocs, loading, error, reload } = useList<IOC>(
    ["incident-iocs-modal", incidentId],
    (tk) => api.get<IOC[]>(`/api/v1/incidents/${incidentId}/iocs`, tk),
  );

  const [showForm, setShowForm] = useState(false);
  const [type, setType] = useState(IOC_TYPE_ORDER[0]);
  const [value, setValue] = useState("");
  const [description, setDescription] = useState("");
  const [identifiedAt, setIdentifiedAt] = useState(todayDateInputValue);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  function resetForm() {
    setType(IOC_TYPE_ORDER[0]);
    setValue("");
    setDescription("");
    setIdentifiedAt(todayDateInputValue());
    setFormError(null);
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    // No client-side "value is required" check here -- the input's own
    // `required` attribute already blocks submission natively (with the
    // browser's own inline validation message), same as every other
    // required-field form in this app (see CreateWebhookModal).
    setSubmitting(true);
    setFormError(null);
    try {
      // identifiedAt is a plain YYYY-MM-DD from the date input, sent as
      // that date's UTC midnight instant -- deliberately UTC, not the
      // browser's local midnight: this is a calendar date, not a moment in
      // time, and encoding it as UTC midnight is the one interpretation
      // that's the same instant (and so round-trips to the same displayed
      // date) no matter which timezone the analyst who typed it, or a
      // later viewer, is in. See formatDate's doc comment for the read
      // side of this -- it must format with timeZone: "UTC" to match.
      await api.post(`/api/v1/incidents/${incidentId}/iocs`, {
        type,
        value: value.trim(),
        description: description.trim(),
        identifiedAt: new Date(identifiedAt).toISOString(),
      }, token);
      resetForm();
      setShowForm(false);
      reload();
    } catch (err) {
      setFormError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Modal onClose={onClose} label={t("incidents.iocs.title")} style={{ maxWidth: 620, display: "flex", flexDirection: "column", height: "min(680px, 85vh)" }}>
      <div className="panel-header">
        <h2 className="modal-title" style={{ marginBottom: 0 }}>
          {t("incidents.iocs.title")}
        </h2>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label={t("common.close")}>
          ×
        </button>
      </div>

      <div style={{ flex: 1, overflowY: "auto", marginTop: 4 }}>
        {loading && <div className="empty-state">{t("common.loading")}</div>}
        {error && <div className="error-banner">{error}</div>}
        {!loading && !error && (iocs?.length ?? 0) === 0 && <div className="empty-state">{t("incidents.iocs.empty")}</div>}
        {!loading &&
          iocs?.map((ioc) => (
            <div key={ioc.id} className="row" style={{ display: "block", marginBottom: 8 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                <span className="badge badge-muted">{t(`incidents.iocs.type.${ioc.type}`)}</span>
                <span className="mono" style={{ fontSize: 13 }}>
                  {ioc.value}
                </span>
              </div>
              {ioc.description && (
                <p className="row-sub" style={{ marginTop: 4 }}>
                  {ioc.description}
                </p>
              )}
              <p className="row-sub" style={{ marginTop: 4 }}>
                {t("incidents.iocs.identifiedBy", {
                  date: formatDate(ioc.identifiedAt),
                  name: ioc.createdByName,
                })}
              </p>
            </div>
          ))}
      </div>

      {showForm ? (
        <form onSubmit={handleSubmit} style={{ borderTop: "1px solid var(--border)", paddingTop: 12, marginTop: 12 }}>
          {formError && <div className="error-banner">{formError}</div>}
          <div className="form-grid">
            <div className="field">
              <label htmlFor="ioc-type">{t("incidents.iocs.form.type")}</label>
              <select id="ioc-type" className="select" value={type} onChange={(e) => setType(e.target.value as typeof type)}>
                {IOC_TYPE_ORDER.map((v) => (
                  <option key={v} value={v}>
                    {t(`incidents.iocs.type.${v}`)}
                  </option>
                ))}
              </select>
            </div>
            <div className="field">
              <label htmlFor="ioc-identified-at">{t("incidents.iocs.form.identifiedAt")}</label>
              <input
                id="ioc-identified-at"
                type="date"
                className="input"
                value={identifiedAt}
                onChange={(e) => setIdentifiedAt(e.target.value)}
                required
              />
            </div>
          </div>
          <div className="field" style={{ marginTop: 10 }}>
            <label htmlFor="ioc-value">{t("incidents.iocs.form.value")}</label>
            <input
              id="ioc-value"
              className="input"
              placeholder={t("incidents.iocs.form.valuePlaceholder")}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              required
            />
          </div>
          <div className="field" style={{ marginTop: 10 }}>
            <label htmlFor="ioc-description">{t("incidents.iocs.form.description")}</label>
            <textarea
              id="ioc-description"
              className="textarea"
              style={{ width: "100%", minHeight: 50 }}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </div>
          <div className="row-actions" style={{ marginTop: 10 }}>
            <button type="submit" className="btn btn-primary btn-sm" disabled={submitting}>
              {submitting ? t("common.saving") : t("incidents.iocs.form.submit")}
            </button>
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={() => {
                setShowForm(false);
                resetForm();
              }}
            >
              {t("common.cancel")}
            </button>
          </div>
        </form>
      ) : (
        <div style={{ borderTop: "1px solid var(--border)", paddingTop: 12, marginTop: 12 }}>
          <button type="button" className="btn btn-sm" onClick={() => setShowForm(true)}>
            + {t("incidents.iocs.addButton")}
          </button>
        </div>
      )}
    </Modal>
  );
}
