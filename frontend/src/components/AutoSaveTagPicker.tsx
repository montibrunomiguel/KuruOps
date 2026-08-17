import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { mutationErrorMessage } from "../api/hooks";
import { TagPicker } from "./TagPicker";

// Auto-saves on every tag change -- shared by the alert and incident detail
// pages' tags rows, previously duplicated as AlertTagsRow/IncidentTagsRow.
// resourcePath is the PUT target (/api/v1/alerts/:id/tags or
// /api/v1/incidents/:id/tags); onSaved lets the parent reload whatever else
// depends on the tag set (e.g. tag-scoped visibility).
export function AutoSaveTagPicker({
  resourcePath,
  value,
  onSaved,
}: {
  resourcePath: string;
  value: string[];
  onSaved: () => void;
}) {
  const { token } = useAuth();
  const { t } = useTranslation();
  const [tags, setTags] = useState<string[]>(value);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const dirty = tags.length !== value.length || tags.some((tg) => !value.includes(tg));

  async function save(next: string[]) {
    setTags(next);
    setSubmitting(true);
    setError(null);
    try {
      await api.put(resourcePath, { tags: next }, token);
      onSaved();
    } catch (err) {
      setTags(value);
      setError(mutationErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div style={{ marginBottom: 16 }}>
      <TagPicker value={tags} onChange={save} disabled={submitting} />
      {dirty && submitting && <span className="helper-text">{t("common.saving")}</span>}
      {error && <div className="error-banner" style={{ marginTop: 8 }}>{error}</div>}
    </div>
  );
}
