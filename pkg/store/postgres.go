package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	// PostgreSQL driver registration.
	// The blank import also registers pgvector support if the extension is installed.
	_ "github.com/lib/pq"
)

// PostgresStore is an AgentStore backed by a PostgreSQL database.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore opens a PostgreSQL connection and runs migrations.
func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, wrapErr("open", err)
	}
	if err := db.PingContext(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("postgres: close after ping failure", "error", closeErr)
		}
		return nil, wrapErr("ping", err)
	}
	m := NewDBMigrations()
	if err := m.Run(ctx, db); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("postgres: close after migration failure", "error", closeErr)
		}
		return nil, err
	}
	return &PostgresStore{db: db}, nil
}

// Save upserts a JSON value into agent_configs.
func (p *PostgresStore) Save(ctx context.Context, key string, value any) error {
	data, err := Marshal(value)
	if err != nil {
		return wrapErr("save", err)
	}
	_, err = p.db.ExecContext(
		ctx,
		`INSERT INTO agent_configs (id, data) VALUES ($1, $2)
		 ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data, updated_at = CURRENT_TIMESTAMP`,
		key, string(data),
	)
	return wrapErr("save", err)
}

// Load retrieves a JSON value from agent_configs by id.
func (p *PostgresStore) Load(ctx context.Context, key string, dest any) error {
	var raw string
	err := p.db.QueryRowContext(
		ctx,
		`SELECT data FROM agent_configs WHERE id = $1`, key,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return wrapErr("load", ErrNotFound)
	}
	if err != nil {
		return wrapErr("load", err)
	}
	if err := Unmarshal([]byte(raw), dest); err != nil {
		return wrapErr("load", err)
	}
	return nil
}

// LoadAll returns all rows from agent_configs.
func (p *PostgresStore) LoadAll(ctx context.Context) ([]any, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT data FROM agent_configs`)
	if err != nil {
		return nil, wrapErr("loadall", err)
	}
	defer func() { _ = rows.Close() }()

	var results []any
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, wrapErr("loadall", err)
		}
		var v any
		if err := Unmarshal([]byte(raw), &v); err != nil {
			return nil, wrapErr("loadall", err)
		}
		results = append(results, v)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapErr("loadall", err)
	}
	return results, nil
}

// Delete removes a row by id.
func (p *PostgresStore) Delete(ctx context.Context, key string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM agent_configs WHERE id = $1`, key)
	return wrapErr("delete", err)
}

// Close closes the database connection.
func (p *PostgresStore) Close() error {
	if err := p.db.Close(); err != nil {
		return fmt.Errorf("postgres close: %w", err)
	}
	return nil
}
