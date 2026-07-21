package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type storeTestCase struct {
	name  string
	store AgentStore
}

func memoryStoreFixture(t *testing.T) AgentStore {
	t.Helper()
	return NewMemoryStore()
}

// storeTestFixtures returns all available store implementations for testing.
func storeTestFixtures(t *testing.T) []storeTestCase {
	t.Helper()

	var cases []storeTestCase

	// In-memory store is always available.
	cases = append(cases, storeTestCase{
		name:  "memory",
		store: memoryStoreFixture(t),
	})

	// MySQL integration test (requires MYSQL_DSN env var).
	if dsn := os.Getenv("MYSQL_DSN"); dsn != "" {
		s, err := NewMySQLStore(context.Background(), dsn)
		if err != nil {
			t.Logf("skipping MySQL: %v", err)
		} else {
			t.Cleanup(func() { _ = s.Close() })
			cases = append(cases, storeTestCase{name: "mysql", store: s})
		}
	}

	// PostgreSQL integration test (requires POSTGRES_DSN env var).
	if dsn := os.Getenv("POSTGRES_DSN"); dsn != "" {
		s, err := NewPostgresStore(context.Background(), dsn)
		if err != nil {
			t.Logf("skipping Postgres: %v", err)
		} else {
			t.Cleanup(func() { _ = s.Close() })
			cases = append(cases, storeTestCase{name: "postgres", store: s})
		}
	}

	// Redis integration test (requires REDIS_DSN env var).
	if dsn := os.Getenv("REDIS_DSN"); dsn != "" {
		s, err := NewRedisStore(dsn)
		if err != nil {
			t.Logf("skipping Redis: %v", err)
		} else {
			t.Cleanup(func() { _ = s.Close() })
			cases = append(cases, storeTestCase{name: "redis", store: s})
		}
	}

	return cases
}

// given a clean store, the key should not exist.
func TestStore_Load_when_keyNotFound_returnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storeTestFixtures(t) {
		t.Run(tc.name, func(t *testing.T) {
			var dest any
			err := tc.store.Load(ctx, "nonexistent", &dest)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrNotFound),
				"expected ErrNotFound, got %v", err)
		})
	}
}

// given a saved value, Load should return it.
func TestStore_Save_and_Load_when_validValue_roundTrips(t *testing.T) {
	ctx := context.Background()
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	for _, tc := range storeTestFixtures(t) {
		t.Run(tc.name, func(t *testing.T) {
			key := fmt.Sprintf("test_key_%s", tc.name)
			orig := payload{Name: "agent1", Count: 42}
			require.NoError(t, tc.store.Save(ctx, key, orig))

			var loaded payload
			require.NoError(t, tc.store.Load(ctx, key, &loaded))
			require.Equal(t, orig, loaded)

			// Cleanup.
			require.NoError(t, tc.store.Delete(ctx, key))
		})
	}
}

// given two saved values, LoadAll should return both.
func TestStore_LoadAll_when_multipleValues_returnsAll(t *testing.T) {
	ctx := context.Background()
	type payload struct {
		Name string `json:"name"`
		ID   int    `json:"id"`
	}

	for _, tc := range storeTestFixtures(t) {
		t.Run(tc.name, func(t *testing.T) {
			p1 := payload{Name: "a", ID: 1}
			p2 := payload{Name: "b", ID: 2}
			require.NoError(t, tc.store.Save(ctx, "key1", p1))
			require.NoError(t, tc.store.Save(ctx, "key2", p2))

			all, err := tc.store.LoadAll(ctx)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(all), 2)

			// Cleanup.
			require.NoError(t, tc.store.Delete(ctx, "key1"))
			require.NoError(t, tc.store.Delete(ctx, "key2"))
		})
	}
}

// given a deleted key, Load should return ErrNotFound.
func TestStore_Delete_when_keyExists_removesIt(t *testing.T) {
	ctx := context.Background()

	for _, tc := range storeTestFixtures(t) {
		t.Run(tc.name, func(t *testing.T) {
			key := fmt.Sprintf("del_key_%s", tc.name)
			require.NoError(t, tc.store.Save(ctx, key, map[string]string{"x": "y"}))

			require.NoError(t, tc.store.Delete(ctx, key))

			var dest any
			err := tc.store.Load(ctx, key, &dest)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrNotFound))
		})
	}
}

// given Save with an invalid interface (channel), it should return an error.
func TestStore_Save_when_unmarshalableValue_returnsError(t *testing.T) {
	ctx := context.Background()

	for _, tc := range storeTestFixtures(t) {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.store.Save(ctx, "bad", make(chan int))
			require.Error(t, err)
		})
	}
}

// TestStoreError_wrap tests the StoreError type.
func TestStoreError_wrap(t *testing.T) {
	err := wrapErr("save", ErrNotFound)
	var se *StoreError
	require.True(t, errors.As(err, &se))
	require.Equal(t, "save", se.Op)
	require.True(t, errors.Is(err, ErrNotFound))
}

// TestMigrations_Run tests migration SQL execution.
func TestMigrations_Run(t *testing.T) {
	// Use in-memory SQLite via a separate approach for migrations test.
	// We only test that the SQL is syntactically valid by constructing the migrations.
	m := NewDBMigrations()
	require.NotNil(t, m)
	require.Len(t, m.migrations, 1)
	require.Equal(t, "001_create_agent_configs", m.migrations[0].Name)
	require.Contains(t, m.migrations[0].SQL, "CREATE TABLE IF NOT EXISTS agent_configs")
}

// TestMigrations_Run_integration tests Run against a real DB when available.
func TestMigrations_Run_integration(t *testing.T) {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = os.Getenv("POSTGRES_DSN")
	}
	if dsn == "" {
		t.Skip("no DB DSN set")
	}

	driver := "mysql"
	if os.Getenv("POSTGRES_DSN") != "" {
		driver = "postgres"
	}

	ctx := context.Background()
	db, err := sql.Open(driver, dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, db.PingContext(ctx))

	m := NewDBMigrations()
	require.NoError(t, m.Run(ctx, db))

	// Run again to test idempotency (IF NOT EXISTS).
	require.NoError(t, m.Run(ctx, db))

	// Verify table exists by inserting and querying.
	_, err = db.ExecContext(ctx, `INSERT INTO agent_configs (id, data) VALUES ('test_mig', '{}')`)
	require.NoError(t, err)

	var data string
	err = db.QueryRowContext(ctx, `SELECT data FROM agent_configs WHERE id = 'test_mig'`).Scan(&data)
	require.NoError(t, err)
	require.Equal(t, "{}", data)

	// Cleanup.
	_, err = db.ExecContext(ctx, `DELETE FROM agent_configs WHERE id = 'test_mig'`)
	require.NoError(t, err)
}
