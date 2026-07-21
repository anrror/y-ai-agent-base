package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	// MySQL driver registration.
	_ "github.com/go-sql-driver/mysql"
)

// MySQLStore is an AgentStore backed by a MySQL database.
type MySQLStore struct {
	db *sql.DB
}

// NewMySQLStore opens a MySQL connection and runs migrations.
func NewMySQLStore(ctx context.Context, dsn string) (*MySQLStore, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, wrapErr("open", err)
	}
	if err := db.PingContext(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("mysql: close after ping failure", "error", closeErr)
		}
		return nil, wrapErr("ping", err)
	}
	m := NewDBMigrations()
	if err := m.Run(ctx, db); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("mysql: close after migration failure", "error", closeErr)
		}
		return nil, err
	}
	return &MySQLStore{db: db}, nil
}

// Save upserts a JSON value into agent_configs.
func (m *MySQLStore) Save(ctx context.Context, key string, value any) error {
	data, err := Marshal(value)
	if err != nil {
		return wrapErr("save", err)
	}
	_, err = m.db.ExecContext(
		ctx,
		`INSERT INTO agent_configs (id, data) VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE data = VALUES(data), updated_at = CURRENT_TIMESTAMP`,
		key, string(data),
	)
	return wrapErr("save", err)
}

// Load retrieves a JSON value from agent_configs by id.
func (m *MySQLStore) Load(ctx context.Context, key string, dest any) error {
	var raw string
	err := m.db.QueryRowContext(
		ctx,
		`SELECT data FROM agent_configs WHERE id = ?`, key,
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
func (m *MySQLStore) LoadAll(ctx context.Context) ([]any, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT data FROM agent_configs`)
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
func (m *MySQLStore) Delete(ctx context.Context, key string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM agent_configs WHERE id = ?`, key)
	return wrapErr("delete", err)
}

// Close closes the database connection.
func (m *MySQLStore) Close() error {
	if err := m.db.Close(); err != nil {
		return fmt.Errorf("mysql close: %w", err)
	}
	return nil
}
