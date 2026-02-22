package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// --- Request Logging Middleware ---

// RequestLogging wraps a handler with structured request/response logging.
func RequestLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		clientIP := clientIPAddress(r)
		correlationID := r.Header.Get("X-Correlation-ID")
		if correlationID == "" {
			correlationID = r.Header.Get("X-Request-ID")
		}

		logger.Info("incoming request",
			"method", r.Method,
			"path", r.URL.Path,
			"client_ip", clientIP,
			"correlation_id", correlationID,
		)

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		logger.Info("outgoing response",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
			"correlation_id", correlationID,
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// --- Rate Limiting Middleware ---

// RateLimiter implements fixed-window per-client-IP rate limiting.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	limit   int
	window  time.Duration
}

type bucket struct {
	count    int
	resetAt  time.Time
}

// NewRateLimiter creates a rate limiter with the given per-window limit.
func NewRateLimiter(limit int, windowSeconds int) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*bucket),
		limit:   limit,
		window:  time.Duration(windowSeconds) * time.Second,
	}
}

// Middleware returns an http.Handler that enforces rate limiting.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIPAddress(r)

		if !rl.allow(ip) {
			http.Error(w, "Too many requests. Please try again later.", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok || now.After(b.resetAt) {
		rl.buckets[key] = &bucket{count: 1, resetAt: now.Add(rl.window)}
		return true
	}

	if b.count >= rl.limit {
		return false
	}

	b.count++
	return true
}

// --- CORS Middleware ---

// CORS returns middleware that handles Cross-Origin Resource Sharing.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin != "" {
				allowed := cfg.AllowAnyOrigin || len(cfg.AllowedOrigins) == 0
				if !allowed {
					for _, o := range cfg.AllowedOrigins {
						if strings.EqualFold(o, origin) {
							allowed = true
							break
						}
					}
				}

				if allowed {
					if cfg.AllowAnyOrigin {
						w.Header().Set("Access-Control-Allow-Origin", "*")
					} else {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						w.Header().Set("Vary", "Origin")
					}

					w.Header().Set("Access-Control-Allow-Methods", strings.Join(cfg.AllowedMethods, ", "))
					w.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowedHeaders, ", "))
				}
			}

			// Handle preflight.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// --- JWT Authentication Middleware ---

// JWTAuth returns middleware that validates JWT bearer tokens.
// It skips validation for paths in the skip list (e.g. /health).
func JWTAuth(cfg JWTConfig, skipPaths []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for configured paths.
			for _, p := range skipPaths {
				if strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// No secret configured = auth disabled.
			if cfg.SecretKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "missing or invalid authorization header", http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")
			if err := validateJWT(token, cfg); err != nil {
				http.Error(w, "invalid token: "+err.Error(), http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// validateJWT performs minimal HS256 JWT validation (signature, expiry, issuer, audience).
func validateJWT(tokenStr string, cfg JWTConfig) error {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return errInvalidToken
	}

	// Verify signature (HS256).
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(cfg.SecretKey))
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expectedSig), []byte(parts[2])) {
		return errInvalidSignature
	}

	// Decode payload.
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errInvalidToken
	}

	var claims struct {
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
		Aud string `json:"aud"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return errInvalidToken
	}

	// Check expiration.
	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return errTokenExpired
	}

	// Check issuer.
	if cfg.ValidateIssuer && cfg.Issuer != "" && claims.Iss != cfg.Issuer {
		return errInvalidIssuer
	}

	// Check audience.
	if cfg.ValidateAudience && cfg.Audience != "" && claims.Aud != cfg.Audience {
		return errInvalidAudience
	}

	return nil
}

type jwtError string

func (e jwtError) Error() string { return string(e) }

const (
	errInvalidToken     = jwtError("invalid token format")
	errInvalidSignature = jwtError("invalid signature")
	errTokenExpired     = jwtError("token expired")
	errInvalidIssuer    = jwtError("invalid issuer")
	errInvalidAudience  = jwtError("invalid audience")
)

// --- Helpers ---

// clientIPAddress extracts the client IP, respecting X-Forwarded-For from trusted proxies.
func clientIPAddress(r *http.Request) string {
	remoteHost, _, _ := net.SplitHostPort(r.RemoteAddr)
	remoteIP := net.ParseIP(remoteHost)

	// Only trust X-Forwarded-For from loopback (trusted proxy).
	if remoteIP != nil && remoteIP.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.SplitN(xff, ",", 2)
			clientIP := strings.TrimSpace(parts[0])
			if clientIP != "" {
				return clientIP
			}
		}
	}

	if remoteHost != "" {
		return remoteHost
	}
	return "unknown"
}
