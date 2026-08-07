import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../../auth/AuthContext";
import { api } from "../../api/client";
import { mutationErrorMessage } from "../../api/hooks";
import type { MigrationResult, TargetDatabaseConfig } from "../../types/api";

const SSL_MODES = ["disable", "require", "verify-full"];

// Settings -> External Database: a guided, one-time cutover from the
// bundled Postgres container to a customer-supplied external database.
// This does NOT hot-swap anything live -- DATABASE_URL is read once at
// process startup with no reload mechanism anywhere in the backend, so a
// successful migration here still ends with a manual restart step (see
// the connection strings shown below). The credentials entered in this
// form need to be privileged enough to CREATE ROLE/CREATE EXTENSION on the
// target, not the eventual low-privilege app/worker credentials this flow
// generates for you.
export function DatabaseMigrationPanel() {
  const { t } = useTranslation();
  const { token } = useAuth();

  const [host, setHost] = useState("");
  const [port, setPort] = useState("5432");
  const [database, setDatabase] = useState("");
  const [user, setUser] = useState("");
  const [password, setPassword] = useState("");
  const [sslMode, setSslMode] = useState("disable");

  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null);

  const [confirming, setConfirming] = useState(false);
  const [migrating, setMigrating] = useState(false);
  const [migrateError, setMigrateError] = useState<string | null>(null);
  const [result, setResult] = useState<MigrationResult | null>(null);

  function target(): TargetDatabaseConfig {
    return { host, database, user, password, sslMode, port: Number(port) };
  }

  async function handleTestConnection(e: FormEvent) {
    e.preventDefault();
    setTesting(true);
    setTestResult(null);
    try {
      await api.post("/api/v1/settings/database-migration/test-connection", target(), token);
      setTestResult({ ok: true, message: t("settings.databaseMigration.testOk") });
    } catch (err) {
      setTestResult({ ok: false, message: mutationErrorMessage(err) });
    } finally {
      setTesting(false);
    }
  }

  async function handleMigrate() {
    setConfirming(false);
    setMigrating(true);
    setMigrateError(null);
    setResult(null);
    try {
      const r = await api.post<MigrationResult>("/api/v1/settings/database-migration/migrate", target(), token);
      setResult(r);
    } catch (err) {
      setMigrateError(mutationErrorMessage(err));
    } finally {
      setMigrating(false);
    }
  }

  const formDisabled = testing || migrating;

  return (
    <>
      <form onSubmit={handleTestConnection} className="panel">
        <h2 className="panel-title" style={{ marginBottom: 4 }}>
          {t("settings.databaseMigration.title")}
        </h2>
        <p className="helper-text" style={{ marginBottom: 14 }}>
          {t("settings.databaseMigration.helper")}
        </p>

        {testResult && (
          <div
            className={testResult.ok ? "helper-text" : "error-banner"}
            style={testResult.ok ? { color: "var(--success)", marginBottom: 12 } : undefined}
          >
            {testResult.message}
          </div>
        )}

        <div className="form-grid">
          <div className="field">
            <label htmlFor="dbmig-host">{t("settings.databaseMigration.host")}</label>
            <input id="dbmig-host" className="input" value={host} onChange={(e) => setHost(e.target.value)} required disabled={formDisabled} />
          </div>
          <div className="field">
            <label htmlFor="dbmig-port">{t("settings.databaseMigration.port")}</label>
            <input id="dbmig-port" className="input" type="number" value={port} onChange={(e) => setPort(e.target.value)} required disabled={formDisabled} />
          </div>
          <div className="field">
            <label htmlFor="dbmig-database">{t("settings.databaseMigration.database")}</label>
            <input id="dbmig-database" className="input" value={database} onChange={(e) => setDatabase(e.target.value)} required disabled={formDisabled} />
          </div>
          <div className="field">
            <label htmlFor="dbmig-user">
              {t("settings.databaseMigration.user")} <span className="field-hint">{t("settings.databaseMigration.userHint")}</span>
            </label>
            <input id="dbmig-user" className="input" value={user} onChange={(e) => setUser(e.target.value)} required disabled={formDisabled} />
          </div>
          <div className="field">
            <label htmlFor="dbmig-password">{t("settings.databaseMigration.password")}</label>
            <input id="dbmig-password" className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required disabled={formDisabled} />
          </div>
          <div className="field">
            <label htmlFor="dbmig-sslmode">{t("settings.databaseMigration.sslMode")}</label>
            <select id="dbmig-sslmode" className="select" value={sslMode} onChange={(e) => setSslMode(e.target.value)} disabled={formDisabled}>
              {SSL_MODES.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="row-actions">
          <button type="submit" className="btn btn-secondary btn-sm" disabled={formDisabled}>
            {testing ? t("settings.databaseMigration.testing") : t("settings.databaseMigration.testConnection")}
          </button>

          {!confirming ? (
            <button type="button" className="btn btn-danger btn-sm" onClick={() => setConfirming(true)} disabled={formDisabled}>
              {t("settings.databaseMigration.migrateButton")}
            </button>
          ) : (
            <>
              <span className="helper-text">{t("settings.databaseMigration.migrateConfirm")}</span>
              <button type="button" className="btn btn-danger btn-sm" onClick={handleMigrate} disabled={formDisabled}>
                {migrating ? t("settings.databaseMigration.migrating") : t("common.confirm")}
              </button>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setConfirming(false)} disabled={formDisabled}>
                {t("common.cancel")}
              </button>
            </>
          )}
        </div>

        {migrateError && <div className="error-banner" style={{ marginTop: 12 }}>{migrateError}</div>}
      </form>

      {result && <MigrationResultPanel result={result} />}
    </>
  );
}

function MigrationResultPanel({ result }: { result: MigrationResult }) {
  const { t } = useTranslation();
  const tables = Object.entries(result.rowCounts).sort(([a], [b]) => a.localeCompare(b));

  return (
    <div className="panel" style={{ marginTop: 16 }}>
      <h2 className="panel-title" style={{ color: "var(--success)" }}>
        {t("settings.databaseMigration.resultTitle")}
      </h2>
      <p className="helper-text" style={{ marginBottom: 14 }}>
        {t("settings.databaseMigration.resultHelper", { version: result.schemaVersion })}
      </p>

      <table className="table" style={{ marginBottom: 16 }}>
        <thead>
          <tr>
            <th>{t("settings.databaseMigration.table")}</th>
            <th>{t("settings.databaseMigration.rows")}</th>
          </tr>
        </thead>
        <tbody>
          {tables.map(([table, count]) => (
            <tr key={table}>
              <td>{table}</td>
              <td>{count.target}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <div className="error-banner" style={{ marginBottom: 12 }}>
        {t("settings.databaseMigration.restartWarning")}
      </div>

      <div className="field" style={{ marginBottom: 10 }}>
        <label htmlFor="dbmig-app-dsn">{t("settings.databaseMigration.appDsn")}</label>
        <textarea id="dbmig-app-dsn" className="input" readOnly rows={2} value={result.appDsn} onFocus={(e) => e.target.select()} />
      </div>
      <div className="field">
        <label htmlFor="dbmig-worker-dsn">{t("settings.databaseMigration.workerDsn")}</label>
        <textarea id="dbmig-worker-dsn" className="input" readOnly rows={2} value={result.workerDsn} onFocus={(e) => e.target.select()} />
      </div>
    </div>
  );
}
