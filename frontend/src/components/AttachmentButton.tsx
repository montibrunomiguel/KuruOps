import { useEffect, useRef, useState } from "react";
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

// Triggers a real browser "Save As" for blob using name -- same
// createObjectURL + programmatic-click + revoke pattern already used by
// Settings -> Audit Export (see AuditExportPanel.exportCEF), now shared
// with attachment downloads below.
function saveBlob(blob: Blob, name: string) {
  const objectUrl = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = objectUrl;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(objectUrl);
}

// GET /api/v1/uploads/images/* requires the same Authorization bearer token
// every other /api/v1 route does (see router.go) -- a plain <img src> or
// <a href> can never send a custom header, so pointing either directly at
// the backend URL always 401s (a broken-image icon in place of a thumbnail,
// and a failed navigation instead of a download). This fetches the bytes
// with the token attached and exposes them as a blob: URL the browser CAN
// load directly, revoking it on unmount/url change so blobs don't pile up
// as an analyst scrolls through a long comment thread. Only meant for
// images -- see AttachmentPreview's non-image branch for why other
// attachment types are fetched lazily on click instead.
function useAttachmentBlob(url: string | null, token: string | null) {
  const [state, setState] = useState<{ blobUrl: string | null; blob: Blob | null; failed: boolean }>({
    blobUrl: null,
    blob: null,
    failed: false,
  });

  useEffect(() => {
    if (!url) {
      setState({ blobUrl: null, blob: null, failed: false });
      return;
    }
    let cancelled = false;
    let created: string | null = null;
    setState({ blobUrl: null, blob: null, failed: false });
    api
      .downloadFile(url, token)
      .then(({ blob }) => {
        if (cancelled) return;
        created = URL.createObjectURL(blob);
        setState({ blobUrl: created, blob, failed: false });
      })
      .catch(() => {
        if (!cancelled) setState({ blobUrl: null, blob: null, failed: true });
      });
    return () => {
      cancelled = true;
      if (created) URL.revokeObjectURL(created);
    };
  }, [url, token]);

  return state;
}

// Renders a previously-uploaded attachment inline in a comment/close-note.
// Images get an authenticated inline thumbnail (see useAttachmentBlob);
// everything else gets a filename chip. Clicking either downloads the file
// -- non-image bytes are only fetched lazily, on click, so opening a
// comment thread with several pcap/zip/log attachments doesn't eagerly
// download all of them just to render filename chips. Shared by
// AlertDetailPage and IncidentDetailPage's Team Notes lists and
// AlertDetailPage's close-comment display.
export function AttachmentPreview({ url }: { url: string }) {
  const { t } = useTranslation();
  const { token } = useAuth();
  const isImage = isImageAttachment(url);
  const { blobUrl, blob, failed } = useAttachmentBlob(isImage ? url : null, token);
  const [downloading, setDownloading] = useState(false);
  const name = attachmentDisplayName(url);

  async function handleClick() {
    setDownloading(true);
    try {
      const fileBlob = blob ?? (await api.downloadFile(url, token)).blob;
      saveBlob(fileBlob, name);
    } catch {
      // Best-effort -- a failed one-off download click isn't worth a
      // dedicated error-banner UI here, unlike a form submission.
    } finally {
      setDownloading(false);
    }
  }

  if (isImage) {
    return (
      <button
        type="button"
        onClick={handleClick}
        disabled={downloading}
        aria-label={name}
        style={{ display: "block", marginTop: 6, padding: 0, border: "none", background: "none", cursor: "pointer" }}
      >
        {blobUrl ? (
          <img src={blobUrl} alt={name} style={{ maxHeight: 160, borderRadius: 6, display: "block" }} />
        ) : failed ? (
          <span className="btn btn-ghost btn-sm" style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
            <FileIcon width={14} height={14} />
            {name}
          </span>
        ) : (
          <span className="helper-text">{t("common.loading")}</span>
        )}
      </button>
    );
  }

  return (
    <button
      type="button"
      onClick={handleClick}
      disabled={downloading}
      className="btn btn-ghost btn-sm"
      style={{ display: "inline-flex", alignItems: "center", gap: 4, marginTop: 6 }}
    >
      <FileIcon width={14} height={14} />
      {name}
    </button>
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
  // Staged/just-uploaded preview -- same 401-on-plain-<img-src> problem
  // AttachmentPreview solves, see useAttachmentBlob's doc comment.
  const isImageValue = !!value && isImageAttachment(value);
  const { blobUrl } = useAttachmentBlob(isImageValue ? value : null, token);

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
        {isImageValue ? (
          blobUrl ? (
            <img src={blobUrl} alt={t("common.attachFile")} style={{ height: 32, borderRadius: 4 }} />
          ) : (
            <span className="helper-text">{t("common.loading")}</span>
          )
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
