import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api } from "../../api/client";
import { useList } from "../../api/hooks";
import type { Playbook } from "../../types/playbooks";

export function PlaybooksListPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [search, setSearch] = useState("");

  const { data: playbooks, loading, error } = useList<Playbook>(["playbooks"], (tk) =>
    api.get<Playbook[]>("/api/v1/playbooks", tk),
  );

  const filtered = useMemo(() => {
    if (!playbooks) return [];
    const q = search.trim().toLowerCase();
    if (!q) return playbooks;
    return playbooks.filter(
      (p) =>
        p.title.toLowerCase().includes(q) ||
        p.category.toLowerCase().includes(q) ||
        p.keywords.some((k) => k.toLowerCase().includes(q)),
    );
  }, [playbooks, search]);

  return (
    <div>
      <div className="toolbar">
        <div className="toolbar-title">
          <h1 className="page-title">{t("playbooks.title")}</h1>
          <p className="page-sub" style={{ marginBottom: 0 }}>
            {t("playbooks.subtitle")}
          </p>
        </div>
        <div className="toolbar-actions">
          <button className="btn btn-primary btn-sm" onClick={() => navigate("/playbooks/new")}>
            {t("playbooks.newPlaybook")}
          </button>
        </div>
      </div>

      <div className="filter-bar">
        <input
          className="input input-search"
          placeholder={t("playbooks.searchPlaceholder")}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      <div className="panel">
        {error && <div className="error-banner">{error}</div>}
        {loading && <div className="empty-state">{t("common.loading")}</div>}
        {!loading && filtered.length === 0 && (
          <div className="empty-state">
            <p style={{ margin: "0 0 12px" }}>{t("playbooks.noResults")}</p>
            {!search && (
              <button className="btn btn-primary btn-sm" onClick={() => navigate("/playbooks/new")}>
                {t("playbooks.newPlaybook")}
              </button>
            )}
          </div>
        )}
        {filtered.map((p) => (
          <div className="row" key={p.id} style={{ cursor: "pointer" }} onClick={() => navigate(`/playbooks/${p.id}`)}>
            <div className="row-main">
              <p className="row-title">{p.title}</p>
              <p className="row-sub">
                {p.category}
                {p.keywords.length > 0 ? ` · ${p.keywords.join(", ")}` : ""}
              </p>
            </div>
            <div className="row-actions">
              <span className="badge badge-muted">
                {t("playbooks.phasesCount", { count: Object.keys(p.steps).length })}
              </span>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
