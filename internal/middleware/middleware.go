package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/anrror/y-ai-agent-base/pkg/types"
)

// Middleware holds configuration for all Gin middleware.
type Middleware struct {
	JWTSecret     string
	CORSConfig    CORSConfig
	RateLimitCfg  RateLimitConfig
	TelemetryHook TelemetryHook
	Logger        *slog.Logger
}

// CORSConfig controls Cross-Origin Resource Sharing.
type CORSConfig struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
}

// RateLimitConfig controls per-IP rate limiting.
type RateLimitConfig struct {
	Enabled        bool
	RequestsPerMin int
	Burst          int
}

// New creates a Middleware group with sensible defaults.
func New(jwtSecret string, telemetry TelemetryHook) *Middleware {
	return &Middleware{
		JWTSecret:     jwtSecret,
		TelemetryHook: telemetry,
		CORSConfig: CORSConfig{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders: []string{"Origin", "Content-Type", "Authorization", "X-Request-ID"},
		},
		RateLimitCfg: RateLimitConfig{
			Enabled:        true,
			RequestsPerMin: 100,
			Burst:          20,
		},
	}
}

// jwtClaims holds the standard and custom claims extracted from a JWT payload.
type jwtClaims struct {
	Subject string `json:"sub"`
	Expiry  int64  `json:"exp"`
	AgentID string `json:"agent_id,omitempty"`
}

// JWTAuth returns middleware that validates the Authorization: Bearer <token>
// header as a real HS256 JWT. It verifies the HMAC-SHA256 signature, checks
// the exp claim, and sets user_id / agent_id in the Gin context.
func (m *Middleware) JWTAuth() gin.HandlerFunc {
	if m.JWTSecret == "" {
		return func(c *gin.Context) { c.Next() }
	}

	secret := []byte(m.JWTSecret)

	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing Authorization header",
			})
			return
		}

		token, ok := parseBearer(auth)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid Authorization format, expected 'Bearer <token>'",
			})
			return
		}

		claims, err := validateJWT(token, secret)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": err.Error(),
			})
			return
		}

		c.Set(string(types.CtxUserID), claims.Subject)
		if claims.AgentID != "" {
			c.Set(string(types.CtxAgentID), claims.AgentID)
		}

		c.Next()
	}
}

// validateJWT parses and validates a JWT token string using HS256.
// It returns the extracted claims or an error describing the failure.
func validateJWT(token string, secret []byte) (*jwtClaims, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	headerB64 := parts[0]
	payloadB64 := parts[1]
	sigB64 := parts[2]

	// Decode header (validate it's JSON but we don't need its contents).
	_, err := decodeJWTPart(headerB64)
	if err != nil {
		return nil, fmt.Errorf("invalid token header")
	}

	// Decode payload.
	payloadJSON, err := decodeJWTPart(payloadB64)
	if err != nil {
		return nil, fmt.Errorf("invalid token payload")
	}

	var claims jwtClaims
	if err = json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("invalid token claims")
	}

	// Verify signature: HMAC-SHA256(header.payload, secret).
	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, fmt.Errorf("invalid token signature")
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(headerB64 + "." + payloadB64))
	expectedSig := mac.Sum(nil)

	if !hmac.Equal(sigBytes, expectedSig) {
		return nil, fmt.Errorf("invalid token signature")
	}

	// Verify expiration.
	if claims.Expiry == 0 {
		return nil, fmt.Errorf("token missing exp claim")
	}
	if time.Now().Unix() > claims.Expiry {
		return nil, fmt.Errorf("token expired")
	}

	// Subject is required.
	if claims.Subject == "" {
		return nil, fmt.Errorf("token missing sub claim")
	}

	return &claims, nil
}

// decodeJWTPart decodes a base64url-encoded (no padding) JWT segment.
func decodeJWTPart(part string) ([]byte, error) {
	// Add padding if needed.
	switch len(part) % 4 {
	case 2:
		part += "=="
	case 3:
		part += "="
	}
	data, err := base64.URLEncoding.DecodeString(part)
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}
	return data, nil
}

func parseBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return header[len(prefix):], true
}

// Recovery returns middleware that recovers from panics and logs them.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error(
					"request panic recovered",
					"panic", r,
					"path", c.Request.URL.Path,
					"method", c.Request.Method,
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
		}()
		c.Next()
	}
}

