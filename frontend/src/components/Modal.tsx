import { useEffect, useRef, type CSSProperties, type FormEvent, type KeyboardEvent, type ReactNode } from "react";

// Shared modal-overlay/modal shell -- extracted from 7 previously-independent
// copies of the exact same markup (CloseAlertModal, AnalysisChat,
// IncidentsListPage's CreateIncidentForm, PlaybookViewModal, OnCallTimeline's
// override popover, WebhooksPanel's Create/Edit modals), none of which had
// role="dialog"/aria-modal, Escape-to-close, or any focus management. Modeled
// on CommandPalette.tsx's dialog (role="dialog"/aria-modal="true", Escape
// scoped via onKeyDown rather than a global listener so a stacked modal only
// ever closes the one that actually has focus), generalized to a real focus
// trap since callers here have several real tab stops, not CommandPalette's
// single combobox input.
const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

interface ModalProps {
  onClose: () => void;
  children: ReactNode;
  // Most callers just need "modal" (see components.css); a few add their own
  // sizing/layout on top (AnalysisChat's taller flex column, OnCallTimeline's
  // narrower popover) -- appended alongside "modal" rather than replacing it.
  className?: string;
  style?: CSSProperties;
  // CloseAlertModal/CreateIncidentForm/CreateWebhookModal/EditWebhookModal
  // render the panel itself as a <form onSubmit=...> (the submit button
  // lives inside it) -- Modal owns the overlay/dialog/focus-trap machinery
  // either way, but the panel element itself has to actually be a <form> for
  // the browser's own submit-on-Enter behavior to keep working.
  as?: "div" | "form";
  onSubmit?: (e: FormEvent) => void;
}

export function Modal({ onClose, children, className, style, as = "div", onSubmit }: ModalProps) {
  const panelRef = useRef<HTMLElement | null>(null);

  function setPanelRef(el: HTMLElement | null) {
    panelRef.current = el;
  }

  // Runs once per mount (a modal is always freshly mounted when it opens --
  // every call site conditionally renders it, never toggles a hidden prop):
  // capture whatever had focus so it can be restored on close, then move
  // focus into the dialog itself (its first focusable element, or the panel
  // as a fallback for a modal with none, e.g. one still loading its content).
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null;
    const panel = panelRef.current;
    const focusable = panel?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR);
    (focusable && focusable.length > 0 ? focusable[0] : panel)?.focus();
    return () => {
      previouslyFocused?.focus();
    };
  }, []);

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === "Escape") {
      // Stopped so a modal opened from inside another dialog-ish surface
      // (e.g. a keyboard shortcut handler higher up) only ever closes this
      // one, not something further up the tree too.
      e.stopPropagation();
      onClose();
      return;
    }
    if (e.key !== "Tab") return;
    const panel = panelRef.current;
    if (!panel) return;
    const focusable = Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
    if (focusable.length === 0) {
      e.preventDefault();
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  }

  const panelClassName = className ? `modal ${className}` : "modal";

  return (
    <div className="modal-overlay" onClick={onClose} onKeyDown={handleKeyDown}>
      {as === "form" ? (
        <form
          ref={setPanelRef}
          role="dialog"
          aria-modal="true"
          className={panelClassName}
          style={style}
          onClick={(e) => e.stopPropagation()}
          onSubmit={onSubmit}
        >
          {children}
        </form>
      ) : (
        <div
          ref={setPanelRef}
          role="dialog"
          aria-modal="true"
          className={panelClassName}
          style={style}
          onClick={(e) => e.stopPropagation()}
          tabIndex={-1}
        >
          {children}
        </div>
      )}
    </div>
  );
}
