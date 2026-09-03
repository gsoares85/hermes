package profile

import (
	"fmt"
	"io"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/gsoares85/hermes/internal/core/conn"
)

// Version is the version of the connections file this build reads and writes.
//
// It is written first and read first, and a file carrying any other number is
// refused rather than read anyway. That is the whole point of having it: a
// format with no version leaves the day it changes with two bad options, to
// guess or to break, and guessing at the meaning of a file that holds how
// someone reaches their production database is not an option.
const Version = 1

// ReadConnections reads a connections file.
//
// Nothing it returns carries a password, because nothing it reads can: the
// format has no field for one. A file that names one is refused rather than
// quietly ignored, so that someone who typed a password into it is told where
// passwords actually live instead of believing this one took effect.
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

	for index, config := range connections {
		if strings.TrimSpace(config.ID) == "" {
			return fmt.Errorf("%w: connection %d (%s) has no id, so its password could never be found again",
				ErrInvalidFile, index+1, config.Name)
		}
		written.Connections = append(written.Connections, entryOf(config))
	}

	encoder := toml.NewEncoder(w)
	if err := encoder.Encode(written); err != nil {
		return fmt.Errorf("writing the connections file: %w", err)
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
// and this has nowhere to put one. The guarantee that a saved connection never
// carries a secret is therefore a property of the type, checked by the
// compiler, instead of a tag someone has to remember not to add.
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
		Archived: e.Archived,
		Params:   e.Params,
		Options:  e.Options,
	}
}
