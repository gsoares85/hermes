package conn

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ErrInvalidURI is returned for a connection string that cannot be read.
var ErrInvalidURI = errors.New("invalid connection URI")

// Schemes libpq accepts for a connection URI.
var uriSchemes = map[string]bool{"postgres": true, "postgresql": true}

// Query keys that configure transport security rather than the session.
const (
	keySSLMode  = "sslmode"
	keyRootCert = "sslrootcert"
	keyCert     = "sslcert"
	keyKey      = "sslkey"
	keyUser     = "user"
	keyPassword = "password"
)

// ParseURI reads a postgres:// connection string into a Config, so that pasting
// one fills the form.
//
// Every error it returns is deliberately written without echoing the input: the
// URI carries the password, and an error is exactly the text that ends up in a
// log or on screen.
func ParseURI(raw string) (Config, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Config{}, fmt.Errorf("%w: empty", ErrInvalidURI)
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return Config{}, fmt.Errorf("%w: not a URI", ErrInvalidURI)
	}
	if !uriSchemes[parsed.Scheme] {
		return Config{}, fmt.Errorf("%w: scheme %q is not postgres or postgresql", ErrInvalidURI, parsed.Scheme)
	}

	host, port, err := parseHost(parsed)
	if err != nil {
		return Config{}, err
	}

	database := strings.TrimPrefix(parsed.Path, "/")
	if database == "" {
		return Config{}, fmt.Errorf("%w: no database in the path", ErrInvalidURI)
	}

	config := Config{Host: host, Port: port, Database: database}
	readUserInfo(parsed, &config)

	if err := readQuery(parsed.Query(), &config); err != nil {
		return Config{}, err
	}

	return config, nil
}

// parseHost applies the libpq defaults for an absent host and port. A host list
// is refused rather than silently reduced to its first entry: failover is a
// feature the model does not carry yet, and picking one host quietly would be a
// surprise the user cannot see.
func parseHost(parsed *url.URL) (string, int, error) {
	if strings.Contains(parsed.Host, ",") {
		return "", 0, fmt.Errorf("%w: several hosts are not supported yet", ErrInvalidURI)
	}

	host := parsed.Hostname()
	if host == "" {
		host = "localhost"
	}

	raw := parsed.Port()
	if raw == "" {
		return host, DefaultPort, nil
	}

	port, err := strconv.Atoi(raw)
	if err != nil {
		return "", 0, fmt.Errorf("%w: port is not a number", ErrInvalidURI)
	}
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("%w: port %d is outside 1-65535", ErrInvalidURI, port)
	}

	return host, port, nil
}

func readUserInfo(parsed *url.URL, config *Config) {
	if parsed.User == nil {
		return
	}

	config.User = parsed.User.Username()
	if password, set := parsed.User.Password(); set {
		config.Password = password
	}
}

// readQuery splits the query into transport security, credentials and session
// parameters. libpq accepts user and password as query keys too, and a form
// that ignored them would drop credentials the user pasted.
func readQuery(query url.Values, config *Config) error {
	for key, values := range query {
		value := ""
		if len(values) > 0 {
			value = values[len(values)-1]
		}

		switch key {
		case keySSLMode:
			config.TLS.Mode = SSLMode(value)
		case keyRootCert:
			config.TLS.RootCert = value
		case keyCert:
			config.TLS.Cert = value
		case keyKey:
			config.TLS.Key = value
		case keyUser:
			config.User = value
		case keyPassword:
			config.Password = value
		default:
			if config.Params == nil {
				config.Params = make(map[string]string)
			}
			config.Params[key] = value
		}
	}

	if err := config.TLS.Mode.validate(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidURI, strings.TrimPrefix(err.Error(), ErrInvalidConfig.Error()+": "))
	}

	return nil
}
