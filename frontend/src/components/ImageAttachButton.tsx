import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { mutationErrorMessage } from "../api/hooks";
import { PaperclipIcon } from "./icons";

// Shared attach-image control for the three places an analyst can attach a
// screenshot: closing/classifying an alert, and posting a Team Notes
// comment on either an alert or an incident. Uploads immediately on file
// selection (via api.uploadImage) rather than staging the raw File and
// uploading on form submit -- keeps the parent form simple (it only ever
// deals with a URL string) and shows upload errors right where they happen.
export function ImageAttachButton({
  value,
  onChange,
  kind,
  id,
  disabled,
}: {
  value: string | null;
  onChange: (url: string | null) => void;
  // Which alert/incident this evidence belongs to -- the backend needs both
  // to build the storage key and to enforce tag-visibility on the upload.
  kind: "alert" | "incident";
  id: string;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const inputRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
    setUploading(true);
    setError(null);
    try {
      const res = await api.uploadImage(file, kind, id, token);
      onChange(res.url);
    } catch (err) {
      setError(mutationErrorMessage(err));
    } finally {
      setUploading(false);
    }
  }

  if (value) {
    return (
      <div className="token-reveal">
        <img src={value} alt={t("common.attachImage")} style={{ height: 32, borderRadius: 4 }} />
        {!disabled && (
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => onChange(null)}>
            {t("common.remove")}
          </button>
        )}
      </div>
    );
  }

  return (
    <div>
      <input
        ref={inputRef}
        type="file"
        accept="image/png,image/jpeg,image/gif,image/webp"
        style={{ display: "none" }}
        onChange={handleFileChange}
        disabled={disabled || uploading}
      />
      <button
        type="button"
        className="btn btn-sm"
        onClick={() => inputRef.current?.click()}
        disabled={disabled || uploading}
      >
        <PaperclipIcon width={13} height={13} />
        {uploading ? t("common.uploading") : t("common.attachImage")}
      </button>
      {error && <div className="error-banner" style={{ marginTop: 6 }}>{error}</div>}
    </div>
  );
}
