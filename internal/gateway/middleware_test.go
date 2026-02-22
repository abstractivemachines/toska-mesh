package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// --- Rate Limiter Tests ---

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := NewRateLimiter(5, 60)

	for range 5 {
		if !rl.allow("10.0.0.1") {
			t.Fatal("expected request to be allowed within limit")
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter(3, 60)

	for range 3 {
		rl.allow("10.0.0.1")
	}

	if rl.allow("10.0.0.1") {
		t.Fatal("expected request to be blocked over limit")
	}
}

func TestRateLimiter_SeparateBucketsPerIP(t *testing.T) {
	rl := NewRateLimiter(2, 60)

	rl.allow("10.0.0.1")
	rl.allow("10.0.0.1")

	// Different IP should still be allowed.
	if !rl.allow("10.0.0.2") {
		t.Fatal("expected different IP to be allowed")
	}
}

func TestRateLimiter_ResetsAfterWindow(t *testing.T) {
	rl := NewRateLimiter(1, 1) // 1-second window

	rl.allow("10.0.0.1")
	if rl.allow("10.0.0.1") {
		t.Fatal("expected to be blocked")
	}

	time.Sleep(1100 * time.Millisecond)

	if !rl.allow("10.0.0.1") {
		t.Fatal("expected to be allowed after window reset")
	}
}

func TestRateLimiter_HTTPMiddleware(t *testing.T) {
	rl := NewRateLimiter(1, 60)

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request: allowed.
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Second request: blocked.
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w2.Code)
	}
}

// --- CORS Tests ---

func TestCORS_AllowAnyOrigin(t *testing.T) {
	handler := CORS(CORSConfig{
		AllowAnyOrigin: true,
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Authorization"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected ACAO=*, got %q", got)
	}
}

func TestCORS_SpecificOrigin(t *testing.T) {
	handler := CORS(CORSConfig{
		AllowAnyOrigin: false,
		AllowedOrigins: []string{"http://allowed.com"},
		AllowedMethods: []string{"GET"},
		AllowedHeaders: []string{"Content-Type"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Allowed origin.
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://allowed.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://allowed.com" {
		t.Fatalf("expected ACAO=http://allowed.com, got %q", got)
	}

	// Disallowed origin.
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Origin", "http://evil.com")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no ACAO header for disallowed origin, got %q", got)
	}
}

func TestCORS_PreflightReturns204(t *testing.T) {
	handler := CORS(CORSConfig{
		AllowAnyOrigin: true,
		AllowedMethods: []string{"POST"},
		AllowedHeaders: []string{"Content-Type"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", w.Code)
	}
}

// --- JWT Tests ---

func makeTestJWT(secret, issuer, audience string, expiry time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

	claims := map[string]any{
		"iss": issuer,
		"aud": audience,
		"exp": expiry.Unix(),
		"sub": "test-user",
	}
	claimsJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("%s.%s.%s", header, payload, sig)
}

func TestJWTAuth_ValidToken(t *testing.T) {
	cfg := JWTConfig{
		SecretKey:        "test-secret-key-at-least-32-characters",
		Issuer:           "test-issuer",
		Audience:         "test-audience",
		ValidateIssuer:   true,
		ValidateAudience: true,
	}

	token := makeTestJWT(cfg.SecretKey, cfg.Issuer, cfg.Audience, time.Now().Add(1*time.Hour))

	handler := JWTAuth(cfg, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestJWTAuth_MissingToken(t *testing.T) {
	cfg := JWTConfig{SecretKey: "test-secret-key-at-least-32-characters"}

	handler := JWTAuth(cfg, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestJWTAuth_ExpiredToken(t *testing.T) {
	cfg := JWTConfig{SecretKey: "test-secret-key-at-least-32-characters"}
	token := makeTestJWT(cfg.SecretKey, "", "", time.Now().Add(-1*time.Hour))

	handler := JWTAuth(cfg, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestJWTAuth_InvalidSignature(t *testing.T) {
	cfg := JWTConfig{SecretKey: "correct-secret-key-at-least-32-chars"}
	token := makeTestJWT("wrong-secret-key-at-least-32-chars!!", "", "", time.Now().Add(1*time.Hour))

	handler := JWTAuth(cfg, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestJWTAuth_SkipPaths(t *testing.T) {
	cfg := JWTConfig{SecretKey: "test-secret-key-at-least-32-characters"}

	handler := JWTAuth(cfg, []string{"/health"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for skipped path, got %d", w.Code)
	}
}

func TestJWTAuth_NoSecretDisablesAuth(t *testing.T) {
	cfg := JWTConfig{SecretKey: ""}

	handler := JWTAuth(cfg, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when auth disabled, got %d", w.Code)
	}
}

// --- Client IP Tests ---

func TestClientIPAddress_DirectConnection(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"

	got := clientIPAddress(req)
	if got != "10.0.0.1" {
		t.Fatalf("expected 10.0.0.1, got %s", got)
	}
}

func TestClientIPAddress_TrustedProxyXFF(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 70.41.3.18")

	got := clientIPAddress(req)
	if got != "203.0.113.50" {
		t.Fatalf("expected 203.0.113.50, got %s", got)
	}
}

func TestClientIPAddress_UntrustedProxyIgnoresXFF(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "spoofed-ip")

	got := clientIPAddress(req)
	if got != "10.0.0.1" {
		t.Fatalf("expected 10.0.0.1 (ignoring XFF from non-loopback), got %s", got)
	}
}
