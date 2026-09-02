import { useEffect, useState } from "react";

import {
  applyParsed,
  closeConnection,
  databases as listDatabases,
  emptyForm,
  openConnection,
  parseURI,
  sslModes,
  testConnection,
  type ConnectionForm as Form,
  type DiagnosisView,
  type StatusView,
} from "../../api/connection";

/**
 * Describes a connection, tests it, and opens it.
 *
 * Nothing here builds a connection string or decides what an error means: the
 * form sends fields and renders whatever the Go side answers. That is why a
 * failure arrives as a summary, a cause and a next step rather than as a
 * message this component would have to interpret.
 */
export function ConnectionForm(): React.JSX.Element {
  const [form, setForm] = useState<Form>(emptyForm);
  const [uri, setUri] = useState("");
  const [modes, setModes] = useState<string[]>([]);
  const [diagnosis, setDiagnosis] = useState<DiagnosisView | null>(null);
  const [status, setStatus] = useState<StatusView | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [available, setAvailable] = useState<string[]>([]);

  useEffect(() => {
    let active = true;

    sslModes()
      .then((loaded): void => {
        if (active) {
          setModes(loaded);
        }
      })
      .catch((): void => {
        // Running outside the desktop shell. The field falls back to a plain
        // text input rather than the window failing to draw.
      });

    return (): void => {
      active = false;
    };
  }, []);

  function update<K extends keyof Form>(field: K, value: Form[K]): void {
    setForm((current): Form => ({ ...current, [field]: value }));
  }

  async function onPaste(): Promise<void> {
    setNotice("");
    try {
      const parsed = await parseURI(uri);
      setForm((current): Form => applyParsed(current, parsed));
      setNotice(
        parsed.hasPassword
          ? "The URI carried a password. Type it below — it is not copied into the form."
          : "The URI filled the fields below.",
      );
    } catch {
      setNotice("That is not a postgres:// connection URI.");
    }
  }

  async function onTest(): Promise<void> {
    setBusy(true);
    setDiagnosis(null);
    try {
      setDiagnosis(await testConnection(form));
    } catch (err) {
      setNotice(String(err));
    } finally {
      setBusy(false);
    }
  }

  async function onOpen(): Promise<void> {
    setBusy(true);
    try {
      const opened = await openConnection(form);
      setStatus(opened);
      setDiagnosis(null);
      // The connection is open and the password has been used. Keeping it in
      // the state of a page that is redrawn and inspected buys nothing, and
      // clearing it here rather than after the listing below means a failure
      // to list does not leave the secret behind on an open connection.
      update("password", "");
      // Asked for straight away: someone who connected without naming a
      // database did it precisely to find out what is there.
      setAvailable(await listDatabases(opened.id));
    } catch (err) {
      setNotice(String(err));
    } finally {
      setBusy(false);
    }
  }

  async function onClose(): Promise<void> {
    if (status === null) {
      return;
    }
    try {
      await closeConnection(status.id);
    } catch (err) {
      setNotice(String(err));
    }
    setStatus(null);
    setAvailable([]);
  }

  return (
    <section className="connection">
      <h2>Connect</h2>

      <div className="connection__paste">
        <label htmlFor="uri">Paste a connection URI</label>
        <div className="connection__row">
          <input
            id="uri"
            value={uri}
            placeholder="postgres://user@host:5432/database"
            onChange={(event): void => {
              setUri(event.target.value);
            }}
          />
          <button type="button" onClick={(): void => void onPaste()} disabled={uri === ""}>
            Fill the form
          </button>
        </div>
        {notice !== "" && <p className="connection__notice">{notice}</p>}
      </div>

      <div className="connection__grid">
        <Field
          label="Name"
          value={form.name}
          onChange={(v): void => {
            update("name", v);
          }}
        />
        <Field
          label="Host"
          value={form.host}
          onChange={(v): void => {
            update("host", v);
          }}
        />
        <Field
          label="Port"
          value={String(form.port)}
          onChange={(v): void => {
            update("port", Number(v) || 0);
          }}
        />
        <Field
          label="Database (optional)"
          value={form.database}
          onChange={(v): void => {
            update("database", v);
          }}
        />
        <Field
          label="User"
          value={form.user}
          onChange={(v): void => {
            update("user", v);
          }}
        />
        <Field
          label="Password"
          value={form.password}
          type="password"
          onChange={(v): void => {
            update("password", v);
          }}
        />

        <label className="connection__field">
          <span>SSL mode</span>
          {modes.length > 0 ? (
            <select
              value={form.sslMode}
              onChange={(event): void => {
                update("sslMode", event.target.value);
              }}
            >
              {modes.map((mode): React.JSX.Element => (
                <option key={mode} value={mode}>
                  {mode}
                </option>
              ))}
            </select>
          ) : (
            <input
              value={form.sslMode}
              onChange={(event): void => {
                update("sslMode", event.target.value);
              }}
            />
          )}
        </label>
      </div>

      <div className="connection__actions">
        <button type="button" onClick={(): void => void onTest()} disabled={busy}>
          {busy ? "Working…" : "Test connection"}
        </button>
        <button type="button" onClick={(): void => void onOpen()} disabled={busy}>
          Connect
        </button>
        {status !== null && (
          <button type="button" onClick={(): void => void onClose()}>
            Disconnect
          </button>
        )}
      </div>

      {status !== null && <StateIndicator status={status} />}
      {available.length > 0 && (
        <DatabaseList
          databases={available}
          selected={form.database}
          onSelect={(name): void => {
            update("database", name);
          }}
        />
      )}
      {diagnosis !== null && <DiagnosisPanel diagnosis={diagnosis} />}
    </section>
  );
}

