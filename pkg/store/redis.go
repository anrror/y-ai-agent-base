package store

import (
	"context"
	"fmt"

	"github.com/gomodule/redigo/redis"
)

// RedisStore is an AgentStore backed by Redis.
type RedisStore struct {
	pool *redis.Pool
}

// NewRedisStore creates a RedisStore with a connection pool.
func NewRedisStore(dsn string) (*RedisStore, error) {
	pool := &redis.Pool{
		Dial: func() (redis.Conn, error) {
			return redis.DialURL(dsn)
		},
	}
	conn := pool.Get()
	defer func() { _ = conn.Close() }()
	if _, err := conn.Do("PING"); err != nil {
		_ = pool.Close()
		return nil, wrapErr("ping", err)
	}
	return &RedisStore{pool: pool}, nil
}

// Save stores a JSON value in Redis under key.
func (r *RedisStore) Save(ctx context.Context, key string, value any) error {
	data, err := Marshal(value)
	if err != nil {
		return wrapErr("save", err)
	}
	conn := r.pool.Get()
	defer func() { _ = conn.Close() }()
	if _, err := conn.Do("SET", key, string(data)); err != nil {
		return wrapErr("save", err)
	}
	return nil
}

// Load retrieves a JSON value from Redis and unmarshalls into dest.
func (r *RedisStore) Load(ctx context.Context, key string, dest any) error {
	conn := r.pool.Get()
	defer func() { _ = conn.Close() }()
	reply, err := conn.Do("GET", key)
	if err != nil {
		return wrapErr("load", err)
	}
	if reply == nil {
		return wrapErr("load", ErrNotFound)
	}
	data, err := redis.Bytes(reply, nil)
	if err != nil {
		return wrapErr("load", fmt.Errorf("redis: unexpected reply type: %w", err))
	}
	if err := Unmarshal(data, dest); err != nil {
		return wrapErr("load", err)
	}
	return nil
}

// LoadAll returns all values stored in Redis.
// It scans all keys using SCAN and retrieves each value.
func (r *RedisStore) LoadAll(ctx context.Context) ([]any, error) {
	conn := r.pool.Get()
	defer func() { _ = conn.Close() }()

	var cursor int64
	var results []any
	for {
		select {
		case <-ctx.Done():
			return nil, wrapErr("loadall", ctx.Err())
		default:
		}

		reply, err := conn.Do("SCAN", cursor, "MATCH", "*", "COUNT", 100)
		if err != nil {
			return nil, wrapErr("loadall", err)
		}
		values, err := redis.Values(reply, nil)
		if err != nil {
			return nil, wrapErr("loadall", fmt.Errorf("redis: unexpected scan reply: %w", err))
		}
		if len(values) != 2 {
			return nil, wrapErr("loadall", fmt.Errorf("redis: unexpected scan result length: %d", len(values)))
		}
		cursor, err = redis.Int64(values[0], nil)
		if err != nil {
			return nil, wrapErr("loadall", err)
		}
		keys, err := redis.Strings(values[1], nil)
		if err != nil {
			return nil, wrapErr("loadall", err)
		}
		for _, k := range keys {
			raw, err := redis.Bytes(conn.Do("GET", k))
			if err != nil {
				return nil, wrapErr("loadall", err)
			}
			var v any
			if err := Unmarshal(raw, &v); err != nil {
				return nil, wrapErr("loadall", err)
			}
			results = append(results, v)
		}
		if cursor == 0 {
			break
		}
	}
	return results, nil
}

// Delete removes a key from Redis.
func (r *RedisStore) Delete(ctx context.Context, key string) error {
	conn := r.pool.Get()
	defer func() { _ = conn.Close() }()
	if _, err := conn.Do("DEL", key); err != nil {
		return wrapErr("delete", err)
	}
	return nil
}

// Close closes the Redis connection pool.
func (r *RedisStore) Close() error {
	if err := r.pool.Close(); err != nil {
		return fmt.Errorf("redis close: %w", err)
	}
	return nil
}
