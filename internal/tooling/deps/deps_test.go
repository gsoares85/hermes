package deps_test

import (
	"encoding/json"
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

// buildGraph asks the toolchain for the full dependency list of every package
// in the module, which is the same graph the compiler sees.
func buildGraph(t *testing.T) map[string][]string {
	t.Helper()

	command := exec.CommandContext(t.Context(), "go", "list", "-deps", "-json", "./...")
	command.Dir = "../../.."

	output, err := command.Output()
	if err != nil {
		t.Fatalf("listing the packages of the module: %v", err)
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
