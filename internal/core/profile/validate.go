package profile

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/gsoares85/hermes/internal/core/conn"
)

// Faults a connections file can have. They are sentinels because the layer
// above answers each one differently: an unknown version is a file from a newer
// Hermes and the person needs to be told to update, while an invalid file is
// something they can fix in an editor if they are told where.
var (
	// ErrUnknownVersion is a file this build must not try to interpret.
	ErrUnknownVersion = errors.New("unknown connections file version")

	// ErrInvalidFile is a file this build understands and refuses.
	ErrInvalidFile = errors.New("invalid connections file")
)

// Problem is a fault located in the file, so that the person who has to fix it
// is told where to look rather than that something, somewhere, is wrong.
//
// Line is 1-based, and zero when the fault belongs to the file as a whole
// rather than to a line of it.
type Problem struct {
	Line  int
	Field string
	Err   error
}

func (p Problem) Error() string {
	switch {
	case p.Line > 0 && p.Field != "":
		return fmt.Sprintf("line %d, %s: %v", p.Line, p.Field, p.Err)
	case p.Line > 0:
		return fmt.Sprintf("line %d: %v", p.Line, p.Err)
	default:
		return p.Err.Error()
	}
}

func (p Problem) Unwrap() error { return p.Err }

// decode turns the bytes into the document, rejecting any key the format does
// not have.
//
// Unknown keys are refused rather than skipped because the keys of this file
// decide how a connection is secured. A file with sslmodee = "verify-full" in
// it, read leniently, connects with no verification at all while looking to its
// author exactly like one that does — and a typo is by far the likeliest way
// that happens.
func decode(source []byte, into *file) error {
	err := toml.NewDecoder(bytes.NewReader(source)).DisallowUnknownFields().Decode(into)
	if err == nil {
		return nil
	}

	var unknown *toml.StrictMissingError
	if errors.As(err, &unknown) {
		return unknownKey(unknown)
	}

	var decoded *toml.DecodeError
	if errors.As(err, &decoded) {
		row, _ := decoded.Position()

		return Problem{Line: row, Err: fmt.Errorf("%w: %s", ErrInvalidFile, decoded.Error())}
	}

	return fmt.Errorf("%w: %w", ErrInvalidFile, err)
}

func unknownKey(unknown *toml.StrictMissingError) error {
	if len(unknown.Errors) == 0 {
		return fmt.Errorf("%w: %s", ErrInvalidFile, unknown.Error())
	}

	first := unknown.Errors[0]
	row, _ := first.Position()
	key := strings.Join(first.Key(), ".")

	// The one unknown key worth naming, because someone writing it believes
	// they are saving a password and is instead writing one into a file in
	// plain text. Hermes never puts it there, and has to say so.
	if strings.EqualFold(lastSegment(key), "password") {
		return Problem{
			Line:  row,
			Field: key,
			Err: fmt.Errorf("%w: this file has no password field — Hermes keeps passwords in the keychain of the system",
				ErrInvalidFile),
		}
	}

	return Problem{
		Line:  row,
		Field: key,
		Err:   fmt.Errorf("%w: no such field", ErrInvalidFile),
	}
}

func lastSegment(key string) string {
	if index := strings.LastIndex(key, "."); index >= 0 {
		return key[index+1:]
	}

	return key
}

// checkVersion refuses a file this build has no business reading.
//
// A version from the future is not read on a best-effort basis: the fields it
// does understand may mean something else there, and a connection opened
// against the wrong host or with the wrong sslmode is not a cosmetic mistake.
func (f file) checkVersion() error {
	switch f.Version {
	case Version:
		return nil
	case 0:
		return fmt.Errorf("%w: the file does not say which version it is, and every connections file must",
			ErrUnknownVersion)
	default:
		return fmt.Errorf("%w: the file is version %d and this Hermes reads version %d",
			ErrUnknownVersion, f.Version, Version)
	}
}

// configs validates every entry and turns it into a connection, blaming the
// line the mistake is on.
func (f file) configs(source []byte) ([]conn.Config, error) {
	configs := make([]conn.Config, 0, len(f.Connections))
	seen := make(map[string]int, len(f.Connections))

	for index, entry := range f.Connections {
		if err := entry.validate(); err != nil {
			return nil, locate(source, index, err)
		}

		if first, repeated := seen[entry.ID]; repeated {
			return nil, locate(source, index, conn.InvalidField{
				Field: "id",
				Problem: fmt.Sprintf("id %q is already used by connection %d, and two connections cannot share one password",
					entry.ID, first+1),
			})
		}
		seen[entry.ID] = index

		configs = append(configs, entry.config())
	}

	return configs, nil
}

// validate checks what the file is responsible for and lets the connection
// check the rest.
//
// The split is deliberate: an identifier is a fact about a saved connection and
// means nothing to one opened from a URI, so it is required here and not there.
// Everything else — the host, the port, the user, the mode — is a rule about
// connecting, is already written down once in conn, and is not written down
// again here.
func (e entry) validate() error {
	if strings.TrimSpace(e.ID) == "" {
		return conn.InvalidField{
			Field:   "id",
			Problem: "a saved connection needs an id: it is what its password is filed under",
		}
	}

	return e.config().Validate()
}

// locate turns a fault about a connection into a fault about a line.
//
// The source is scanned rather than the parser asked, because the parser is
// done by the time the value is known to be wrong: the position of a key is not
// carried on the decoded struct, and threading one through would mean parsing
// the file a second way to learn something a scan answers exactly.
func locate(source []byte, index int, err error) error {
	problem := Problem{Err: err}

	var invalid conn.InvalidField
	if errors.As(err, &invalid) {
		problem.Field = invalid.Field
	}

	problem.Line = lineOf(source, index, problem.Field)

	return problem
}

// lineOf finds the line of a key inside the nth connection of the file, and
// falls back to the line the connection starts on when the key is not there —
// which is the right place to point when the fault is that it is missing.
func lineOf(source []byte, index int, key string) int {
	var (
		connections = -1
		start       = 0
	)

	for number, text := range strings.Split(string(source), "\n") {
		trimmed := strings.TrimSpace(strings.SplitN(text, "#", 2)[0])

		if trimmed == "[[connection]]" {
			connections++
			if connections == index {
				start = number + 1
			}
			if connections > index {
				break
			}

			continue
		}

		if connections != index {
			continue
		}

		// A table header ends the fields of this connection: past
		// [connection.params], a line reading host = ... sets a session
		// parameter called host, and blaming it would send someone to edit a
		// line that has nothing to do with the fault.
		if strings.HasPrefix(trimmed, "[") {
			break
		}

		if key != "" && assigns(trimmed, key) {
			return number + 1
		}
	}

	return start
}

// assigns reports whether a line sets the key, allowing for the spacing people
// use to line their values up.
func assigns(line, key string) bool {
	rest, found := strings.CutPrefix(line, key)

	return found && strings.HasPrefix(strings.TrimSpace(rest), "=")
}
