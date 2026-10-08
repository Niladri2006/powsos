package tests

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
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
	mux.Handle("POST /api/v1/responders/application", authMiddleware(http.HandlerFunc(h.ApplyResponder)))

	mux.HandleFunc("POST /api/v1/reports", h.CreateReport)
	mux.Handle("GET /api/v1/reports", optionalAuth(http.HandlerFunc(h.GetReports)))
	mux.Handle("GET /api/v1/reports/{id}", optionalAuth(http.HandlerFunc(h.GetReportByID)))

	mux.Handle("POST /api/v1/reports/{id}/assign", responderAuth(http.HandlerFunc(h.AssignReport)))
	mux.Handle("POST /api/v1/reports/{id}/status", responderAuth(http.HandlerFunc(h.UpdateStatus)))
	mux.Handle("POST /api/v1/reports/{id}/notes", responderAuth(http.HandlerFunc(h.AddNote)))
	mux.Handle("GET /api/v1/reports/export", adminAuth(http.HandlerFunc(h.ExportReports)))
	mux.Handle("GET /api/v1/responders/pending", adminAuth(http.HandlerFunc(h.GetPendingResponders)))
	mux.Handle("POST /api/v1/responders/{id}/verification", adminAuth(http.HandlerFunc(h.VerifyResponder)))
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
	regPayload := `{"name": "Jane Citizen", "email": "jane@example.com", "password": "securepassword123", "role": "admin"}`
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
	if authResp.User.Role != models.RoleCitizen || authResp.User.RequestedRole != models.RoleCitizen {
		t.Fatalf("self-registration must not grant privileged role: %+v", authResp.User)
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
	_, cfg, db, app, cleanup := setupTestApp(t)
	defer cleanup()

	// 1. Create a Responder token for tests
	responderUser := &models.User{
		ID:            "resp-test-01",
		Name:          "Officer Max",
		Email:         "max@rescue.org",
		PasswordHash:  "test",
		Role:          models.RoleResponder,
		RequestedRole: models.RoleResponder,
		Verified:      true,
		CreatedAt:     time.Now(),
	}
	if err := db.CreateUser(t.Context(), responderUser); err != nil {
		t.Fatalf("failed to create verified responder test account: %v", err)
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
	if publicReport.ReporterPhone != "" || publicReport.ReporterName != "" || publicReport.Address != "" ||
		publicReport.Area != "" || publicReport.City != "" {
		t.Fatalf("public report exposed private contact or address: %+v", publicReport)
	}
	if publicReport.Latitude == 44.0521 || publicReport.Longitude == -123.0868 {
		t.Fatalf("public report exposed exact coordinates: %f, %f", publicReport.Latitude, publicReport.Longitude)
	}

	// 5. Authorized Responder GET -> Phone should be unmasked
	req = httptest.NewRequest("GET", "/api/v1/reports/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+respToken)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	var authReport models.Report
	_ = json.NewDecoder(rec.Body).Decode(&authReport)
	if authReport.ReporterPhone != "" {
		t.Fatalf("unassigned responder should not receive reporter phone, got %s", authReport.ReporterPhone)
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

	req = httptest.NewRequest("GET", "/api/v1/reports/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+respToken)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	authReport = models.Report{}
	_ = json.NewDecoder(rec.Body).Decode(&authReport)
	if authReport.ReporterPhone != "+1 555-7334" {
		t.Fatalf("assigned responder should receive reporter phone, got %s", authReport.ReporterPhone)
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

	req = httptest.NewRequest("GET", "/api/v1/reports/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+respToken)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	var tracked models.Report
	if err := json.NewDecoder(rec.Body).Decode(&tracked); err != nil {
		t.Fatalf("decode tracked report: %v", err)
	}
	if len(tracked.Timeline) != 4 || tracked.Timeline[len(tracked.Timeline)-1].Status != models.StatusResolved {
		t.Fatalf("status actions were not recorded in the timeline: %+v", tracked.Timeline)
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

func TestResponderApplicationReview(t *testing.T) {
	_, cfg, db, app, cleanup := setupTestApp(t)
	defer cleanup()

	applicant := &models.User{
		ID:            "applicant-01",
		Name:          "Rescue Applicant",
		Email:         "applicant@example.org",
		PasswordHash:  "unused-test-hash",
		Role:          models.RoleCitizen,
		RequestedRole: models.RoleCitizen,
		CreatedAt:     time.Now(),
	}
	admin := &models.User{
		ID:            "admin-01",
		Name:          "Test Admin",
		Email:         "admin@example.org",
		PasswordHash:  "unused-test-hash",
		Role:          models.RoleAdmin,
		RequestedRole: models.RoleAdmin,
		Verified:      true,
		CreatedAt:     time.Now(),
	}
	for _, user := range []*models.User{applicant, admin} {
		if err := db.CreateUser(t.Context(), user); err != nil {
			t.Fatalf("create test user: %v", err)
		}
	}
	applicantToken, err := auth.GenerateToken(applicant, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("create applicant token: %v", err)
	}
	adminToken, err := auth.GenerateToken(admin, cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/responders/application", strings.NewReader(`{"phone":"+1 555-0135","organization":"Local transport volunteer"}`))
	req.Header.Set("Authorization", "Bearer "+applicantToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("application expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/v1/responders/pending", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	var pending struct {
		Responders []models.User `json:"responders"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&pending); err != nil {
		t.Fatalf("decode pending applications: %v", err)
	}
	if rec.Code != http.StatusOK || len(pending.Responders) != 1 || pending.Responders[0].ID != applicant.ID {
		t.Fatalf("expected applicant in admin review list, got status=%d applications=%+v", rec.Code, pending.Responders)
	}

	req = httptest.NewRequest("POST", "/api/v1/responders/"+applicant.ID+"/verification", strings.NewReader(`{"verified":true}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("verification expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	verified, err := db.GetUserByID(t.Context(), applicant.ID)
	if err != nil {
		t.Fatalf("load verified applicant: %v", err)
	}
	if verified.Role != models.RoleResponder || !verified.Verified {
		t.Fatalf("review did not grant verified responder role: %+v", verified)
	}
	if verified.Phone != "+1 555-0135" || verified.Organization != "Local transport volunteer" {
		t.Fatalf("application contact details were not saved: %+v", verified)
	}
}

func TestReportDuplicateAndNearbyFiltering(t *testing.T) {
	_, _, _, app, cleanup := setupTestApp(t)
	defer cleanup()

	payload := `{
		"animal_type":"dog","urgency":"urgent","condition":"Injured and limping",
		"address":"Near the park","latitude":44.0521,"longitude":-123.0868,
		"reporter_name":"Reporter","reporter_phone":"5550100"
	}`
	req := httptest.NewRequest("POST", "/api/v1/reports", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create report expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created models.Report
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created report: %v", err)
	}

	req = httptest.NewRequest("POST", "/api/v1/reports", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "POSSIBLE_DUPLICATE") {
		t.Fatalf("nearby duplicate should require confirmation, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/v1/reports?latitude=44.052&longitude=-123.087&radius_km=1", nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("nearby query expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var nearby struct {
		Reports []models.Report `json:"reports"`
		Total   int             `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&nearby); err != nil {
		t.Fatalf("decode nearby reports: %v", err)
	}
	if nearby.Total != 1 || len(nearby.Reports) != 1 || nearby.Reports[0].ID != created.ID {
		t.Fatalf("nearby query returned unexpected cases: %+v", nearby)
	}
	if nearby.Reports[0].Latitude == created.Latitude || nearby.Reports[0].ReporterPhone != "" {
		t.Fatalf("nearby public projection exposed precise or private data: %+v", nearby.Reports[0])
	}
	if nearby.Reports[0].DistanceMeters == nil || *nearby.Reports[0].DistanceMeters%100 != 0 {
		t.Fatalf("public nearby distance should be approximate: %+v", nearby.Reports[0].DistanceMeters)
	}
}

func TestReportRequiresCoordinatesAndValidPhotoLocation(t *testing.T) {
	_, _, _, app, cleanup := setupTestApp(t)
	defer cleanup()

	req := httptest.NewRequest("POST", "/api/v1/reports", strings.NewReader(`{"animal_type":"dog"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "INVALID_LOCATION") {
		t.Fatalf("report without coordinates should be rejected, got %d: %s", rec.Code, rec.Body.String())
	}

	payload := `{
		"animal_type":"dog","urgency":"urgent","condition":"Needs assistance",
		"address":"Near the park","latitude":44.0521,"longitude":-123.0868,
		"photo_latitude":44.0531,"reporter_name":"Reporter","reporter_phone":"5550100"
	}`
	req = httptest.NewRequest("POST", "/api/v1/reports", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "INVALID_IMAGE_LOCATION") {
		t.Fatalf("incomplete photo coordinates should be rejected, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReportPhotoEvidenceMetadataAndPrivacy(t *testing.T) {
	_, _, db, app, cleanup := setupTestApp(t)
	defer cleanup()

	picture := image.NewRGBA(image.Rect(0, 0, 8, 8))
	picture.Set(0, 0, color.RGBA{R: 200, G: 40, B: 30, A: 255})
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, picture); err != nil {
		t.Fatalf("encode test photo: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{
		"animal_type": "cat", "urgency": "critical", "condition": "Unable to move",
		"address": "Near a bus stop", "latitude": "44.0521", "longitude": "-123.0868",
		"location_accuracy": "18", "location_source": "browser_geolocation",
		"reporter_name": "Photo Reporter", "reporter_phone": "5550199",
		"photo_source": "camera", "photo_captured_at": time.Now().UTC().Format(time.RFC3339Nano),
		"photo_location_accuracy": "12", "photo_latitude": "44.0531", "photo_longitude": "-123.0878",
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write form field %s: %v", key, err)
		}
	}
	file, err := writer.CreateFormFile("photo", "scene.png")
	if err != nil {
		t.Fatalf("create photo field: %v", err)
	}
	if _, err := file.Write(imageData.Bytes()); err != nil {
		t.Fatalf("write photo field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/reports", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("photo report expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created models.Report
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode photo report: %v", err)
	}
	if created.Evidence == nil || created.Evidence.SourceType != "camera_option" || created.Evidence.MIMEType != "image/png" || len(created.Evidence.SHA256) != 64 {
		t.Fatalf("photo evidence metadata missing or incorrect: %+v", created.Evidence)
	}
	if created.Evidence.CaptureTimestamp == nil || created.Evidence.UploadTimestamp.IsZero() || created.Evidence.StorageReference == "" {
		t.Fatalf("photo evidence timestamps or storage reference missing: %+v", created.Evidence)
	}
	if created.Evidence.Latitude == nil || *created.Evidence.Latitude != 44.0531 ||
		created.Evidence.Longitude == nil || *created.Evidence.Longitude != -123.0878 ||
		created.Evidence.LocationAccuracy == nil || *created.Evidence.LocationAccuracy != 12 {
		t.Fatalf("photo location snapshot was not stored: %+v", created.Evidence)
	}
	persisted, err := db.GetEvidence(t.Context(), created.ID)
	if err != nil || persisted == nil || persisted.SHA256 != created.Evidence.SHA256 ||
		persisted.Latitude == nil || *persisted.Latitude != 44.0531 {
		t.Fatalf("evidence metadata did not persist to the database: %+v, %v", persisted, err)
	}

	req = httptest.NewRequest("GET", "/api/v1/reports/"+created.ID, nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	var publicReport models.Report
	if err := json.NewDecoder(rec.Body).Decode(&publicReport); err != nil {
		t.Fatalf("decode public photo report: %v", err)
	}
	if publicReport.Evidence != nil || publicReport.ReporterName != "" || publicReport.ReporterPhone != "" {
		t.Fatalf("public report exposed restricted evidence or reporter information: %+v", publicReport)
	}
}
