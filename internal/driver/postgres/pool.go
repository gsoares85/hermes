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
		return nil, fmt.Errorf("opening a pool for %s:%d: %w", target.Host, target.Port, err)
	}

	return &connPool{pool: pool}, nil
}

func poolConfig(target driver.Target) (*pgxpool.Config, error) {
	// Parsed from an empty string to get the libpq defaults, then filled in
	// field by field. The password never becomes part of a connection string:
	// a string is what ends up quoted into a log or an error message.
	config, err := pgxpool.ParseConfig("")
	if err != nil {
		return nil, fmt.Errorf("building the pool configuration: %w", err)
	}

	if target.Port < 1 || target.Port > 65535 {
		return nil, fmt.Errorf("%w: port %d is outside 1-65535", ErrUnsupported, target.Port)
	}

	config.ConnConfig.Host = target.Host
	config.ConnConfig.Port = uint16(target.Port)
	config.ConnConfig.Database = target.Database
	config.ConnConfig.User = target.User
	config.ConnConfig.Password = target.Password

	if err := applyTLS(config, target.SSLMode); err != nil {
		return nil, err
	}

	if len(target.Params) > 0 {
		config.ConnConfig.RuntimeParams = make(map[string]string, len(target.Params))
		for key, value := range target.Params {
			config.ConnConfig.RuntimeParams[key] = value
		}
	}

	config.MaxConns = maxConns
	// Zero on purpose: with a minimum above zero the pool would dial as soon
	// as it is built, which is exactly the eager connect Open promises not to do.
	config.MinConns = 0
	config.MaxConnIdleTime = maxConnIdleTime
	config.MaxConnLifetime = maxConnLifetime

	return config, nil
}

// applyTLS honours the two modes that need no certificate handling and refuses
// the rest. Verification arrives with the TLS step of this task; until then an
// unsupported mode is an error, never a silent downgrade to a weaker one.
func applyTLS(config *pgxpool.Config, mode string) error {
	switch mode {
	case "", "prefer":
		return nil
	case "disable":
		config.ConnConfig.TLSConfig = nil
		return nil
	default:
		return fmt.Errorf("%w: sslmode %q is not wired yet", ErrUnsupported, mode)
	}
}

// connPool adapts pgxpool to the engine contract. It exists so that no pgx type
// is ever returned across the seam.
type connPool struct {
	pool *pgxpool.Pool
}

func (p *connPool) Ping(ctx context.Context) error {
	if err := p.pool.Ping(ctx); err != nil {
		return fmt.Errorf("reaching the server: %w", err)
	}

	return nil
}

func (p *connPool) ServerVersion(ctx context.Context) (string, error) {
	var version string
	if err := p.pool.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		return "", fmt.Errorf("reading the server version: %w", err)
	}

	return version, nil
}

func (p *connPool) Close() {
	p.pool.Close()
}
