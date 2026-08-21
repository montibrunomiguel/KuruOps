import { useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../api/client";
import { useList } from "../api/hooks";
import { LiveRegion } from "./LiveRegion";
import type { Tag } from "../types/tags";

// Sources its options from the Settings-managed tag catalog (GET
// /api/v1/tags) instead of accepting free text -- an analyst can only
// attach a tag that an admin has already created in Settings > Tags. See
// TagsPanel and TagService.FilterKnown on the backend, which enforces the
// same rule server-side regardless of what this component lets through.
export function TagPicker({
  value,
  onChange,
  disabled,
}: {
  value: string[];
  onChange: (next: string[]) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const { data: catalog } = useList<Tag>(["tags"], (tk) => api.get<Tag[]>("/api/v1/tags", tk));
  const available = (catalog ?? []).filter((tag) => !value.includes(tag.name));
  // Visually the chip list already updates the moment value/onChange change
  // -- this is purely for screen-reader users, who get no other signal that
  // anything happened (see LiveRegion's doc comment).
  const [announcement, setAnnouncement] = useState("");

  function remove(name: string) {
    onChange(value.filter((v) => v !== name));
    setAnnouncement(t("common.tagRemoved", { name }));
  }

  function add(name: string) {
    onChange([...value, name]);
    setAnnouncement(t("common.tagAdded", { name }));
  }

  return (
    <div>
      <LiveRegion message={announcement} />
      <div className="tag-picker" role="group" aria-label={t("common.tagPickerGroupLabel")}>
        {value.map((name) => (
          <span className="tag-chip" key={name}>
            {name}
            {!disabled && (
              <button type="button" onClick={() => remove(name)} aria-label={t("common.removeTag", { name })}>
                ×
              </button>
            )}
          </span>
        ))}
        {!disabled && available.length > 0 && (
          <select
            className="tag-picker-add"
            value=""
            aria-label={t("common.addTagOption")}
            onChange={(e) => {
              if (e.target.value) add(e.target.value);
            }}
          >
            <option value="">{t("common.addTagOption")}</option>
            {available.map((tag) => (
              <option key={tag.id} value={tag.name}>
                {tag.name}
              </option>
            ))}
          </select>
        )}
      </div>
      {!disabled && (catalog ?? []).length === 0 && (
        <p className="helper-text" style={{ marginTop: 6 }}>{t("common.noTagsCatalog")}</p>
      )}
    </div>
  );
}
