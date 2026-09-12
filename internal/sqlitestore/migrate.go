package sqlitestore

import (
	"context"
	"fmt"
)

// migrations is the schema, one step per version, applied in order.
//
// A ladder from the very first version rather than a CREATE TABLE that grows:
// this is the first local schema the product has, and the backup catalogue is
// going into the same file. Inventing the mechanism now, while there is one
// step and nothing to lose, costs a function; inventing it later, with a
// year of somebody's history in the file, costs a migration written under
// pressure against data nobody can reproduce.
//
// A step is never edited once it has shipped. The version a file carries says
// which steps have run, and changing one of them underneath would leave two
// machines at the same version with different tables.
var migrations = []string{
	`CREATE TABLE jobs (
		id         TEXT    PRIMARY KEY,
		kind       TEXT    NOT NULL,
		title      TEXT    NOT NULL,
		state      TEXT    NOT NULL,
		started_at INTEGER,
		ended_at   INTEGER NOT NULL,
		error      TEXT    NOT NULL DEFAULT '',
		log        TEXT    NOT NULL DEFAULT '',
		dropped    INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX jobs_ended_at ON jobs (ended_at DESC, id DESC);`,
}

// migrate brings the file up to the schema this build expects.
//
// The version lives in PRAGMA user_version, which SQLite keeps in the header
// of the file itself: a table of our own would need a migration to create and
// would beg the question of how to read it before it exists.
func (s *Store) migrate(ctx context.Context) error {
	version, err := s.version(ctx)
	if err != nil {
		return err
	}

	if version > len(migrations) {
		return fmt.Errorf("%w: %s is at version %d and this build knows %d",
			ErrFromTheFuture, s.path, version, len(migrations))
	}

	for step, statements := range migrations[version:] {
		if err := s.step(ctx, version+step+1, statements); err != nil {
			return err
		}
	}

	return nil
}

// step applies one migration and records that it ran, both or neither.
//
// In a transaction because SQLite can roll back a CREATE TABLE: a migration
// interrupted half way through would otherwise leave a file at a version whose
// tables are not all there, which is the one state no later step can repair.
func (s *Store) step(ctx context.Context, to int, statements string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrating %s to version %d: %w", s.path, to, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, statements); err != nil {
		return fmt.Errorf("migrating %s to version %d: %w", s.path, to, err)
	}

	// The version cannot be a parameter: PRAGMA does not take one. It is an
	// int that this file decides and nothing outside it can reach, so there is
	// no string here that came from anywhere but the loop above.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", to)); err != nil {
		return fmt.Errorf("recording version %d of %s: %w", to, s.path, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing version %d of %s: %w", to, s.path, err)
	}

	return nil
}

// version answers how much of the ladder this file has climbed.
//
// It is also where a file that is not a database announces itself: reading the
// header is the first statement that touches the bytes, so a file of something
// else fails here rather than at the first job somebody runs.
func (s *Store) version(ctx context.Context) (int, error) {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("reading the version of %s: %w", s.path, err)
	}

	return version, nil
}