function Field(props: {
  label: string;
  value: string;
  type?: string;
  onChange: (value: string) => void;
}): React.JSX.Element {
  return (
    <label className="connection__field">
      <span>{props.label}</span>
      <input
        type={props.type ?? "text"}
        value={props.value}
        onChange={(event): void => {
          props.onChange(event.target.value);
        }}
      />
    </label>
  );
}

/**
 * What this connection can reach.
 *
 * Shown after connecting rather than before, because it is the server that
 * knows: the list is what this role may actually open, not every name that
 * exists.
 */
function DatabaseList(props: {
  databases: string[];
  selected: string;
  onSelect: (name: string) => void;
}): React.JSX.Element {
  return (
    <div className="connection__databases">
      <p>Databases you can open</p>
      <ul>
        {props.databases.map((name): React.JSX.Element => (
          <li key={name}>
            <button
              type="button"
              className={name === props.selected ? "is-selected" : ""}
              onClick={(): void => {
                props.onSelect(name);
              }}
            >
              {name}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** The state indicator CON-17 asks for: what the connection is doing, now. */
function StateIndicator({ status }: { status: StatusView }): React.JSX.Element {
  return (
    <p className={`connection__state connection__state--${status.state}`}>
      <span className="connection__dot" aria-hidden="true" />
      {status.state}
      {status.diagnosis.failed && ` — ${status.diagnosis.summary}`}
    </p>
  );
}

/**
 * A failure, as three separate things. Showing only the summary would leave the
 * reader knowing what happened and not what to do, which is the state this
 * whole layer exists to avoid.
 */
function DiagnosisPanel({ diagnosis }: { diagnosis: DiagnosisView }): React.JSX.Element {
  if (!diagnosis.failed) {
    return <p className="connection__ok">The connection works.</p>;
  }

  return (
    <div className="connection__diagnosis">
      <p className="connection__summary">{diagnosis.summary}</p>
      <p className="connection__cause">{diagnosis.cause}</p>
      <p className="connection__next">{diagnosis.nextStep}</p>
      {diagnosis.detail !== "" && (
        <details>
          <summary>Driver detail</summary>
          <pre>{diagnosis.detail}</pre>
        </details>
      )}
    </div>
  );
}
