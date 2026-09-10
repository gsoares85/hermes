package profile

import (
	"fmt"
	"io"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/gsoares85/hermes/internal/core/conn"
)

// The versions of the connections file this build understands.
//
// The number is written first and read first, and a file carrying anything
// outside this range is refused rather than read anyway. That is the whole
// point of having it: a format with no version leaves the day it changes with
// two bad options, to guess or to break, and guessing at the meaning of a file
// that holds how someone reaches their production database is not an option.
//
// Version 2 added the environment and the read-only mark. They went into a
// version of their own rather than into version 1 because the decoder here is
// strict: an older build meeting them under version 1 would refuse the file and
// blame a field, when what is actually true is that the file is newer than it
// is. It reads 2 and answers exactly that.
const (
	// FirstVersion is the oldest file this build still reads. Refusing one
	// would lose every connection somebody had saved, over two optional fields
	// they had never heard of.
	FirstVersion = 1

	// Version is what this build writes, always, whatever it read.
	Version = 2
)

// ReadConnections reads a connections file.
//
// Nothing it returns carries a password. The format has no field for one, and
// the two free-form maps it does have are checked for the keywords libpq would
// read a secret from — password under params is a password in a plain-text
// file, whatever the struct says. Either way the file is refused rather than
// quietly cleaned up, so that someone who typed a password into it is told
// where passwords actually live instead of believing this one took effect.
func ReadConnections(r io.Reader) ([]conn.Config, error) {
	source, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading the connections file: %w", err)
	}

	var read file
	if err := decode(source, &read); err != nil {
		return nil, err
	}

	if err := read.checkVersion(); err != nil {
		return nil, err
	}

	return read.configs(source)
}

// WriteConnections writes the connections file.
//
// It takes the whole list rather than appending one: the file is a value, and
// rewriting it whole is what makes a half-written file impossible to produce by
// forgetting a case.
func WriteConnections(w io.Writer, connections []conn.Config) error {
	written := file{Version: Version, Connections: make([]entry, 0, len(connections))}

	// The identifier is what the password is filed under, so a missing one is a
	// password nothing can ever find again and a repeated one is two
	// connections quietly sharing a credential. ReadConnections refuses both;
	// refusing them here too is what stops this build writing a file it cannot
	// itself read back.
	seen := make(map[string]int, len(connections))

	for index, config := range connections {
		id := strings.TrimSpace(config.ID)
		if id == "" {
			return fmt.Errorf("%w: connection %d (%s) has no id, so its password could never be found again",
				ErrInvalidFile, index+1, config.Name)
		}

		if first, repeated := seen[id]; repeated {
			return fmt.Errorf("%w: connections %d and %d share the id %q, and two connections cannot share one password",
				ErrInvalidFile, first+1, index+1, id)
		}
		seen[id] = index

		// The one rule this has to repeat rather than leave to the reader. The
		// format has no password field, but params and options are maps of
		// text and password is a libpq keyword, so a secret can reach the file
		// through either. Refusing on the way in as well as on the way out is
		// what stops this build writing bytes it would then refuse to read.
		if err := credentialFree(index, config); err != nil {
			return err
		}

		written.Connections = append(written.Connections, entryOf(config))
	}

	encoder := toml.NewEncoder(w)
	if err := encoder.Encode(written); err != nil {
		return fmt.Errorf("writing the connections file: %w", err)
	}

	return nil
}

// credentialFree refuses a connection carrying a secret under a free-form key,
// naming the connection so that the person can find it in a list they handed
// over whole.
func credentialFree(index int, config conn.Config) error {
	for _, settings := range []struct {
		field  string
		values map[string]string
	}{
		{"params", config.Params},
		{"options", config.Options},
	} {
		key, found := conn.CredentialKeyword(settings.values)
		if !found {
			continue
		}

		return fmt.Errorf(
			"%w: connection %d (%s) carries a password in %s.%s, and this file is never where a password goes",
			ErrInvalidFile, index+1, config.Name, settings.field, key)
	}

	return nil
}

// file is the document. The version is a field of it rather than a comment
// because a comment is not read by anything.
type file struct {
	Version     int     `toml:"version"`
	Connections []entry `toml:"connection"`
}

// entry is one connection as the file spells it.
//
// It is a type of its own rather than conn.Config with tags, and that is the
// design rather than an accident of layering: conn.Config has a Password field
// and this has nowhere to put one. The compiler keeps that much on its own —
// there is no field to assign a password to and no tag to forget.
//
// What the compiler cannot keep is the last two fields. Params and Options are
// maps of text, and password is a keyword libpq honours, so the type alone
// would let a secret in through either of them. That is why the guarantee is
// the type plus one rule: conn.CredentialKeyword, applied when the file is
// written and again when it is read.
//
// The scalars come before the tables because TOML gives every key after a table
// header to that table: with params written first, archived would be read back
// as a session parameter.
type entry struct {
	ID          string `toml:"id"`
	Name        string `toml:"name,omitempty"`
	Host        string `toml:"host"`
	Port        int    `toml:"port"`
	Database    string `toml:"database,omitempty"`
	User        string `toml:"user"`
	SSLMode     string `toml:"sslmode,omitempty"`
	SSLRootCert string `toml:"sslrootcert,omitempty"`
	SSLCert     string `toml:"sslcert,omitempty"`
	SSLKey      string `toml:"sslkey,omitempty"`
	Environment string `toml:"environment,omitempty"`
	ReadOnly    bool   `toml:"read_only,omitempty"`
	Archived    bool   `toml:"archived,omitempty"`

	Params  map[string]string `toml:"params,omitempty"`
	Options map[string]string `toml:"options,omitempty"`
}

func entryOf(config conn.Config) entry {
	return entry{
		ID:          config.ID,
		Name:        config.Name,
		Host:        config.Host,
		Port:        config.Port,
		Database:    config.Database,
		User:        config.User,
		SSLMode:     string(config.TLS.Mode),
		SSLRootCert: config.TLS.RootCert,
		SSLCert:     config.TLS.Cert,
		SSLKey:      config.TLS.Key,
		Environment: string(config.Environment),
		ReadOnly:    config.ReadOnly,
		Archived:    config.Archived,
		Params:      config.Params,
		Options:     config.Options,
	}
}

func (e entry) config() conn.Config {
	return conn.Config{
		ID:       e.ID,
		Name:     e.Name,
		Host:     e.Host,
		Port:     e.Port,
		Database: e.Database,
		User:     e.User,
		TLS: conn.TLS{
			Mode:     conn.SSLMode(e.SSLMode),
			RootCert: e.SSLRootCert,
			Cert:     e.SSLCert,
			Key:      e.SSLKey,
		},
		Environment: conn.Environment(e.Environment),
		ReadOnly:    e.ReadOnly,
		Archived:    e.Archived,
		Params:      e.Params,
		Options:     e.Options,
	}
}
