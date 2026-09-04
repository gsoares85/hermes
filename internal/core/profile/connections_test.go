package profile_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/core/profile"
)

// The acceptance criterion of this work, written as an assertion about bytes
// rather than about fields.
//
// Checking that the struct has no password field would pass the day someone
// adds a Comment field and a connection is saved with the password typed into
// it. Searching the file for the secret itself is the check that keeps holding:
// whatever route the password could take into this file, it fails.
func TestNoByteOfTheFileIsThePassword(t *testing.T) {
	t.Parallel()

	const password = "correct-horse-battery-staple"

	saved := sample()
	saved.Password = password
	saved.Params = map[string]string{"application_name": "hermes"}
	saved.Options = map[string]string{"connect_timeout": "10"}

	var written bytes.Buffer
	if err := profile.WriteConnections(&written, []conn.Config{saved}); err != nil {
		t.Fatalf("writing the connections file: %v", err)
	}

	if bytes.Contains(written.Bytes(), []byte(password)) {
		t.Errorf("the connections file carries the password:\n%s", written.String())
	}
	// The file is worth nothing if it did not save the connection either.
	if !bytes.Contains(written.Bytes(), []byte(saved.Host)) {
		t.Errorf("the connections file does not carry the host:\n%s", written.String())
	}
}

// Reading back what was written has to give the same connection, minus the one
// thing the format refuses to carry. Anything less makes the file a lossy copy
// of what someone configured.
func TestAConnectionSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()

	saved := sample()
	saved.Password = "s3cr3t"
	saved.Params = map[string]string{"application_name": "hermes", "search_path": "public"}
	saved.Options = map[string]string{"connect_timeout": "10"}

	var written bytes.Buffer
	if err := profile.WriteConnections(&written, []conn.Config{saved}); err != nil {
		t.Fatalf("writing the connections file: %v", err)
	}

	read, err := profile.ReadConnections(bytes.NewReader(written.Bytes()))
	if err != nil {
		t.Fatalf("reading the connections file back: %v\n%s", err, written.String())
	}
	if len(read) != 1 {
		t.Fatalf("read %d connections, want 1", len(read))
	}

	want := saved
	want.Password = ""
	if !reflect.DeepEqual(read[0], want) {
		t.Errorf("the connection came back changed:\n got %+v\nwant %+v", read[0], want)
	}
}

func TestTheFileSaysWhichVersionItIs(t *testing.T) {
	t.Parallel()

	var written bytes.Buffer
	if err := profile.WriteConnections(&written, []conn.Config{sample()}); err != nil {
		t.Fatalf("writing the connections file: %v", err)
	}

	if !strings.HasPrefix(written.String(), "version = 1\n") {
		t.Errorf("the file does not open with its version:\n%s", written.String())
	}
}

// A version this build does not know is refused rather than read as far as it
// goes. The fields it recognises may mean something else in that version, and a
// connection opened against the wrong host is not a cosmetic mistake.
func TestAFileOfAnotherVersionIsRefused(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"from the future": "version = 2\n",
		"with no version": "[[connection]]\nid = \"a\"\n",
	}

	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := profile.ReadConnections(strings.NewReader(source))
			if !errors.Is(err, profile.ErrUnknownVersion) {
				t.Errorf("ReadConnections() = %v, want ErrUnknownVersion", err)
			}
		})
	}
}

func TestAFileWithNoConnectionsIsAFileWithNoConnections(t *testing.T) {
	t.Parallel()

	read, err := profile.ReadConnections(strings.NewReader("version = 1\n"))
	if err != nil {
		t.Fatalf("ReadConnections() = %v, want an empty list", err)
	}
	if len(read) != 0 {
		t.Errorf("read %d connections from an empty file, want none", len(read))
	}
}

