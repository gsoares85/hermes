import { ConnectionService } from "../../bindings/github.com/gsoares85/hermes/internal/ui";
import type {
  ConnectionForm,
  ConnectionView,
  DiagnosisView,
  SavedView,
  StatusView,
  VaultView,
} from "../../bindings/github.com/gsoares85/hermes/internal/ui/models";

export type { ConnectionForm, ConnectionView, DiagnosisView, SavedView, StatusView, VaultView };

/**
 * An empty form.
 *
 * The port is the PostgreSQL default rather than zero, so the field shows the
 * value that will be used instead of leaving the reader to know it.
 */
export const emptyForm: ConnectionForm = {
  // Empty until the connection has been saved once. It is what tells Save to
  // replace a connection rather than add another one, and what the password is
  // filed under in the keychain.
  id: "",
  name: "",
  // Session settings go to the server; connection settings configure the
  // client. Kept apart because sending one as the other loses it — and for
  // the ones that pin the authentication method, removes the protection.
  params: null,
  options: null,
  host: "localhost",
  // Left empty on purpose: connecting without naming a database opens the
  // maintenance database, and the list of what is there comes back.
  port: 5432,
  database: "",
  user: "",
  password: "",
  sslMode: "prefer",
  rootCert: "",
  cert: "",
  key: "",
};

/**
 * Fills a form from a pasted connection URI.
 *
 * The Go side does the parsing: the rules for what a connection URI means
 * belong where they are tested, not in the window. It deliberately does not
 * return the password, so a pasted URI leaves the password field to be typed —
 * `hasPassword` is how the form knows to ask.
 */
export async function parseURI(uri: string): Promise<ConnectionView> {
  return await ConnectionService.Parse(uri);
}

/** Tries the connection and explains the result, success or failure. */
export async function testConnection(form: ConnectionForm): Promise<DiagnosisView> {
  return await ConnectionService.Test(form);
}

/** Connects and keeps the connection, returning its state. */
export async function openConnection(form: ConnectionForm): Promise<StatusView> {
  return await ConnectionService.Open(form);
}

/** Reads the last known state without contacting the server. */
export async function connectionStatus(id: string): Promise<StatusView> {
  return await ConnectionService.Status(id);
}

/** Releases a connection. */
export async function closeConnection(id: string): Promise<void> {
  await ConnectionService.Close(id);
}

/**
 * The databases this connection may open.
 *
 * It is how someone who connected without naming one chooses: the server
 * answers with what this role may actually reach, so the list is not a set of
 * names most of which would be refused.
 */
export async function databases(id: string): Promise<string[]> {
  return (await ConnectionService.Databases(id)) ?? [];
}

/**
 * The sslmode values the driver accepts, listed by the Go side.
 *
 * The binding types the result as nullable because a nil slice in Go arrives as
 * null, and strict mode is right to insist the difference be handled: an empty
 * list is what the form should fall back to, not a crash.
 */
export async function sslModes(): Promise<string[]> {
  return (await ConnectionService.SSLModes()) ?? [];
}

/**
 * Saves the connection and its password.
 *
 * The two go to different places — the connection to a file, the password to
 * the keychain of the system — and the Go side is what knows that. Nothing
 * comes back carrying the password: the returned view has no field for one.
 */
export async function saveConnection(form: ConnectionForm): Promise<SavedView> {
  return await ConnectionService.Save(form);
}

/**
 * The connections that have been saved.
 *
 * It deliberately says nothing about which of them have a stored password.
 * Answering that would mean reading the keychain once per row, which on macOS
 * and on Linux can raise an authorisation dialog per connection for someone who
 * only wanted to see a list.
 */
export async function savedConnections(): Promise<SavedView[]> {
  return (await ConnectionService.List()) ?? [];
}

/** Removes a connection and the password that belonged to it. */
export async function deleteConnection(id: string): Promise<void> {
  await ConnectionService.Delete(id);
}

/**
 * Where passwords are being kept.
 *
 * The warning is empty when the keychain of the system is in use. When it is
 * not, it says so in words the person can act on — which is the difference
 * between a fallback and an application that silently forgets.
 */
export async function vaultStatus(): Promise<VaultView> {
  return await ConnectionService.VaultStatus();
}

/**
 * Fills the form from a saved connection.
 *
 * The password is left empty, and that is not an omission: the Go side reads it
 * from the keychain when the connection is opened, so it never has to travel
 * out here to come straight back.
 */
export function formFromSaved(saved: SavedView): ConnectionForm {
  return {
    id: saved.id,
    name: saved.name,
    params: saved.params,
    options: saved.options,
    host: saved.host,
    port: saved.port,
    database: saved.database,
    user: saved.user,
    sslMode: saved.sslMode,
    rootCert: saved.rootCert,
    cert: saved.cert,
    key: saved.key,
    password: "",
  };
}

/** Applies a parsed URI to a form, keeping whatever the URI did not carry. */
export function applyParsed(form: ConnectionForm, parsed: ConnectionView): ConnectionForm {
  return {
    ...form,
    name: parsed.name || form.name,
    host: parsed.host,
    port: parsed.port,
    database: parsed.database,
    user: parsed.user,
    sslMode: parsed.sslMode || form.sslMode,
    // Carried through rather than dropped: a pasted URI that sets
    // application_name or connect_timeout meant it.
    params: parsed.params,
    options: parsed.options,
    rootCert: parsed.rootCert,
    cert: parsed.cert,
    key: parsed.key,
    // Never filled from the URI: the Go side does not send it back.
    password: "",
  };
}
