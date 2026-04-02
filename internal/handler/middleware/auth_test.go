package middleware

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"epublic8/internal/config"
)

func TestBasicAuthNoAuthConfigured(t *testing.T) {
	// When BasicAuth is empty, should pass through without auth
	cfg := &config.SecurityConfig{
		BasicAuth: "",
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := BasicAuth(cfg, next)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !nextCalled {
		t.Error("expected next handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestBasicAuthMissingHeader(t *testing.T) {
	cfg := &config.SecurityConfig{
		BasicAuth: "admin:secret",
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})

	handler := BasicAuth(cfg, next)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if nextCalled {
		t.Error("expected next handler NOT to be called")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if wwwAuth := rec.Header().Get("WWW-Authenticate"); wwwAuth == "" {
		t.Error("expected WWW-Authenticate header")
	}
}

func TestBasicAuthInvalidHeaderFormat(t *testing.T) {
	cfg := &config.SecurityConfig{
		BasicAuth: "admin:secret",
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})

	handler := BasicAuth(cfg, next)

	tests := []struct {
		name string
		auth string
	}{
		{"Bearer token", "Bearer somtoken"},
		{"Invalid base64", "Basic notbase64!!"},
		{"Empty after Basic", "Basic "},
		{"Wrong prefix", "BasicAuth test"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nextCalled = false
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Authorization", tc.auth)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if nextCalled {
				t.Error("expected next handler NOT to be called")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rec.Code)
			}
		})
	}
}

func TestBasicAuthInvalidCredentials(t *testing.T) {
	cfg := &config.SecurityConfig{
		BasicAuth: "admin:secret",
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})

	handler := BasicAuth(cfg, next)

	tests := []struct {
		name         string
		username     string
		password     string
		expectCalled bool
	}{
		{"wrong password", "admin", "wrongpass", false},
		{"wrong username", "wronguser", "secret", false},
		{"empty username", "", "secret", false},
		{"empty password", "admin", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nextCalled = false
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(tc.username+":"+tc.password)))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if nextCalled != tc.expectCalled {
				t.Errorf("expected nextCalled=%v, got %v", tc.expectCalled, nextCalled)
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rec.Code)
			}
		})
	}
}

func TestBasicAuthValidCredentials(t *testing.T) {
	cfg := &config.SecurityConfig{
		BasicAuth: "admin:secret",
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := BasicAuth(cfg, next)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret")))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !nextCalled {
		t.Error("expected next handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestBasicAuthValidCredentialsLongPassword(t *testing.T) {
	cfg := &config.SecurityConfig{
		BasicAuth: "user:this_is_a_very_long_password_with_special_chars!@#$%",
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := BasicAuth(cfg, next)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:this_is_a_very_long_password_with_special_chars!@#$%")))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !nextCalled {
		t.Error("expected next handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware(t *testing.T) {
	cfg := &config.SecurityConfig{
		BasicAuth: "testuser:testpass",
	}

	middleware := AuthMiddleware(cfg)

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware(next)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("testuser:testpass")))
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if !nextCalled {
		t.Error("expected next handler to be called")
	}
}

func TestBasicAuthDifferentPaths(t *testing.T) {
	cfg := &config.SecurityConfig{
		BasicAuth: "admin:secret",
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := BasicAuth(cfg, next)

	paths := []string{"/", "/api/upload", "/download", "/health", "/metrics"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			nextCalled = false
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret")))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if !nextCalled {
				t.Errorf("expected next handler to be called for path %s", path)
			}
		})
	}
}

func TestBasicAuthMalformedBase64(t *testing.T) {
	cfg := &config.SecurityConfig{
		BasicAuth: "admin:secret",
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})

	handler := BasicAuth(cfg, next)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic invalid!!!base64!!!")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if nextCalled {
		t.Error("expected next handler NOT to be called")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestBasicAuthCredentialsWithoutPassword(t *testing.T) {
	cfg := &config.SecurityConfig{
		BasicAuth: "admin:secret",
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})

	handler := BasicAuth(cfg, next)
	// Send "admin:" (empty password)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:")))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should fail because password doesn't match
	if nextCalled {
		t.Error("expected next handler NOT to be called")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}
