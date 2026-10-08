package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"pawsos/internal/auth"
	"pawsos/internal/config"
	"pawsos/internal/database"
	"pawsos/internal/handlers"
	"pawsos/internal/middleware"
	"pawsos/internal/models"
	"pawsos/internal/storage"
)

func main() {
	log.Println("[PAWSOS] Initializing PawSOS Emergency Rescue Platform...")

	// 1. Load Configuration
	cfg := config.Load()
	log.Printf("[PAWSOS] Environment: %s | Port: %s | DB Driver: %s", cfg.Environment, cfg.Port, cfg.DBDriver)
	if cfg.Environment == "production" && len(cfg.JWTSecret) < 32 {
		log.Fatal("[PAWSOS FATAL] Production requires JWT_SECRET with at least 32 characters.")
	}
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = "pawsos-local-development-secret"
		log.Println("[PAWSOS WARN] Using a development-only JWT secret.")
	}

	// 2. Initialize Database & Run Migrations
	db, err := database.Connect(cfg.DBDriver, cfg.DBSource)
	if err != nil {
		log.Fatalf("[PAWSOS FATAL] Failed to connect to database: %v", err)
	}
	defer db.Close()
	log.Println("[PAWSOS] Database connected & schema migrated successfully.")
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := db.RemoveLegacyDemoData(cleanupCtx); err != nil {
		cleanupCancel()
		log.Fatalf("[PAWSOS FATAL] Could not disable legacy demo accounts and reports: %v", err)
	}
	cleanupCancel()
	if (cfg.BootstrapAdminEmail == "") != (cfg.BootstrapAdminPassword == "") {
		log.Fatal("[PAWSOS FATAL] Set both ADMIN_EMAIL and ADMIN_PASSWORD to provision the initial administrator.")
	}
	if cfg.BootstrapAdminEmail != "" {
		if len(cfg.BootstrapAdminPassword) < 12 || len(cfg.BootstrapAdminPassword) > 72 {
			log.Fatal("[PAWSOS FATAL] ADMIN_PASSWORD must be between 12 and 72 bytes.")
		}
		existingAdmin, lookupErr := db.GetUserByEmail(context.Background(), cfg.BootstrapAdminEmail)
		if errors.Is(lookupErr, database.ErrNotFound) {
			passwordHash, hashErr := auth.HashPassword(cfg.BootstrapAdminPassword)
			if hashErr != nil {
				log.Fatalf("[PAWSOS FATAL] Failed to secure bootstrap administrator password: %v", hashErr)
			}
			admin := &models.User{
				ID:           uuid.NewString(),
				Name:         "PawSOS Administrator",
				Email:        cfg.BootstrapAdminEmail,
				PasswordHash: passwordHash,
				Role:         models.RoleAdmin,
				Verified:     true,
				CreatedAt:    time.Now(),
			}
			if err := db.CreateUser(context.Background(), admin); err != nil {
				log.Fatalf("[PAWSOS FATAL] Failed to create bootstrap administrator: %v", err)
			}
			log.Println("[PAWSOS] Initial administrator provisioned from environment configuration.")
		} else if lookupErr != nil {
			log.Fatalf("[PAWSOS FATAL] Failed to check bootstrap administrator configuration: %v", lookupErr)
		} else if existingAdmin.Role != models.RoleAdmin || !existingAdmin.Verified {
			log.Fatal("[PAWSOS FATAL] ADMIN_EMAIL belongs to an existing non-administrator account. Choose a dedicated administrator email.")
		}
	}

	// 3. Initialize Local / S3 Storage
	store, err := storage.NewStorage(cfg.StorageDir, cfg.MaxUploadSizeMB)
	if err != nil {
		log.Fatalf("[PAWSOS FATAL] Failed to initialize storage: %v", err)
	}
	log.Printf("[PAWSOS] Storage ready at '%s' (max upload: %dMB)", cfg.StorageDir, cfg.MaxUploadSizeMB)

	// 4. Initialize Handlers & Rate Limiters
	h := handlers.NewHandler(db, cfg, store)
	rateLimiterStrict := middleware.NewRateLimiter(15, 1*time.Minute) // 15 req/min for auth & submissions
	rateLimiterGeneral := middleware.NewRateLimiter(120, 1*time.Minute)

	authMiddleware := middleware.Auth(cfg.JWTSecret)
	responderAuth := middleware.Auth(cfg.JWTSecret, models.RoleResponder, models.RoleAdmin)
	adminAuth := middleware.Auth(cfg.JWTSecret, models.RoleAdmin)
	optionalAuth := middleware.OptionalAuth(cfg.JWTSecret)

	// 5. Router Setup
	mux := http.NewServeMux()

	// Health and Readiness
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("GET /ready", h.Ready)

	// Authentication Endpoints
	mux.Handle("POST /api/v1/auth/register", rateLimiterStrict.Middleware(http.HandlerFunc(h.Register)))
	mux.Handle("POST /api/v1/auth/login", rateLimiterStrict.Middleware(http.HandlerFunc(h.Login)))
	mux.Handle("GET /api/v1/auth/me", authMiddleware(http.HandlerFunc(h.Me)))
	mux.Handle("POST /api/v1/responders/application", rateLimiterStrict.Middleware(authMiddleware(http.HandlerFunc(h.ApplyResponder))))

	// Report Endpoints
	mux.Handle("POST /api/v1/reports", rateLimiterStrict.Middleware(http.HandlerFunc(h.CreateReport)))
	mux.Handle("GET /api/v1/reports", optionalAuth(rateLimiterGeneral.Middleware(http.HandlerFunc(h.GetReports))))
	mux.Handle("GET /api/v1/reports/{id}", optionalAuth(rateLimiterGeneral.Middleware(http.HandlerFunc(h.GetReportByID))))

	// Responder & Admin Workflow Endpoints (Strictly Authorized Server-Side)
	mux.Handle("POST /api/v1/reports/{id}/assign", responderAuth(http.HandlerFunc(h.AssignReport)))
	mux.Handle("POST /api/v1/reports/{id}/status", responderAuth(http.HandlerFunc(h.UpdateStatus)))
	mux.Handle("POST /api/v1/reports/{id}/notes", responderAuth(http.HandlerFunc(h.AddNote)))
	mux.Handle("GET /api/v1/reports/export", adminAuth(http.HandlerFunc(h.ExportReports)))
	mux.Handle("GET /api/v1/responders/pending", adminAuth(http.HandlerFunc(h.GetPendingResponders)))
	mux.Handle("POST /api/v1/responders/{id}/verification", adminAuth(http.HandlerFunc(h.VerifyResponder)))

	// Statistics
	mux.HandleFunc("GET /api/v1/stats", h.GetStats)

	// Serve Uploaded Files
	publicUploads := http.StripPrefix("/uploads/", http.FileServer(http.Dir(filepath.Join(cfg.StorageDir, "public"))))
	mux.HandleFunc("GET /uploads/", func(w http.ResponseWriter, r *http.Request) {
		relativePath := strings.TrimPrefix(r.URL.Path, "/uploads/")
		if strings.HasPrefix(relativePath, "reports/") {
			publicUploads.ServeHTTP(w, r)
			return
		}
		filename := filepath.Base(relativePath)
		if filename != relativePath || !strings.HasPrefix(filename, "report_") ||
			(filepath.Ext(filename) != ".jpg" && filepath.Ext(filename) != ".png") {
			http.NotFound(w, r)
			return
		}
		legacyPath := filepath.Join(cfg.StorageDir, filename)
		if _, err := os.Stat(legacyPath); err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, legacyPath)
	})

	// Serve Static Frontend Assets (HTML, CSS, JS, Images)
	staticDir := "."
	fs := http.FileServer(http.Dir(staticDir))
	publicStaticExtensions := map[string]bool{
		".html": true, ".css": true, ".js": true, ".jpg": true, ".jpeg": true,
		".png": true, ".ico": true, ".woff": true, ".woff2": true,
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Clean(r.URL.Path)

		// Don't serve Go source files or hidden files
		if strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".mod") || strings.HasSuffix(path, ".sum") || strings.HasPrefix(filepath.Base(path), ".") {
			http.NotFound(w, r)
			return
		}

		// Try file as requested
		fullPath := filepath.Join(staticDir, path)
		if path != "/" {
			if publicStaticExtensions[filepath.Ext(path)] {
				info, err := os.Stat(fullPath)
				if err == nil && !info.IsDir() {
					fs.ServeHTTP(w, r)
					return
				}
			}
		}

		// Try appending .html (e.g. /report -> /report.html)
		if filepath.Ext(path) == "" {
			htmlPath := filepath.Join(staticDir, path+".html")
			if hInfo, hErr := os.Stat(htmlPath); hErr == nil && !hInfo.IsDir() {
				http.ServeFile(w, r, htmlPath)
				return
			}
		}

		// If root directory, serve index.html
		if path == "/" || path == "." {
			http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
			return
		}

		http.NotFound(w, r)
	})

	// 6. Global Middleware Chain: Recoverer -> SecurityHeaders -> CORS -> Logger
	handlerChain := middleware.Recoverer(
		middleware.SecurityHeaders(
			middleware.CORS(cfg.CORSAllowedOrigin)(
				middleware.Logger(mux),
			),
		),
	)

	// 7. Server with Clean Graceful Shutdown
	addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handlerChain,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("[PAWSOS] Server listening on http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[PAWSOS FATAL] Server failed: %v", err)
		}
	}()

	// Listen for shutdown signals (SIGINT, SIGTERM)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[PAWSOS] Graceful shutdown initiated...")
	ctxShut, cancelShut := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShut()

	if err := srv.Shutdown(ctxShut); err != nil {
		log.Printf("[PAWSOS ERROR] Forced shutdown: %v", err)
	} else {
		log.Println("[PAWSOS] Server gracefully stopped.")
	}
}
