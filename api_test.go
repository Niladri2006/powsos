package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pawsos/internal/auth"
	"pawsos/internal/config"
	"pawsos/internal/database"
	"pawsos/internal/handlers"
	"pawsos/internal/middleware"
	"pawsos/internal/models"
	"pawsos/internal/storage"
)

func setupTestApp(t *testing.T) (*handlers.Handler, *config.Config, *database.DB, http.Handler, func()) {
	tmpDir, err := os.MkdirTemp("", "pawsos-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test_pawsos.db")
	db, err := database.Connect("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to connect test db: %v", err)
	}

	cfg := &config.Config{
		Port:              "8080",
		Host:              "127.0.0.1",
		DBDriver:          "sqlite",
		DBSource:          dbPath,
		JWTSecret:         "test_secret_key_1234567890123456",
		StorageDir:        filepath.Join(tmpDir, "uploads"),
		MaxUploadSizeMB:   5,
		CORSAllowedOrigin: "*",
		Environment:       "test",
	}

	store, err := storage.NewStorage(cfg.StorageDir, cfg.MaxUploadSizeMB)
	if err != nil {
		t.Fatalf("failed to create test storage: %v", err)
	}

	h := handlers.NewHandler(db, cfg, store)

	// Setup mux identical to main.go
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("GET /ready", h.Ready)

	authMiddleware := middleware.Auth(cfg.JWTSecret)
	responderAuth := middleware.Auth(cfg.JWTSecret, models.RoleResponder, models.RoleAdmin)
	adminAuth := middleware.Auth(cfg.JWTSecret, models.RoleAdmin)
	optionalAuth := middleware.OptionalAuth(cfg.JWTSecret)

	mux.HandleFunc("POST /api/v1/auth/register", h.Register)
	mux.HandleFunc("POST /api/v1/auth/login", h.Login)
	mux.Handle("GET /api/v1/auth/me", authMiddleware(http.HandlerFunc(h.Me)))

	mux.HandleFunc("POST /api/v1/reports", h.CreateReport)
	mux.Handle("GET /api/v1/reports", optionalAuth(http.HandlerFunc(h.GetReports)))
	mux.Handle("GET /api/v1/reports/{id}", optionalAuth(http.HandlerFunc(h.GetReportByID)))

	mux.Handle("POST /api/v1/reports/{id}/assign", responderAuth(http.HandlerFunc(h.AssignReport)))
	mux.Handle("POST /api/v1/reports/{id}/status", responderAuth(http.HandlerFunc(h.UpdateStatus)))
	mux.Handle("POST /api/v1/reports/{id}/notes", responderAuth(http.HandlerFunc(h.AddNote)))
	mux.Handle("GET /api/v1/reports/export", adminAuth(http.HandlerFunc(h.ExportReports)))
	mux.HandleFunc("GET /api/v1/stats", h.GetStats)

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return h, cfg, db, mux, cleanup
}

func TestHealthAndReady(t *testing.T) {
	_, _, _, app, cleanup := setupTestApp(t)
	defer cleanup()

	// GET /health
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected /health 200, got %d", rec.Code)
	}

	// GET /ready
	req = httptest.NewRequest("GET", "/ready", nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected /ready 200, got %d", rec.Code)
	}
}

