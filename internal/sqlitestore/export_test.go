package sqlitestore

import (
	"context"
	"fmt"
)

// The two ways a file can be wrong that nothing in the package can produce: a
// version from a build that does not exist yet, and a row somebody edited by
// hand or a future schema left behind. Both are states the application has to
// survive, and neither can be reached through the API — so they are reached
// from here, which is compiled into the test binary and nothing else.

// SetVersion writes the schema version of the file.
func (s *Store) SetVersion(ctx context.Context, to int) error {
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", to)); err != nil {
		return fmt.Errorf("setting the version of %s: %w", s.path, err)
	}

	return nil
}

// WriteState writes the state column of a row, whatever it says.
func (s *Store) WriteState(ctx context.Context, id, state string) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE jobs SET state = ? WHERE id = ?", state, id); err != nil {
		return fmt.Errorf("writing the state of job %s in %s: %w", id, s.path, err)
	}

	return nil
}

// QueryIntForTest answers a single number the database is asked for, so that a
// case can ask what a connection was born with rather than what this package
// believes it set.
func (s *Store) QueryIntForTest(ctx context.Context, query string, into *int) error {
	if err := s.db.QueryRowContext(ctx, query).Scan(into); err != nil {
		return fmt.Errorf("asking %s: %w", s.path, err)
	}

	return nil
}