// Whoever has to fix the file is the person who wrote it, in an editor, so the
// error has to name the line and the key rather than say that something is
// wrong somewhere.
func TestAFaultNamesItsFieldAndItsLine(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		source string
		field  string
		line   int
	}{
		"no host": {
			`version = 1

[[connection]]
id   = "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f"
port = 5432
user = "reader"
`, "host", 3},
		"port out of range": {
			`version = 1

[[connection]]
id   = "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f"
host = "db.example.com"
port = 70000
user = "reader"
`, "port", 6},
		"unknown sslmode": {
			`version = 1

[[connection]]
id      = "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f"
host    = "db.example.com"
port    = 5432
user    = "reader"
sslmode = "sometimes"
`, "sslmode", 8},
		"no id": {
			`version = 1

[[connection]]
host = "db.example.com"
port = 5432
user = "reader"
`, "id", 3},
		"a field that does not exist": {
			`version = 1

[[connection]]
id      = "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f"
host    = "db.example.com"
port    = 5432
user    = "reader"
sslmodee = "verify-full"
`, "connection.sslmodee", 8},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := profile.ReadConnections(strings.NewReader(testCase.source))

			var problem profile.Problem
			if !errors.As(err, &problem) {
				t.Fatalf("ReadConnections() = %v, want a located problem", err)
			}
			if problem.Field != testCase.field {
				t.Errorf("the problem blames %q, want %q", problem.Field, testCase.field)
			}
			if problem.Line != testCase.line {
				t.Errorf("the problem is on line %d, want line %d (%v)", problem.Line, testCase.line, problem)
			}
			if !strings.Contains(problem.Error(), testCase.field) {
				t.Errorf("the message does not name the field: %v", problem)
			}
		})
	}
}

// The second connection is the one to blame, and pointing at the first would
// send someone to edit the entry that is fine.
func TestTheFaultIsFoundInTheRightConnection(t *testing.T) {
	t.Parallel()

	source := `version = 1

[[connection]]
id   = "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f"
host = "first.example.com"
port = 5432
user = "reader"

[[connection]]
id   = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
host = "second.example.com"
port = 99999
user = "reader"
`

	_, err := profile.ReadConnections(strings.NewReader(source))

	var problem profile.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("ReadConnections() = %v, want a located problem", err)
	}
	if problem.Line != 12 {
		t.Errorf("the problem is on line %d, want line 12 (%v)", problem.Line, problem)
	}
}

// Two connections sharing an identifier share one entry in the keychain: saving
// the second would overwrite the password of the first, and the first would
// start failing to authenticate for no visible reason.
func TestTwoConnectionsCannotShareAnIdentifier(t *testing.T) {
	t.Parallel()

	source := `version = 1

[[connection]]
id   = "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f"
host = "first.example.com"
port = 5432
user = "reader"

[[connection]]
id   = "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f"
host = "second.example.com"
port = 5432
user = "reader"
`

	_, err := profile.ReadConnections(strings.NewReader(source))

	var problem profile.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("ReadConnections() = %v, want a located problem", err)
	}
	if problem.Field != "id" || problem.Line != 10 {
		t.Errorf("the problem is %q on line %d, want id on line 10 (%v)", problem.Field, problem.Line, problem)
	}
}

// Someone who types a password into this file believes they have saved it. They
// have instead written a production password into a file in plain text, and the
// only useful thing to do is to refuse the file and say where passwords live.
func TestAPasswordInTheFileIsRefusedAndExplained(t *testing.T) {
	t.Parallel()

	source := `version = 1

[[connection]]
id       = "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f"
host     = "db.example.com"
port     = 5432
user     = "reader"
password = "hunter2"
`

	_, err := profile.ReadConnections(strings.NewReader(source))
	if !errors.Is(err, profile.ErrInvalidFile) {
		t.Fatalf("ReadConnections() = %v, want ErrInvalidFile", err)
	}
	if !strings.Contains(err.Error(), "keychain") {
		t.Errorf("the error does not say where passwords live: %v", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the error quoted the password back: %v", err)
	}
}

func TestBrokenSyntaxPointsAtItsLine(t *testing.T) {
	t.Parallel()

	source := `version = 1

[[connection]]
id = "9d0f6e5c
`

	_, err := profile.ReadConnections(strings.NewReader(source))

	var problem profile.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("ReadConnections() = %v, want a located problem", err)
	}
	if problem.Line != 4 {
		t.Errorf("the problem is on line %d, want line 4 (%v)", problem.Line, problem)
	}
}

