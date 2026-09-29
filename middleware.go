package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"pawsos/internal/auth"
	"pawsos/internal/models"
)

type contextKey string

const (
	UserClaimsKey contextKey = "user_claims"
)

type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
	bytesWritten int64
}

func (w *responseWriterWrapper) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriterWrapper) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += int64(n)
	return n, err
}

// Logger logs each incoming HTTP request with method, path, status, latency and remote IP
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapper := &responseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapper, r)

		duration := time.Since(start)
		log.Printf("[HTTP] %s %s %d %s %s - %dB",
			r.Method,
			r.URL.Path,
			wrapper.statusCode,
			duration.Round(time.Microsecond),
			r.RemoteAddr,
			wrapper.bytesWritten,
		)
	})
}

// Recoverer intercepts any unhandled panics and outputs a structured 500 error
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[PANIC RECOVERED] %v", rec)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]string{
						"code":    "INTERNAL_SERVER_ERROR",
						"message": "An unexpected server error occurred.",
					},
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders applies baseline HTTP security hardening headers
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// CORS handles Cross-Origin Resource Sharing and preflight requests
func CORS(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := allowedOrigin
			if origin == "" {
				origin = "*"
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Auth enforces valid JWT token authentication and optional role-based authorization
func Auth(jwtSecret string, requiredRoles ...models.UserRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Missing authorization header")
				return
			}

			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				writeError(w, http.StatusUnauthorized, "INVALID_TOKEN", "Malformed authorization header, expected 'Bearer <token>'")
				return
			}

			claims, err := auth.ValidateToken(parts[1], jwtSecret)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "INVALID_TOKEN", "Invalid or expired token")
				return
			}

			// Role verification if required
			if len(requiredRoles) > 0 {
				authorized := false
				for _, role := range requiredRoles {
					if claims.Role == role || claims.Role == models.RoleAdmin {
						authorized = true
						break
					}
				}
				if !authorized {
					writeError(w, http.StatusForbidden, "FORBIDDEN", fmt.Sprintf("Role '%s' is not authorized for this resource", claims.Role))
					return
				}
			}

			ctx := context.WithValue(r.Context(), UserClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalAuth extracts user claims if an authorization header is present without requiring it
func OptionalAuth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "" {
				parts := strings.Split(authHeader, " ")
				if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
					if claims, err := auth.ValidateToken(parts[1], jwtSecret); err == nil {
						ctx := context.WithValue(r.Context(), UserClaimsKey, claims)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimiter provides simple in-memory IP rate limiting
type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*clientBucket
	rate    int
	window  time.Duration
}

type clientBucket struct {
	count     int
	resetTime time.Time
}

func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		clients: make(map[string]*clientBucket),
		rate:    rate,
		window:  window,
	}

	// Periodic garbage collection for expired buckets
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			rl.mu.Lock()
			now := time.Now()
			for ip, bucket := range rl.clients {
				if now.After(bucket.resetTime) {
					delete(rl.clients, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()

	return rl
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := strings.Split(r.RemoteAddr, ":")[0]
		rl.mu.Lock()
		now := time.Now()
		bucket, exists := rl.clients[ip]
		if !exists || now.After(bucket.resetTime) {
			rl.clients[ip] = &clientBucket{
				count:     1,
				resetTime: now.Add(rl.window),
			}
			rl.mu.Unlock()
			next.ServeHTTP(w, r)
			return
		}

		if bucket.count >= rl.rate {
			rl.mu.Unlock()
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests. Please slow down.")
			return
		}

		bucket.count++
		rl.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
