package deps_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/tooling/deps"
)

const module = "github.com/gsoares85/hermes"

// The dependency rule of the project, as the architecture states it: the core
// knows neither the UI, nor the driver, nor the development tooling, nor any
// concrete database technology.
var projectRules = []deps.Rule{
	{
		Reason:   "the core layer must not know the UI",
		Packages: module + "/internal/core",
		Forbidden: []string{
			module + "/internal/ui",
			module + "/frontend",
			"github.com/wailsapp",
		},
	},
	{
		Reason:   "the core layer must not know a database technology",
		Packages: module + "/internal/core",
		Forbidden: []string{
			"github.com/jackc/pgx",
			"database/sql",
			"github.com/testcontainers",
		},
	},
	{
		// The core talks to the engine contract in internal/driver and never
		// to an implementation of it. That is the whole point of the seam: a
		// single import of internal/driver/postgres from the core would put
		// pgx one hop away from the domain and make it untestable without
		// Docker. The contract itself is allowed, which is why this forbids
		// the subpackages and not the package.
		Reason:   "the core layer must talk to the engine contract, never to an implementation",
		Packages: module + "/internal/core",
		Forbidden: []string{
			module + "/internal/driver/",
		},
	},
	{
		// The vault contract lives in internal/core/secret, where it is
		// consumed; every implementation of it lives in internal/vault, and
		// so does every library that talks to a keychain. The whole tree is
		// forbidden rather than only its subpackages — unlike internal/driver,
		// there is no contract in there for the core to legitimately reach.
		// internal/credential is the same shape: handing a password to a child
		// process means files and process environments, which is infrastructure
		// and stays outside. See ADR-0010.
		//
		// internal/sqlitestore is the same shape once more, for the local
		// state: internal/core/store owns the concept of what is remembered
		// between runs, and that package is the only one that knows it is a
		// database. The rule above forbidding database/sql in the core is not
		// enough on its own — reaching the store through its own package would
		// put the driver one hop from the domain without importing it. See
		// ADR-0014.
		Reason:   "the core layer must not reach an implementation of the vault",
		Packages: module + "/internal/core",
		Forbidden: []string{
			module + "/internal/vault",
			module + "/internal/credential",
			module + "/internal/filestore",
			module + "/internal/proc",
			module + "/internal/sqlitestore",
		},
	},
	{
		// The rule that would have caught the mistake ADR-0010 was written to
		// prevent. Without it, cgo and D-Bus in the domain compile and pass:
		// the rules above name a driver and a framework, and a keychain is
		// neither.
		//
		// It is stated for the core alone. The UI is covered by the rule above
		// forbidding internal/vault, which is the only way it could reach a
		// keychain of its own accord; naming the libraries there too would fire
		// the day internal/ui legitimately imports Wails, which brings D-Bus
		// with it on Linux — a failure for the wrong reason teaches people to
		// edit the gate rather than the code.
		Reason:   "the core layer must not talk to an operating system keychain",
		Packages: module + "/internal/core",
		Forbidden: []string{
			"github.com/keybase/go-keychain",
			"github.com/danieljoos/wincred",
			"github.com/godbus/dbus",
			"github.com/zalando/go-keyring",
		},
	},
	{
		Reason:   "the core layer must not depend on development tooling",
		Packages: module + "/internal/core",
		Forbidden: []string{
			module + "/internal/tooling",
			module + "/internal/testsupport",
		},
	},
	{
		Reason:   "the UI layer must not reach a database technology directly",
		Packages: module + "/internal/ui",
		Forbidden: []string{
			"github.com/jackc/pgx",
			"database/sql",
		},
	},
	{
		// The same rule the core has. The UI is handed an engine by the
		// command that wires the application together; reaching for one
		// itself would make the boundary a suggestion.
		Reason:   "the UI layer must talk to the engine contract, never to an implementation",
		Packages: module + "/internal/ui",
		Forbidden: []string{
			module + "/internal/driver/",
		},
	},
	{
		// The same rule again, for the vault. The window is handed one by the
		// command that wires the application together, exactly as it is handed
		// an engine.
		Reason:   "the UI layer must be handed a vault, never reach for one",
		Packages: module + "/internal/ui",
		Forbidden: []string{
			module + "/internal/vault",
			module + "/internal/credential",
			module + "/internal/filestore",
			module + "/internal/proc",
			module + "/internal/sqlitestore",
		},
	},
}