// RequestID returns middleware that ensures every request has a unique
// X-Request-ID header.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-ID")
		if rid == "" {
			rid = newRequestID()
		}
		c.Set(string(types.CtxRequestID), rid)
		c.Header("X-Request-ID", rid)
		c.Next()
	}
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// CORSHandler returns middleware that sets CORS headers from config.
func (m *Middleware) CORSHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := "*"
		if len(m.CORSConfig.AllowedOrigins) > 0 {
			origin = strings.Join(m.CORSConfig.AllowedOrigins, ", ")
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Methods", strings.Join(m.CORSConfig.AllowedMethods, ", "))
		c.Header("Access-Control-Allow-Headers", strings.Join(m.CORSConfig.AllowedHeaders, ", "))

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// maxPerIPBuckets caps the number of tracked client IPs to prevent unbounded
// memory growth under sustained traffic with diverse source IPs.
const maxPerIPBuckets = 100_000

// perIPBucketEvictRatio is the fraction of entries evicted when the map
// exceeds maxPerIPBuckets.
const perIPBucketEvictRatio = 0.1

// perIPBucket holds token buckets keyed by client IP.
type perIPBucket struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

func newPerIPBucket() *perIPBucket {
	return &perIPBucket{buckets: make(map[string]*tokenBucket)}
}

func (p *perIPBucket) allow(ip string, requestsPerMin int, burst int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	b, ok := p.buckets[ip]
	if !ok {
		// Evict oldest entries before adding a new one when at capacity.
		if len(p.buckets) >= maxPerIPBuckets {
			p.evictOldest()
		}
		b = &tokenBucket{tokens: float64(burst), lastRefill: time.Now()}
		p.buckets[ip] = b
	}

	rate := float64(requestsPerMin) / 60.0
	elapsed := time.Since(b.lastRefill).Seconds()
	b.tokens += elapsed * rate
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}
	b.lastRefill = time.Now()

	if b.tokens >= 1.0 {
		b.tokens--
		return true
	}
	return false
}

// evictOldest removes the oldest entries when the bucket map is full to
// prevent unbounded memory growth. It evicts evictRatio of the total entries.
func (p *perIPBucket) evictOldest() {
	n := len(p.buckets)
	target := int(float64(n) * perIPBucketEvictRatio)
	if target < 1 {
		target = 1
	}

	// Collect candidates with their timestamps and sort by age.
	type entry struct {
		ip    string
		stale time.Time
	}
	candidates := make([]entry, 0, n)
	for ip, b := range p.buckets {
		candidates = append(candidates, entry{ip: ip, stale: b.lastRefill})
	}
	// Partial sort: only need the oldest `target` entries.
	// Use simple sort since n <= maxPerIPBuckets = 100K.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].stale.Before(candidates[j].stale)
	})
	for i := 0; i < target && i < len(candidates); i++ {
		delete(p.buckets, candidates[i].ip)
	}
}

// RateLimitHandler returns middleware that rate-limits requests per IP.
func (m *Middleware) RateLimitHandler() gin.HandlerFunc {
	if !m.RateLimitCfg.Enabled {
		return func(c *gin.Context) { c.Next() }
	}

	bucket := newPerIPBucket()

	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !bucket.allow(ip, m.RateLimitCfg.RequestsPerMin, m.RateLimitCfg.Burst) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}
		c.Next()
	}
}

// Logging returns middleware that logs every request using the TelemetryHook.
func (m *Middleware) Logging() gin.HandlerFunc {
	return func(c *gin.Context) {
		if m.TelemetryHook != nil {
			m.TelemetryHook.BeforeRequest(c)
		}

		start := time.Now()
		c.Next()
		latency := time.Since(start)

		if m.TelemetryHook != nil {
			m.TelemetryHook.AfterRequest(c, latency)
		}
	}
}

// RequestTimeout returns a Gin middleware that applies a per-request timeout.
// It checks, in order:
//  1. X-Request-Timeout header (milliseconds as integer string, e.g. "5000")
//  2. X-Timeout header (same format)
//
// The resolved timeout is capped at maxTimeout. If none provided, maxTimeout
// is used as the default. A zero or negative maxTimeout disables the middleware.
//
// The deadline is stored in the request context; upstream handlers and the
// pipeline will inherit it automatically via c.Request.Context().
func RequestTimeout(maxTimeout time.Duration) gin.HandlerFunc {
	if maxTimeout <= 0 {
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		timeout := maxTimeout

		headerTimeout := c.GetHeader("X-Request-Timeout")
		if headerTimeout == "" {
			headerTimeout = c.GetHeader("X-Timeout")
		}
		if headerTimeout != "" {
			if ms, err := strconv.ParseInt(headerTimeout, 10, 64); err == nil && ms > 0 {
				d := time.Duration(ms) * time.Millisecond
				if d <= maxTimeout {
					timeout = d
				}
			}
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
