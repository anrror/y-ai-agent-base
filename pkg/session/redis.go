package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gomodule/redigo/redis"
)

// RedisStore is a Redis-backed session store with TTL via EXPIRE.
type RedisStore struct {
	pool *redis.Pool
	ttl  time.Duration
}

// RedisStoreOption configures the RedisStore.
type RedisStoreOption func(*RedisStore)

// WithRedisTTL sets the TTL for stored sessions.
func WithRedisTTL(d time.Duration) RedisStoreOption {
	return func(s *RedisStore) {
		s.ttl = d
	}
}

// NewRedisStore creates a Redis-backed session store from a redigo pool.
// Default TTL: 30 minutes.
func NewRedisStore(pool *redis.Pool, opts ...RedisStoreOption) *RedisStore {
	s := &RedisStore{
		pool: pool,
		ttl:  30 * time.Minute,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// NewRedisPool creates a self-contained Redis connection pool.
// addr is "host:port" (e.g. "localhost:6379").
func NewRedisPool(addr string, maxIdle, maxActive int) *redis.Pool {
	return &redis.Pool{
		MaxIdle:     maxIdle,
		MaxActive:   maxActive,
		IdleTimeout: 240 * time.Second,
		Wait:        true,
		Dial: func() (redis.Conn, error) {
			return redis.Dial("tcp", addr)
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			if err != nil {
				return fmt.Errorf("redis ping: %w", err)
			}
			return nil
		},
	}
}

func sessionKey(id string) string {
	return "session:" + id
}

// Get retrieves a session from Redis. Returns ErrNotFound if absent.
func (s *RedisStore) Get(ctx context.Context, id string) (*Session, error) {
	conn, err := s.pool.GetContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("redis get conn: %w", err)
	}
	defer func() { _ = conn.Close() }()

	data, err := redis.Bytes(conn.Do("GET", sessionKey(id)))
	if err != nil {
		if errors.Is(err, redis.ErrNil) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("redis get: %w", err)
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("redis unmarshal: %w", err)
	}
	return &session, nil
}

// Set upserts a session into Redis with TTL.
func (s *RedisStore) Set(ctx context.Context, session *Session) error {
	conn, err := s.pool.GetContext(ctx)
	if err != nil {
		return fmt.Errorf("redis get conn: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now()
	}
	session.UpdatedAt = time.Now()

	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("redis marshal: %w", err)
	}

	ttlSeconds := int(s.ttl.Seconds())
	if _, err := conn.Do("SETEX", sessionKey(session.ID), ttlSeconds, data); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}
	return nil
}

// Delete removes a session from Redis. Idempotent.
func (s *RedisStore) Delete(ctx context.Context, id string) error {
	conn, err := s.pool.GetContext(ctx)
	if err != nil {
		return fmt.Errorf("redis get conn: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Do("DEL", sessionKey(id)); err != nil {
		return fmt.Errorf("redis delete: %w", err)
	}
	return nil
}

// Close releases the Redis connection pool.
func (s *RedisStore) Close() error {
	if err := s.pool.Close(); err != nil {
		return fmt.Errorf("redis close: %w", err)
	}
	return nil
}
