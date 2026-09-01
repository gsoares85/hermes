package conn

import "github.com/gsoares85/hermes/internal/driver"

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

	return target
}
