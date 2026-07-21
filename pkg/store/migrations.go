package store

import (
	"context"
	"database/sql"
	"fmt"
)

// DBMigrations holds DDL statements for key-value store tables.
type DBMigrations struct {
	migrations []Migration
}

// Migration is a single schema change.
type Migration struct {
	Name string
	SQL  string
}

// NewDBMigrations returns migrations for agent_configs table.
func NewDBMigrations() *DBMigrations {
	return &DBMigrations{
		migrations: []Migration{
			{
				Name: "001_create_agent_configs",
				SQL: `CREATE TABLE IF NOT EXISTS agent_configs (
					id         VARCHAR(255) NOT NULL PRIMARY KEY,
					data       JSON NOT NULL,
					created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
				)`,
			},
		},
	}
}

// Run executes all migrations against db.
func (m *DBMigrations) Run(ctx context.Context, db *sql.DB) error {
	for _, mig := range m.migrations {
		if _, err := db.ExecContext(ctx, mig.SQL); err != nil {
			return fmt.Errorf("store: migration %s: %w", mig.Name, err)
		}
	}
	return nil
}