func TestCheckReportsAForbiddenImport(t *testing.T) {
	t.Parallel()

	graph := map[string][]string{
		"app/core/thing": {"app/ui/window", "fmt"},
	}
	rules := []deps.Rule{{
		Reason:    "the core layer must not know the UI",
		Packages:  "app/core",
		Forbidden: []string{"app/ui"},
	}}

	got := deps.Check(graph, rules)
	if len(got) != 1 {
		t.Fatalf("got %d violations, want 1: %v", len(got), got)
	}
	if got[0].Package != "app/core/thing" || got[0].Import != "app/ui/window" {
		t.Errorf("violation = %v, want the core package and the UI import", got[0])
	}
	if !strings.Contains(got[0].String(), "must not know the UI") {
		t.Errorf("violation %q does not explain the rule", got[0])
	}
}

func TestCheckIgnoresPackagesOutsideTheRule(t *testing.T) {
	t.Parallel()

	graph := map[string][]string{
		"app/ui/window":  {"app/ui/theme", "app/core/thing"},
		"app/cmd/binary": {"app/ui/window"},
	}
	rules := []deps.Rule{{
		Reason:    "the core layer must not know the UI",
		Packages:  "app/core",
		Forbidden: []string{"app/ui"},
	}}

	if got := deps.Check(graph, rules); len(got) != 0 {
		t.Errorf("got %d violations (%v), want none: the rule covers app/core only", len(got), got)
	}
}

// A layer importing itself is not a violation of a rule about that layer, and a
// rule that fired on it would be useless.
func TestCheckAllowsALayerToImportItself(t *testing.T) {
	t.Parallel()

	graph := map[string][]string{"app/core/a": {"app/core/b"}}
	rules := []deps.Rule{{
		Reason:    "self",
		Packages:  "app/core",
		Forbidden: []string{"app/core"},
	}}

	if got := deps.Check(graph, rules); len(got) != 0 {
		t.Errorf("got %d violations (%v), want none", len(got), got)
	}
}

func TestCheckReportsEveryViolationSorted(t *testing.T) {
	t.Parallel()

	graph := map[string][]string{
		"app/core/z": {"app/ui/b"},
		"app/core/a": {"app/ui/z", "app/ui/a"},
	}
	rules := []deps.Rule{{
		Reason:    "no UI in the core",
		Packages:  "app/core",
		Forbidden: []string{"app/ui"},
	}}

	got := deps.Check(graph, rules)
	if len(got) != 3 {
		t.Fatalf("got %d violations, want 3: %v", len(got), got)
	}
	want := []string{"app/core/a", "app/core/a", "app/core/z"}
	for i, pkg := range want {
		if got[i].Package != pkg {
			t.Errorf("violation %d is for %q, want %q (%v)", i, got[i].Package, pkg, got)
		}
	}
}

// The rules are data, and data can be written down without ever being wired in.
// A rule protecting the layer from something nothing imports yet passes exactly
// as well when it has been deleted, misspelled or never added — which is the
// state internal/vault and internal/credential are in until the phases that
// create them. This feeds the real project rules a graph that violates each new
// edge, so the gate is proven to bite before there is anything for it to bite.
func TestTheProjectRulesForbidReachingForAVault(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ pkg, imported string }{
		"a keychain library in the core": {
			module + "/internal/core/conn", "github.com/keybase/go-keychain",
		},
		"the credential store of Windows in the core": {
			module + "/internal/core/conn", "github.com/danieljoos/wincred",
		},
		"the session bus in the core": {
			module + "/internal/core/conn", "github.com/godbus/dbus/v5",
		},
		"a vault implementation in the core": {
			module + "/internal/core/conn", module + "/internal/vault",
		},
		"the credential helper in the core": {
			module + "/internal/core/dump", module + "/internal/credential",
		},
		"a vault implementation in the UI": {
			module + "/internal/ui", module + "/internal/vault",
		},
		"the credential helper in the UI": {
			module + "/internal/ui", module + "/internal/credential",
		},
		"the file store in the core": {
			module + "/internal/core/profile", module + "/internal/filestore",
		},
		"the file store in the UI": {
			module + "/internal/ui", module + "/internal/filestore",
		},

		// The local state, which does not exist yet: internal/core/store is
		// the contract and internal/sqlitestore will be the only package that
		// knows it is a database. A rule written for a package nothing imports
		// yet passes exactly as well when it has been misspelled or never
		// added, which is what these two cases are for.
		"the SQLite store in the core": {
			module + "/internal/core/store", module + "/internal/sqlitestore",
		},
		"the SQLite store in the UI": {
			module + "/internal/ui", module + "/internal/sqlitestore",
		},

		// The catalog is the package that most wants to reach for a driver:
		// everything it does is read pg_catalog, and the shortest way to write
		// that is to hold a pgx connection. It must not. The rules that stop it
		// are the ones already written for internal/core, which match by
		// prefix and therefore reach every package under it — these cases are
		// what proves that rather than assuming it, because a rule believed to
		// cover a package it does not is a gate that passes by not looking.
		"a driver implementation in the catalog": {
			module + "/internal/core/catalog", module + "/internal/driver/postgres",
		},
		"pgx in the catalog": {
			module + "/internal/core/catalog", "github.com/jackc/pgx/v5",
		},
		"pgx in the DDL writer": {
			module + "/internal/core/ddl", "github.com/jackc/pgx/v5",
		},
		"the test harness in the catalog": {
			module + "/internal/core/catalog", module + "/internal/testsupport",
		},

		// The DDL writer is the second package with a pull towards the harness,
		// because the test that matters most to it needs a server: the round
		// trip writes a model, applies it and reads it back. That test is test
		// code and may import the harness; the package itself must not, or the
		// writer would need Docker to compile.
		"the test harness in the DDL writer": {
			module + "/internal/core/ddl", module + "/internal/testsupport",
		},
		"a driver implementation in the DDL writer": {
			module + "/internal/core/ddl", module + "/internal/driver/postgres",
		},
	}

	for name, forbidden := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			graph := map[string][]string{forbidden.pkg: {forbidden.imported}}
			if got := deps.Check(graph, projectRules); len(got) == 0 {
				t.Errorf("%s importing %s is allowed by the project rules",
					forbidden.pkg, forbidden.imported)
			}
		})
	}
}

