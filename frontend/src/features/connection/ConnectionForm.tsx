import { useEffect, useState } from "react";

import {
  applyParsed,
  closeConnection,
  databases as listDatabases,
  deleteConnection,
  emptyForm,
  formFromSaved,
  openConnection,
  parseURI,
  saveConnection,
  savedConnections,
  sslModes,
  testConnection,
  vaultStatus,
  type ConnectionForm as Form,
  type DiagnosisView,
  type SavedView,
  type StatusView,
  type VaultView,
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
  const [saved, setSaved] = useState<SavedView[]>([]);
  const [vault, setVault] = useState<VaultView | null>(null);

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

    // Asked once, at startup, and never again: the answer cannot change while
    // the application runs, and asking on every redraw would be a round trip
    // to a keychain for something already known.
    vaultStatus()
      .then((status): void => {
        if (active) {
          setVault(status);
        }
      })
      .catch((): void => {
        // Outside the desktop shell there is no vault to report on, and a
        // missing banner is better than a window that does not draw.
      });

    void refreshSaved();

    return (): void => {
      active = false;
    };
  }, []);

  async function refreshSaved(): Promise<void> {
    try {
      setSaved(await savedConnections());
    } catch (err) {
      // A file someone broke by hand is reported rather than swallowed: an
      // empty list would look exactly like never having saved anything.
      setNotice(String(err));
    }
  }

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

  async function onSave(): Promise<void> {
    setBusy(true);
    try {
      const stored = await saveConnection(form);
      // The identifier comes back on a connection that had none, and keeping it
      // is what makes the next save an edit instead of a second copy.
      update("id", stored.id);
      // The password is in the keychain now. Leaving it in the state of a page
      // that is redrawn and inspected buys nothing.
      update("password", "");
      setNotice(`Saved “${stored.name === "" ? stored.host : stored.name}”.`);
    } catch (err) {
      setNotice(String(err));
    } finally {
      await refreshSaved();
      setBusy(false);
    }
  }

  async function onForget(id: string): Promise<void> {
    setBusy(true);
    try {
      await deleteConnection(id);
      if (form.id === id) {
        setForm(emptyForm);
      }
      setNotice("The connection and its password were removed.");
    } catch (err) {
      setNotice(String(err));
    } finally {
      await refreshSaved();
      setBusy(false);
    }
  }

  function onLoad(connection: SavedView): void {
    setForm(formFromSaved(connection));
    setDiagnosis(null);
    setNotice("Loaded. The password comes from the keychain when you connect.");
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

      {vault !== null && <VaultBanner vault={vault} />}

      {saved.length > 0 && (
        <SavedConnections
          connections={saved}
          current={form.id}
          busy={busy}
          onLoad={onLoad}
          onForget={(id): void => void onForget(id)}
        />
      )}

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
        {/*
          Disabled while a connection is open, because the id of the open one
          lives in this state and connecting again would overwrite it: the
          first pool would stay open on the other side with nothing left here
          able to close it. Disconnect first.
        */}
        <button
          type="button"
          onClick={(): void => void onOpen()}
          disabled={busy || status !== null}
        >
          Connect
        </button>
        {status !== null && (
          <button type="button" onClick={(): void => void onClose()}>
            Disconnect
          </button>
        )}
        <button type="button" onClick={(): void => void onSave()} disabled={busy}>
          {form.id === "" ? "Save connection" : "Save changes"}
        </button>
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

/**
 * Where passwords are being kept, and a warning when the answer is "nowhere
 * that outlives this window".
 *
 * Drawn only when there is something to say. A machine with a working keyring
 * gets no banner, because a banner that is always there is a banner nobody
 * reads on the day it matters.
 */
function VaultBanner({ vault }: { vault: VaultView }): React.JSX.Element | null {
  if (vault.warning === "") {
    return null;
  }

  return (
    <p className="connection__vault" role="status">
      {vault.warning}
    </p>
  );
}

/**
 * The connections that have been saved.
 *
 * Loading one fills the form and leaves the password field empty: the Go side
 * reads it from the keychain at the moment it connects, so it never travels out
 * here. Forgetting one takes its password with it.
 */
function SavedConnections(props: {
  connections: SavedView[];
  current: string;
  busy: boolean;
  onLoad: (connection: SavedView) => void;
  onForget: (id: string) => void;
}): React.JSX.Element {
  return (
    <div className="connection__saved">
      <p>Saved connections</p>
      <ul>
        {props.connections.map((connection): React.JSX.Element => (
          <li key={connection.id}>
            <button
              type="button"
              className={connection.id === props.current ? "is-selected" : ""}
              onClick={(): void => {
                props.onLoad(connection);
              }}
            >
              <span className="connection__saved-name">
                {connection.name === "" ? connection.host : connection.name}
              </span>
              <span className="connection__saved-target">
                {connection.user}@{connection.host}:{connection.port}
                {connection.database === "" ? "" : `/${connection.database}`}
              </span>
            </button>
            <button
              type="button"
              className="connection__forget"
              disabled={props.busy}
              aria-label={`Forget ${connection.name === "" ? connection.host : connection.name}`}
              onClick={(): void => {
                props.onForget(connection.id);
              }}
            >
              Forget
            </button>
          </li>
        ))}
      </ul>
    </div>
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
