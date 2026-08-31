// Package deps turns the dependency rule of the architecture into something the
// build can fail on.
//
// The arrows point inwards: the domain does not know infrastructure and
// infrastructure does not know the UI. Written only as a comment, that rule
// survives exactly as long as everyone remembers it. Here it is data, applied to
// the real import graph by a test.
package deps

import (
	"fmt"
	"sort"
	"strings"
)

// Rule forbids a set of import path prefixes inside a set of packages.
type Rule struct {
	// Reason states what the rule protects, and is what a failure reports.
	Reason string
	// Packages is the import path prefix the rule applies to.
	Packages string
	// Forbidden lists the import path prefixes those packages must not reach.
	Forbidden []string
}

// Violation is one package importing something its layer must not know about.
type Violation struct {
	Package string
	Import  string
	Reason  string
}

// String renders the violation the way the failing test prints it.
func (v Violation) String() string {
	return fmt.Sprintf("%s imports %s: %s", v.Package, v.Import, v.Reason)
}

// Check applies the rules to a dependency graph, mapping each package to the
// full list of packages it depends on. Violations come back sorted, so the
// failure of a build reads the same way twice.
func Check(graph map[string][]string, rules []Rule) []Violation {
	var violations []Violation

	for pkg, imports := range graph {
		for _, rule := range rules {
			if !strings.HasPrefix(pkg, rule.Packages) {
				continue
			}
			violations = append(violations, violationsIn(pkg, imports, rule)...)
		}
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Package != violations[j].Package {
			return violations[i].Package < violations[j].Package
		}

		return violations[i].Import < violations[j].Import
	})

	return violations
}

func violationsIn(pkg string, imports []string, rule Rule) []Violation {
	var violations []Violation

	for _, imported := range imports {
		for _, forbidden := range rule.Forbidden {
			// A package is never a violation of a rule about itself: the
			// prefix of a layer also matches the layer's own packages.
			if !strings.HasPrefix(imported, forbidden) || strings.HasPrefix(imported, rule.Packages) {
				continue
			}

			violations = append(violations, Violation{Package: pkg, Import: imported, Reason: rule.Reason})
		}
	}

	return violations
}
