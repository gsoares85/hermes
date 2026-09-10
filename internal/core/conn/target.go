package conn

import "github.com/gsoares85/hermes/internal/driver"

// readOnlyParam is the session parameter that makes the server refuse writes.
//
// It is where the read-only mark actually lives. The alternative — deciding in
// the client whether a statement writes — needs a SQL parser and is wrong about
// the first function that writes inside itself, so the mark is stated once on
// connect and enforced by the only thing that can enforce it.
const readOnlyParam = "default_transaction_read_only"

// Target reduces a saved connection to what the engine needs to reach a server.
//
// The conversion is where the product layer stops and the driver seam begins:
// Name and Archived describe how a person organises their connections and stay
// on this side of it. The parameter map is copied for the same reason Clone
// exists — the caller keeps editing the form, and a live connection must not
// change under it.
func (c Config) Target() driver.Target {
	target := driver.Target{
		Host:     c.Host,
		Port:     c.Port,
		Database: c.EffectiveDatabase(),
		User:     c.User,
		Password: c.Password,
		SSLMode:  string(c.TLS.Mode),
		RootCert: c.TLS.RootCert,
		Cert:     c.TLS.Cert,
		Key:      c.TLS.Key,
	}

	target.Params = copyOf(c.Params)
	target.Options = copyOf(c.Options)

	// After the copy, so the mark never reaches the configuration the caller
	// keeps, and over the top of whatever was there, so it cannot be turned off
	// by a session parameter further down the same form. An unmarked connection
	// says nothing at all rather than saying off: a server, a database or a role
	// deliberately left read-only is not something Hermes should override.
	if c.ReadOnly {
		if target.Params == nil {
			target.Params = make(map[string]string, 1)
		}
		target.Params[readOnlyParam] = "on"
	}

	return target
}
