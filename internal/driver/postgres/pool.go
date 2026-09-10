// Package postgres implements the engine contract with pgx v5.
//
// It is the only package in the repository allowed to import pgx: everything
// above it programs against internal/driver, which is what keeps the core
// layer testable without a database. See ADR-0009 and ADR-0006.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gsoares85/hermes/internal/driver"
)

// ErrUnsupported is returned for a target this implementation cannot honour
// yet. It exists so that an unsupported setting fails loudly instead of being
// dropped, which for a security setting would mean quietly connecting with less
// protection than the user asked for.
var ErrUnsupported = errors.New("unsupported connection setting")

// Pool sizing. Deliberately small: the resting memory budget is 200MB with
// three connections open, and a desktop client that idles on a handful of
// connections has no reason to hold more.
const (
	maxConns        = 4
	maxConnIdleTime = 2 * time.Minute
	maxConnLifetime = 30 * time.Minute
)

// Opener builds pgx-backed pools.
type Opener struct{}

// New returns the engine implementation to hand to the core layer.
func New() driver.Opener {
	return Opener{}
}

// Open builds a pool for the target without connecting to it.
//
// Nothing here reaches the network. A saved connection pointing at a host that
// is down must fail when someone uses it, not when the window opens, and the
// cold-start budget depends on that. Ping is what actually connects.
func (Opener) Open(ctx context.Context, target driver.Target) (driver.Pool, error) {
	config, err := poolConfig(target)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, classify(fmt.Errorf("opening a pool for %s:%d: %w", target.Host, target.Port, err))
	}

	return &connPool{pool: pool}, nil
}

func poolConfig(target driver.Target) (*pgxpool.Config, error) {
	if target.Port < 1 || target.Port > 65535 {
		return nil, fmt.Errorf("%w: port %d is outside 1-65535", ErrUnsupported, target.Port)
	}

	settings, err := connString(target)
	if err != nil {
		return nil, err
	}

	config, err := pgxpool.ParseConfig(settings)
	if err != nil {
		// The string is built here and carries no secret, so quoting it back
		// is what makes a bad certificate path findable.
		return nil, fmt.Errorf("%w: %s: %w", ErrUnsupported, settings, err)
	}

	// Set on the parsed configuration rather than in the string above, which
	// is the whole reason the string is built without it.
	//
	// Only when there is one: ParseConfig has already resolved a password from
	// PGPASSWORD or from .pgpass, and overwriting that with an empty form field
	// would turn off the one way of connecting that keeps no secret inside the
	// application.
	if target.Password != "" {
		config.ConnConfig.Password = target.Password
	}

	// Merged, not replaced. ParseConfig derives runtime parameters of its own
	// from PGAPPNAME and PGOPTIONS, and assigning a fresh map would discard
	// them without saying so.
	for key, value := range target.Params {
		if config.ConnConfig.RuntimeParams == nil {
			config.ConnConfig.RuntimeParams = make(map[string]string, len(target.Params))
		}
		config.ConnConfig.RuntimeParams[key] = value
	}

	config.MaxConns = maxConns
	// Zero on purpose: with a minimum above zero the pool would dial as soon
	// as it is built, which is exactly the eager connect Open promises not to do.
	config.MinConns = 0
	config.MaxConnIdleTime = maxConnIdleTime
	config.MaxConnLifetime = maxConnLifetime

	return config, nil
}

// connPool adapts pgxpool to the engine contract. It exists so that no pgx type
// is ever returned across the seam.
type connPool struct {
	pool *pgxpool.Pool
}

// Ping is where a connection is actually made, so it is where a failure gets
// classified. The class is what the layer above turns into something readable;
// the driver error stays underneath for whoever can read it.
func (p *connPool) Ping(ctx context.Context) error {
	if err := p.pool.Ping(ctx); err != nil {
		return classify(fmt.Errorf("reaching the server: %w", err))
	}

	return nil
}

func (p *connPool) ServerVersion(ctx context.Context) (string, error) {
	var version string
	if err := p.pool.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		return "", classify(fmt.Errorf("reading the server version: %w", err))
	}

	return version, nil
}

func (p *connPool) Close() {
	p.pool.Close()
}

// listDatabases asks the catalog which databases this role may open.
//
// datallowconn excludes the ones the server itself refuses; datistemplate drops
// template0 and template1, which are there to be copied rather than opened; and
// has_database_privilege is what makes the answer specific to who is asking
// instead of a list of names most of which would be refused.
//
// Every name is written with pg_catalog in front of it, and the function is the
// reason rather than the relation. pg_catalog.has_database_privilege takes
// (name, text, text), so a has_database_privilege(name, name, text) declared in
// any schema on the search path is an exact match and wins outright — no
// ordering required. It would then decide, as the role that just connected,
// which databases that role is told it may open. That is CVE-2018-1058, and
// this is the first SELECT of every browsing session: the root of the object
// tree is drawn from whatever it answers.
//
// current_user stays bare because it is not a name at all: the parser turns the
// keyword into a value expression, so there is nothing for a schema to shadow
// and nothing to qualify — pg_catalog.current_user is a syntax error.
const listDatabases = `SELECT d.datname FROM pg_catalog.pg_database d
	WHERE d.datallowconn AND NOT d.datistemplate
	  AND pg_catalog.has_database_privilege(current_user, d.datname, 'CONNECT')
	ORDER BY d.datname`

func (p *connPool) Databases(ctx context.Context) ([]string, error) {
	rows, err := p.pool.Query(ctx, listDatabases)
	if err != nil {
		return nil, classify(fmt.Errorf("listing the databases: %w", err))
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("reading a database name: %w", err)
		}
		databases = append(databases, name)
	}

	if err := rows.Err(); err != nil {
		return nil, classify(fmt.Errorf("listing the databases: %w", err))
	}

	return databases, nil
}