// Writing a connection with no identifier would produce a file whose passwords
// can never be found again: the entry in the keychain is filed under the
// identifier, and there would be nothing to look it up with.
func TestWritingRefusesAConnectionWithoutAnIdentifier(t *testing.T) {
	t.Parallel()

	unsaved := sample()
	unsaved.ID = ""

	err := profile.WriteConnections(&bytes.Buffer{}, []conn.Config{unsaved})
	if !errors.Is(err, profile.ErrInvalidFile) {
		t.Errorf("WriteConnections() = %v, want ErrInvalidFile", err)
	}
}

// A file written by Hermes is one someone is expected to read, edit and put
// under version control, so it has to look like something a person wrote.
func TestTheFileIsWorthOpeningInAnEditor(t *testing.T) {
	t.Parallel()

	var written bytes.Buffer
	if err := profile.WriteConnections(&written, []conn.Config{sample()}); err != nil {
		t.Fatalf("writing the connections file: %v", err)
	}

	for _, want := range []string{"[[connection]]", "id = ", "host = ", "port = 5432", "sslmode = "} {
		if !strings.Contains(written.String(), want) {
			t.Errorf("the file does not contain %q:\n%s", want, written.String())
		}
	}
}

func sample() conn.Config {
	return conn.Config{
		ID:       "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f",
		Name:     "production — read only",
		Host:     "db.example.com",
		Port:     5432,
		Database: "app",
		User:     "reader",
		TLS: conn.TLS{
			Mode:     conn.SSLVerifyFull,
			RootCert: "/etc/ssl/certs/company-ca.pem",
		},
	}
}

// A session parameter can be called anything, including host. It lives in a
// table of its own after the fields of the connection, and blaming its line
// would send someone to edit something unrelated to the fault.
func TestAParameterIsNotMistakenForAField(t *testing.T) {
	t.Parallel()

	source := `version = 1

[[connection]]
id   = "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f"
port = 5432
user = "reader"

[connection.params]
host = "not a field of the connection"
`

	_, err := profile.ReadConnections(strings.NewReader(source))

	var problem profile.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("ReadConnections() = %v, want a located problem", err)
	}
	if problem.Field != "host" || problem.Line != 3 {
		t.Errorf("the problem is %q on line %d, want host on line 3 (%v)", problem.Field, problem.Line, problem)
	}
}

// A stray key at the top of the file has no table to name it after, and the
// message still has to be about the key someone typed.
func TestAStrayKeyAtTheTopOfTheFileIsNamed(t *testing.T) {
	t.Parallel()

	_, err := profile.ReadConnections(strings.NewReader("version = 1\nversionn = 2\n"))

	var problem profile.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("ReadConnections() = %v, want a located problem", err)
	}
	if problem.Field != "versionn" || problem.Line != 2 {
		t.Errorf("the problem is %q on line %d, want versionn on line 2 (%v)", problem.Field, problem.Line, problem)
	}
}

// The rendering is the whole value of locating a fault, so it is asserted
// rather than assumed: a Problem that knows the line and prints only the
// message has told the person nothing they could not already see.
func TestAProblemReadsLikeSomethingSomeoneCanAct(t *testing.T) {
	t.Parallel()

	cause := errors.New("no host")

	cases := map[string]struct {
		problem profile.Problem
		want    string
	}{
		"line and field": {profile.Problem{Line: 7, Field: "host", Err: cause}, "line 7, host: no host"},
		"line only":      {profile.Problem{Line: 7, Err: cause}, "line 7: no host"},
		"neither":        {profile.Problem{Err: cause}, "no host"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := testCase.problem.Error(); got != testCase.want {
				t.Errorf("Error() = %q, want %q", got, testCase.want)
			}
			if !errors.Is(testCase.problem, cause) {
				t.Error("the problem does not unwrap to what caused it")
			}
		})
	}
}