func TestAuthWorkflow(t *testing.T) {
	_, _, _, app, cleanup := setupTestApp(t)
	defer cleanup()

	// 1. Register User
	regPayload := `{"name": "Jane Citizen", "email": "jane@example.com", "password": "securepassword123", "role": "citizen"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/register", strings.NewReader(regPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("register expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var authResp models.AuthResponse
	if err := json.NewDecoder(rec.Body).Decode(&authResp); err != nil {
		t.Fatalf("failed to decode auth response: %v", err)
	}
	if authResp.Token == "" {
		t.Fatalf("expected auth token, got empty")
	}

	// 2. Reject duplicate email
	req = httptest.NewRequest("POST", "/api/v1/auth/register", strings.NewReader(regPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for duplicate email, got %d", rec.Code)
	}

	// 3. Login with correct password
	loginPayload := `{"email": "jane@example.com", "password": "securepassword123"}`
	req = httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(loginPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. Login with wrong password
	badLogin := `{"email": "jane@example.com", "password": "wrongpassword"}`
	req = httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(badLogin))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for bad login, got %d", rec.Code)
	}

	// 5. Access protected /api/v1/auth/me
	req = httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+authResp.Token)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /auth/me, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReportWorkflowAndPermissions(t *testing.T) {
	_, cfg, _, app, cleanup := setupTestApp(t)
	defer cleanup()

	// 1. Create a Responder token for tests
	responderUser := &models.User{
		ID:    "resp-test-01",
		Name:  "Officer Max",
		Email: "max@rescue.org",
		Role:  models.RoleResponder,
	}
	respToken, _ := auth.GenerateToken(responderUser, cfg.JWTSecret, 2*time.Hour)

	// Create a Citizen token
	citizenUser := &models.User{
		ID:    "cit-test-01",
		Name:  "Regular User",
		Email: "user@test.org",
		Role:  models.RoleCitizen,
	}
	citToken, _ := auth.GenerateToken(citizenUser, cfg.JWTSecret, 2*time.Hour)

	// 2. Validate missing required fields fails
	badReport := `{"animal_type": "dog"}`
	req := httptest.NewRequest("POST", "/api/v1/reports", strings.NewReader(badReport))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for incomplete report, got %d", rec.Code)
	}

	// 3. Create a valid emergency report
	validReport := `{
		"animal_type": "cat",
		"urgency": "critical",
		"condition": "Stuck in engine bay, engine is hot",
		"description": "Black kitten heard screaming under parked vehicle",
		"address": "742 Evergreen Terrace",
		"area": "Suburbs",
		"city": "Springfield",
		"latitude": 44.0521,
		"longitude": -123.0868,
		"reporter_name": "Homer Simpson",
		"reporter_phone": "+1 555-7334"
	}`

	req = httptest.NewRequest("POST", "/api/v1/reports", strings.NewReader(validReport))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 report creation, got %d: %s", rec.Code, rec.Body.String())
	}

	var created models.Report
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("failed to decode created report: %v", err)
	}
	if !strings.HasPrefix(created.ID, "PAW-") {
		t.Fatalf("expected ID to start with PAW-, got %s", created.ID)
	}
	if created.Status != models.StatusReported {
		t.Fatalf("expected initial status 'reported', got %s", created.Status)
	}

	// 4. Anonymous GET /api/v1/reports/{id} -> Check Privacy Masking
	req = httptest.NewRequest("GET", "/api/v1/reports/"+created.ID, nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	var publicReport models.Report
	_ = json.NewDecoder(rec.Body).Decode(&publicReport)
	if !strings.Contains(publicReport.ReporterPhone, "••••") {
		t.Fatalf("expected masked phone for public, got %s", publicReport.ReporterPhone)
	}

	// 5. Authorized Responder GET -> Phone should be unmasked
	req = httptest.NewRequest("GET", "/api/v1/reports/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+respToken)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	var authReport models.Report
	_ = json.NewDecoder(rec.Body).Decode(&authReport)
	if authReport.ReporterPhone != "+1 555-7334" {
		t.Fatalf("expected unmasked phone for responder, got %s", authReport.ReporterPhone)
	}

	// 6. Citizen attempts to assign case -> Must be rejected with 403 Forbidden
	assignPayload := `{"responder_name": "Unauthorized Attempt", "eta": "10m"}`
	req = httptest.NewRequest("POST", "/api/v1/reports/"+created.ID+"/assign", strings.NewReader(assignPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+citToken)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for citizen assignment, got %d", rec.Code)
	}

	// 7. Responder assigns case -> Must succeed
	req = httptest.NewRequest("POST", "/api/v1/reports/"+created.ID+"/assign", strings.NewReader(assignPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+respToken)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for responder assignment, got %d: %s", rec.Code, rec.Body.String())
	}

	// 8. Responder updates status to in_progress then resolved
	statusPayload := `{"status": "in_progress", "note": "On scene, hood opened, kitten secured in crate."}`
	req = httptest.NewRequest("POST", "/api/v1/reports/"+created.ID+"/status", strings.NewReader(statusPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+respToken)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for status update, got %d", rec.Code)
	}

	statusResolved := `{"status": "resolved", "note": "Kitten hydrated, transferred to veterinary partner."}`
	req = httptest.NewRequest("POST", "/api/v1/reports/"+created.ID+"/status", strings.NewReader(statusResolved))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+respToken)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for resolving status, got %d", rec.Code)
	}

	// 9. Verify stats endpoint reflects the report
	req = httptest.NewRequest("GET", "/api/v1/stats", nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	var stats models.StatsResponse
	_ = json.NewDecoder(rec.Body).Decode(&stats)
	if stats.TotalReports != 1 || stats.Resolved != 1 {
		t.Fatalf("expected 1 total, 1 resolved in stats, got total=%d, resolved=%d", stats.TotalReports, stats.Resolved)
	}
}
