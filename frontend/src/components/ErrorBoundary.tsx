import { Component, type ErrorInfo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

// A plain white/blank screen on any uncaught render exception (the default
// with no boundary) gives an analyst no signal that anything happened, let
// alone what to do about it -- this catches it and offers a reload instead.
// Class component because componentDidCatch has no hook equivalent; the
// actual fallback markup is a separate functional component below so it can
// still use useTranslation.
export class ErrorBoundary extends Component<{ children: ReactNode }, { hasError: boolean }> {
  constructor(props: { children: ReactNode }) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError() {
    return { hasError: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // No error-reporting backend wired up yet -- this is the only trace available.
    console.error("Uncaught render error", error, info.componentStack);
  }

  render() {
    if (this.state.hasError) return <ErrorFallback />;
    return this.props.children;
  }
}

function ErrorFallback() {
  const { t } = useTranslation();
  return (
    <div className="app-shell" style={{ alignItems: "center", justifyContent: "center", display: "flex" }}>
      <div className="panel" style={{ maxWidth: 420, textAlign: "center" }}>
        <h1 className="page-title">{t("errorBoundary.title")}</h1>
        <p className="page-sub">{t("errorBoundary.message")}</p>
        <button className="btn btn-primary" onClick={() => window.location.reload()}>
          {t("errorBoundary.reload")}
        </button>
      </div>
    </div>
  );
}