func TestAFileThatCannotBeReadIsReported(t *testing.T) {
	t.Parallel()

	broken := errors.New("the disk went away")

	_, err := profile.ReadConnections(failingReader{broken})
	if !errors.Is(err, broken) {
		t.Errorf("ReadConnections() = %v, want the failure of the reader", err)
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// The identifier is what the password is filed under, so two connections
// sharing one is two connections sharing a password: renaming one, or deleting
// it, silently takes the other's credential with it.
//
// Reading refuses that already. Writing has to refuse it too, or the build can
// produce a file it cannot itself read back.
func TestWriteConnectionsRefusesTwoConnectionsWithOneIdentifier(t *testing.T) {
	t.Parallel()

	var written strings.Builder

	err := profile.WriteConnections(&written, []conn.Config{
		{ID: "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f", Host: "first.example.com", Port: 5432, User: "u"},
		{ID: "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f", Host: "second.example.com", Port: 5432, User: "u"},
	})
	if err == nil {
		t.Fatal("WriteConnections wrote a file it could not read back")
	}
	if !strings.Contains(err.Error(), "9d0f6e5c") {
		t.Errorf("WriteConnections = %v, want it to name the identifier that repeats", err)
	}
	if written.Len() != 0 {
		t.Errorf("WriteConnections wrote %q before refusing", written.String())
	}
}

// Whitespace around an identifier is not a second identifier. The validation
// trims before it judges, so the duplicate check has to trim before it compares
// — otherwise " a" and "a" pass as distinct and then collide in the keychain,
// which is the failure the check exists to prevent, arrived at the long way.
func TestReadConnectionsSeesThroughWhitespaceInAnIdentifier(t *testing.T) {
	t.Parallel()

	_, err := profile.ReadConnections(strings.NewReader(`version = 1

[[connection]]
id   = "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f"
host = "first.example.com"
port = 5432
user = "u"

[[connection]]
id   = " 9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f "
host = "second.example.com"
port = 5432
user = "u"
`))
	if err == nil {
		t.Fatal("ReadConnections accepted two connections whose identifiers differ only in whitespace")
	}
}

// The file is a value: reading one and editing what comes back must not reach
// into whatever produced it, and writing one must not leave the caller's
// connection sharing a map with the entry that was written. Config.Clone exists
// for exactly this, and the round trip through TOML has to honour it.
func TestTheFileSharesNoMapWithTheConnectionsItCarries(t *testing.T) {
	t.Parallel()

	original := conn.Config{
		ID: "9d0f6e5c-1b2a-4c3d-8e4f-5a6b7c8d9e0f", Host: "db.example.com", Port: 5432, User: "u",
		Params:  map[string]string{"application_name": "hermes"},
		Options: map[string]string{"connect_timeout": "10"},
	}

	var written strings.Builder
	if err := profile.WriteConnections(&written, []conn.Config{original}); err != nil {
		t.Fatalf("WriteConnections = %v", err)
	}

	read, err := profile.ReadConnections(strings.NewReader(written.String()))
	if err != nil {
		t.Fatalf("ReadConnections = %v", err)
	}
	if len(read) != 1 {
		t.Fatalf("ReadConnections returned %d connections, want 1", len(read))
	}

	// Editing what came back must not reach the connection that was written.
	read[0].Params["application_name"] = "something else"
	read[0].Options["connect_timeout"] = "0"

	if original.Params["application_name"] != "hermes" {
		t.Errorf("editing what was read changed the connection that was written: %v", original.Params)
	}
	if original.Options["connect_timeout"] != "10" {
		t.Errorf("editing what was read changed the connection that was written: %v", original.Options)
	}
}
