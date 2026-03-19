package store

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/rs/zerolog/log"
)

//go:embed migrations
var migrationsFS embed.FS

// RunMigrations applies all pending database migrations from the embedded
// migrations/ directory. It is safe to call on every startup — already-applied
// migrations are skipped.
func RunMigrations(dsn string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: migrations source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("store: migrate new: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("store: migrate up: %w", err)
	}

	version, dirty, _ := m.Version()
	log.Info().Uint("schema_version", version).Bool("dirty", dirty).Msg("database migrations applied")
	return nil
}
