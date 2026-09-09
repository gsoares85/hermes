package catalog

import (
	"context"
	"fmt"
	"strings"
)

// listViews reads the views of a schema and the query behind each one.
//
// pg_get_viewdef renders the query from the parse tree rather than returning
// what somebody typed, which is the normalisation this model would otherwise
// have to attempt itself: whitespace, casing of keywords, redundant parentheses
// and the qualification of every name are all decided by the server, so two
// views written differently and meaning the same come back identical. It is
// asked without the pretty form on purpose — pretty printing is a layout
// PostgreSQL has changed between releases, and a model that carried it would
// report a difference between two servers holding the same view.
//
// The check option is read separately because it is not in the query. WITH
// CHECK OPTION is stored beside the view, in reloptions, and pg_get_viewdef
// does not mention it — so a reader that asks only for the definition drops the
// clause that decides whether a write through the view is refused.
//
// The rest of reloptions is read as well, and security_barrier is why that is
// not cosmetic: a view declared with it refuses to let a cheap function see the
// rows the view was meant to hide, and a copy made without it answers questions
// the original refused. security_invoker, from PostgreSQL 15, decides whose
// rights the view reads with. Both are a change of security posture produced by
// a copy of a structure, which is the kind of difference this model exists to
// carry rather than to lose.
//
// relkind 'v' is a view. A materialised view ('m') is a different object with
// storage of its own, and it is not compared by this version; it is left out
// here rather than read as a view, because reading one as a view would produce
// DDL that creates the wrong kind of object.
const listViews = `SELECT c.relname,
	       pg_catalog.pg_get_viewdef(c.oid),
	       o.option_value,
	       c.reloptions
	FROM pg_catalog.pg_class c
	JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
	LEFT JOIN LATERAL pg_catalog.pg_options_to_table(c.reloptions) o
	       ON o.option_name OPERATOR(pg_catalog.=) 'check_option'
	WHERE n.nspname OPERATOR(pg_catalog.=) $1
	  AND c.relkind OPERATOR(pg_catalog.=) 'v'`

func (r *Reader) views(ctx context.Context, schema Name) ([]View, error) {
	rows := r.server.Query(ctx, listViews, schema.String())
	defer rows.Close()

	var views []View

	for rows.Next() {
		var (
			name, definition string
			checkOption      *string
			options          []string
		)

		if err := rows.Scan(&name, &definition, &checkOption, &options); err != nil {
			return nil, fmt.Errorf("reading a view of %s: %w", schema, err)
		}

		views = append(views, View{
			Name:        NewName(name),
			Definition:  viewQuery(definition),
			Options:     besidesCheckOption(options),
			CheckOption: text(checkOption),
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the views of %s: %w", schema, err)
	}

	return views, nil
}

// besidesCheckOption drops the check option from the storage parameters, which
// is where the catalog keeps it and where the model does not.
//
// It has a field of its own because it changes what a write through the view is
// allowed to do, which is worth more than a line in a list. Leaving it in both
// places would have the writer emit it twice — once in the WITH and once as the
// clause — and have the diff report one change as two.
func besidesCheckOption(options []string) []string {
	var kept []string

	for _, option := range options {
		if !strings.HasPrefix(option, "check_option=") {
			kept = append(kept, option)
		}
	}

	return kept
}

// viewQuery is the definition without the punctuation the server wraps it in.
//
// pg_get_viewdef answers a leading space and a trailing semicolon, and neither
// is part of the query: what the model holds is what goes after CREATE VIEW x
// AS, and the writer supplies the terminator. Keeping them would put the same
// query in two forms — one read from the server and one written by hand for a
// comparison target — which is a difference on a view nobody touched.
func viewQuery(rendered string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rendered), ";"))
}