// The rule applied to the repository itself. This is the gate: it fails the
// build the day someone imports the UI, a driver or the test harness from the
// core, which is the moment the architecture stops being true.
func TestTheProjectRespectsTheDependencyRule(t *testing.T) {
	t.Parallel()

	graph := buildGraph(t)
	if len(graph) == 0 {
		t.Fatal("the dependency graph is empty, the rule would pass vacuously")
	}

	if got := deps.Check(graph, projectRules); len(got) != 0 {
		for _, violation := range got {
			t.Errorf("%v", violation)
		}
	}
}

// The platforms the product is built for. The graph is read once per platform
// because build tags make it a different graph on each: internal/vault has one
// implementation per operating system, and each of them imports a different
// keychain library. A gate that only ever looked at the runner it happened to
// run on would enforce the rule about go-keychain on macOS alone — which is to
// say, never, since the pipeline reads this on Linux.
var platforms = []string{"linux", "darwin", "windows"}

// buildGraph asks the toolchain for the full dependency list of every package
// in the module, on every platform it is built for, which is the same graph
// each of those compilers sees.
func buildGraph(t *testing.T) map[string][]string {
	t.Helper()

	graph := make(map[string][]string)
	for _, platform := range platforms {
		for pkg, deps := range platformGraph(t, platform) {
			// Unioned rather than kept apart: a rule is broken if it is broken
			// anywhere, and the violation names the package either way.
			graph[pkg] = append(graph[pkg], deps...)
		}
	}

	return graph
}

func platformGraph(t *testing.T, platform string) map[string][]string {
	t.Helper()

	command := exec.CommandContext(t.Context(), "go", "list", "-deps", "-json", "./...")
	command.Dir = "../../.."
	// CGO off because this only ever parses: a darwin graph read on a Linux
	// runner must not need a C toolchain that can target darwin.
	command.Env = append(os.Environ(), "GOOS="+platform, "CGO_ENABLED=0")

	output, err := command.Output()
	if err != nil {
		t.Fatalf("listing the packages of the module for %s: %v", platform, err)
	}

	type listed struct {
		ImportPath string
		Deps       []string
	}

	graph := make(map[string][]string)
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for decoder.More() {
		var pkg listed
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("reading the package list: %v", err)
		}
		if strings.HasPrefix(pkg.ImportPath, module) {
			graph[pkg.ImportPath] = pkg.Deps
		}
	}

	return graph
}

// A gate that reads one platform enforces the rules of one platform. The three
// keychain libraries are each behind a build tag, so a graph read on a single
// operating system contains exactly one of them — and the rule forbidding the
// other two would pass by never being tested.
//
// This asserts the reading itself: the union has to contain all three, or the
// gate above is quietly checking a third of what it claims to.
func TestTheGraphIsReadForEveryPlatformTheProductIsBuiltFor(t *testing.T) {
	t.Parallel()

	graph := buildGraph(t)

	imports := make(map[string]bool)
	for _, deps := range graph {
		for _, imported := range deps {
			imports[imported] = true
		}
	}

	for _, keychain := range []string{
		"github.com/keybase/go-keychain",
		"github.com/danieljoos/wincred",
		"github.com/godbus/dbus/v5",
	} {
		if !imports[keychain] {
			t.Errorf("%s is in no graph, so the rule forbidding it in the core is never exercised", keychain)
		}
	}
}
