import { ConnectionService } from "../../bindings/github.com/gsoares85/hermes/internal/ui";
import type {
  ConnectionForm,
  ConnectionView,
  DiagnosisView,
  StatusView,
} from "../../bindings/github.com/gsoares85/hermes/internal/ui/models";

export type { ConnectionForm, ConnectionView, DiagnosisView, StatusView };

/**
 * An empty form.
 *
 * The port is the PostgreSQL default rather than zero, so the field shows the
 * value that will be used instead of leaving the reader to know it.
 */
export const emptyForm: ConnectionForm = {
  name: "",
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
    rootCert: parsed.rootCert,
    cert: parsed.cert,
    key: parsed.key,
    // Never filled from the URI: the Go side does not send it back.
    password: "",
  };
}
