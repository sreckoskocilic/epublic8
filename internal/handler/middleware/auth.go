// Package middleware provides HTTP middleware components for authentication and other concerns.
package middleware

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"epublic8/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// BasicAuth returns a handler that wraps the provided handler with basic authentication.
// If the config's BasicAuth field is empty, the handler is returned unchanged (no auth required).
// The format for BasicAuth is "username:password" or "username:hashed_password".
// For plain text passwords, direct comparison is used.
// For bcrypt hashed passwords (starting with $2a$, $2b$, or $2y$), the constant-time comparison
// is performed against the hashed input.
//
// Public endpoints (health, version) should be handled before this middleware.
func BasicAuth(cfg *config.SecurityConfig, next http.Handler) http.Handler {
	// If no basic auth configured, pass through
	if cfg.BasicAuth == "" {
		return next
	}

	// Parse the credentials: "username:password" or "username:hashed_password"
	parts := strings.SplitN(cfg.BasicAuth, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		// Invalid config - log and treat as no auth
		return next
	}

	username := parts[0]
	passwordOrHash := parts[1]

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for Authorization header
		auth := r.Header.Get("Authorization")
		if auth == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
			http.Error(w, "authorization required", http.StatusUnauthorized)
			return
		}

		// Parse "Basic base64(username:password)"
		if !strings.HasPrefix(auth, "Basic ") {
			w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
			http.Error(w, "invalid authorization header", http.StatusUnauthorized)
			return
		}

		encoded := strings.TrimPrefix(auth, "Basic ")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
			http.Error(w, "invalid authorization header", http.StatusUnauthorized)
			return
		}

		// Split into username and password
		credParts := strings.SplitN(string(decoded), ":", 2)
		if len(credParts) != 2 {
			w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}

		providedUser := credParts[0]
		providedPass := credParts[1]

		// Constant-time comparison for username (case-sensitive)
		if subtle.ConstantTimeCompare([]byte(username), []byte(providedUser)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}

		// For bcrypt hashes (starts with $2a$, $2b$, or $2y$), use bcrypt comparison.
		// For plain text passwords, use constant-time comparison.
		var passwordOK bool
		if strings.HasPrefix(passwordOrHash, "$2a$") || strings.HasPrefix(passwordOrHash, "$2b$") || strings.HasPrefix(passwordOrHash, "$2y$") {
			passwordOK = bcrypt.CompareHashAndPassword([]byte(passwordOrHash), []byte(providedPass)) == nil
		} else {
			passwordOK = subtle.ConstantTimeCompare([]byte(passwordOrHash), []byte(providedPass)) == 1
		}
		if !passwordOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}

		// Authentication successful - call the next handler
		next.ServeHTTP(w, r)
	})
}

// AuthMiddleware creates a middleware that applies basic authentication based on config.
// This is a convenience wrapper around BasicAuth for use with http.Handler types.
func AuthMiddleware(cfg *config.SecurityConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return BasicAuth(cfg, next)
	}
}
