import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { mutationErrorMessage } from "../api/hooks";
import { PaperclipIcon, FileIcon } from "./icons";

const IMAGE_EXTENSIONS = new Set(["png", "jpg", "jpeg", "gif", "webp"]);

// The backend stores an attachment's leaf key segment as
// "<uuid>_<slugified original filename>.<ext>" (see
// UploadHandlers.attachmentFilename on the Go side) -- everything after the
// first "_" is the part worth showing to a human. Exported so comment lists
// (IncidentDetailPage/AlertDetailPage) can reuse the exact same logic.
export function attachmentDisplayName(url: string): string {
  const leaf = url.split("/").pop() ?? url;
  const idx = leaf.indexOf("_");
  return idx !== -1 && idx + 1 < leaf.length ? leaf.slice(idx + 1) : leaf;
}

export function isImageAttachment(url: string): boolean {
  const ext = url.split(".").pop()?.toLowerCase() ?? "";
  return IMAGE_EXTENSIONS.has(ext);
}

// Renders a previously-uploaded attachment inline in a comment/close-note --
// an image thumbnail (click to open full-size in a new tab) or a download
// chip for anything else. Shared by AlertDetailPage and IncidentDetailPage's
// Team Notes lists and AlertDetailPage's close-comment display.
export function AttachmentPreview({ url }: { url: string }) {
  if (isImageAttachment(url)) {
    return (
      <a href={url} target="_blank" rel="noreferrer" style={{ display: "inline-block", marginTop: 6 }}>
        <img src={url} alt={attachmentDisplayName(url)} style={{ maxHeight: 160, borderRadius: 6, display: "block" }} />
      </a>
    );
  }
  return (
    <a
      href={url}
      download
      className="btn btn-ghost btn-sm"
      style={{ display: "inline-flex", alignItems: "center", gap: 4, marginTop: 6 }}
    >
      <FileIcon width={14} height={14} />
      {attachmentDisplayName(url)}
    </a>
  );
}

// Shared attach-file control for the three places an analyst can attach
// evidence: closing/classifying an alert, and posting a Team Notes comment
// on either an alert or an incident. Uploads immediately on file selection
// (via api.uploadAttachment) rather than staging the raw File and uploading
// on form submit -- keeps the parent form simple (it only ever deals with a
// URL string) and shows upload errors right where they happen. Any file
// type can be picked here -- the backend is the real gate (see
// UploadHandlers.resolveAttachmentExt).
export function AttachmentButton({
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
      const res = await api.uploadAttachment(file, kind, id, token);
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
        {isImageAttachment(value) ? (
          <img src={value} alt={t("common.attachFile")} style={{ height: 32, borderRadius: 4 }} />
        ) : (
          <span style={{ display: "inline-flex", alignItems: "center", gap: 4, fontSize: 13 }}>
            <FileIcon width={14} height={14} />
            {attachmentDisplayName(value)}
          </span>
        )}
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
        {uploading ? t("common.uploading") : t("common.attachFile")}
      </button>
      {error && <div className="error-banner" style={{ marginTop: 6 }}>{error}</div>}
    </div>
  );
}
