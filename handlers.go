package handlers

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"pawsos/internal/auth"
	"pawsos/internal/config"
	"pawsos/internal/database"
	"pawsos/internal/middleware"
	"pawsos/internal/models"
	"pawsos/internal/storage"
)

type Handler struct {
	db      *database.DB
	cfg     *config.Config
	storage *storage.Storage
	started time.Time
}

func NewHandler(db *database.DB, cfg *config.Config, store *storage.Storage) *Handler {
	return &Handler{
		db:      db,
		cfg:     cfg,
		storage: store,
		started: time.Now(),
	}
}

// ----------------- HEALTH CHECKS -----------------

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":      "ok",
		"service":     "pawsos-backend",
		"uptime":      time.Since(h.started).String(),
		"environment": h.cfg.Environment,
	})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE", "Database connection is unhealthy")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ready",
		"database": "connected",
		"driver":   h.cfg.DBDriver,
	})
}

// ----------------- AUTHENTICATION -----------------

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	req.Email = strings.TrimSpace(req.Email)
	req.Name = strings.TrimSpace(req.Name)
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "A valid email address is required")
		return
	}
	if len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Password must be at least 6 characters")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Full name is required")
		return
	}

	role := req.Role
	if role != models.RoleResponder && role != models.RoleAdmin {
		role = models.RoleCitizen
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "Failed to secure password")
		return
	}

	user := &models.User{
		ID:           uuid.New().String(),
		Name:         req.Name,
		Email:        req.Email,
		PasswordHash: hash,
		Role:         role,
		Phone:        req.Phone,
		Organization: req.Organization,
		CreatedAt:    time.Now(),
	}

	if err := h.db.CreateUser(r.Context(), user); err != nil {
		if errors.Is(err, database.ErrConflict) {
			writeError(w, http.StatusConflict, "EMAIL_EXISTS", "An account with this email already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to register user")
		return
	}

	token, err := auth.GenerateToken(user, h.cfg.JWTSecret, 7*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TOKEN_ERROR", "Failed to generate session token")
		return
	}

	writeJSON(w, http.StatusCreated, models.AuthResponse{
		Token: token,
		User:  *user,
	})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Email and password are required")
		return
	}

	user, err := h.db.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid email or password")
		return
	}

	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid email or password")
		return
	}

	token, err := auth.GenerateToken(user, h.cfg.JWTSecret, 7*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TOKEN_ERROR", "Failed to generate session token")
		return
	}

	writeJSON(w, http.StatusOK, models.AuthResponse{
		Token: token,
		User:  *user,
	})
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(middleware.UserClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Missing session")
		return
	}

	user, err := h.db.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "USER_NOT_FOUND", "User profile not found")
		return
	}

	writeJSON(w, http.StatusOK, user)
}

// ----------------- REPORTS -----------------

func generateReportID() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(9000))
	return fmt.Sprintf("PAW-%04d", n.Int64()+1000)
}

func (h *Handler) CreateReport(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")

	var report models.Report
	var photoURL string

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Limit multipart request to max upload size + 1MB overhead
		r.Body = http.MaxBytesReader(w, r.Body, (h.cfg.MaxUploadSizeMB+1)*1024*1024)
		if err := r.ParseMultipartForm(h.cfg.MaxUploadSizeMB * 1024 * 1024); err != nil {
			writeError(w, http.StatusBadRequest, "UPLOAD_ERROR", "Failed to parse upload form or file exceeds limit")
			return
		}

		report.AnimalType = strings.TrimSpace(r.FormValue("animal_type"))
		report.AnimalName = strings.TrimSpace(r.FormValue("animal_name"))
		report.Urgency = models.UrgencyLevel(strings.TrimSpace(r.FormValue("urgency")))
		report.Condition = strings.TrimSpace(r.FormValue("condition"))
		report.Description = strings.TrimSpace(r.FormValue("description"))
		report.Address = strings.TrimSpace(r.FormValue("address"))
		report.Area = strings.TrimSpace(r.FormValue("area"))
		report.City = strings.TrimSpace(r.FormValue("city"))
		report.ReporterName = strings.TrimSpace(r.FormValue("reporter_name"))
		report.ReporterPhone = strings.TrimSpace(r.FormValue("reporter_phone"))

		latStr := r.FormValue("latitude")
		lngStr := r.FormValue("longitude")
		if lat, err := strconv.ParseFloat(latStr, 64); err == nil {
			report.Latitude = lat
		}
		if lng, err := strconv.ParseFloat(lngStr, 64); err == nil {
			report.Longitude = lng
		}

		// Handle photo upload if attached
		file, fileHeader, err := r.FormFile("photo")
		if err == nil {
			defer file.Close()
			savedPath, err := h.storage.SaveImage(file, fileHeader.Filename)
			if err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_IMAGE", err.Error())
				return
			}
			photoURL = savedPath
		}
	} else {
		// JSON payload
		var req models.CreateReportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
			return
		}

		report.AnimalType = strings.TrimSpace(req.AnimalType)
		report.AnimalName = strings.TrimSpace(req.AnimalName)
		report.Urgency = req.Urgency
		report.Condition = strings.TrimSpace(req.Condition)
		report.Description = strings.TrimSpace(req.Description)
		report.Address = strings.TrimSpace(req.Address)
		report.Area = strings.TrimSpace(req.Area)
		report.City = strings.TrimSpace(req.City)
		report.Latitude = req.Latitude
		report.Longitude = req.Longitude
		report.ReporterName = strings.TrimSpace(req.ReporterName)
		report.ReporterPhone = strings.TrimSpace(req.ReporterPhone)
	}

	// Validation
	if report.AnimalType == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Animal type is required")
		return
	}
	if report.Condition == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Condition / injury summary is required")
		return
	}
	if report.Address == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Incident location / address is required")
		return
	}
	if report.ReporterName == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Reporter name is required")
		return
	}
	if report.ReporterPhone == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Reporter phone number is required")
		return
	}

	if report.Urgency == "" {
		report.Urgency = models.UrgencyUrgent
	}

	now := time.Now()
	report.ID = generateReportID()
	report.Status = models.StatusReported
	report.PhotoURL = photoURL
	report.CreatedAt = now
	report.UpdatedAt = now

	// If coordinates weren't passed, assign standard city fallback coordinates
	if report.Latitude == 0 && report.Longitude == 0 {
		report.Latitude = 37.7749
		report.Longitude = -122.4194
	}

	if err := h.db.CreateReport(r.Context(), &report); err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to save report: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, report)
}

func (h *Handler) GetReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	filter := database.ReportFilter{
		Status:  q.Get("status"),
		Urgency: q.Get("urgency"),
		Search:  q.Get("search"),
		Limit:   limit,
		Offset:  offset,
	}

	reports, total, err := h.db.GetReports(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to query reports")
		return
	}

	// Check if requester is an authorized responder or admin
	isAuthorized := false
	if claims, ok := r.Context().Value(middleware.UserClaimsKey).(*auth.Claims); ok && claims != nil {
		if claims.Role == models.RoleResponder || claims.Role == models.RoleAdmin {
			isAuthorized = true
		}
	}

	// Mask phone numbers for public privacy
	masked := make([]*models.Report, len(reports))
	for i, rep := range reports {
		masked[i] = rep.MaskedReport(isAuthorized)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"reports": masked,
		"total":   total,
		"limit":   filter.Limit,
		"offset":  filter.Offset,
	})
}

func (h *Handler) GetReportByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		id = strings.TrimPrefix(r.URL.Path, "/api/v1/reports/")
	}

	report, err := h.db.GetReportByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "REPORT_NOT_FOUND", "Incident report not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to retrieve report")
		return
	}

	isAuthorized := false
	if claims, ok := r.Context().Value(middleware.UserClaimsKey).(*auth.Claims); ok && claims != nil {
		if claims.Role == models.RoleResponder || claims.Role == models.RoleAdmin {
			isAuthorized = true
		}
	}

	writeJSON(w, http.StatusOK, report.MaskedReport(isAuthorized))
}

func (h *Handler) AssignReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 4 {
			id = parts[3]
		}
	}

	var req models.AssignReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	claims, ok := r.Context().Value(middleware.UserClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Responder login required")
		return
	}

	responderID := req.ResponderID
	responderName := req.ResponderName
	if responderName == "" {
		responderName = claims.Name
	}
	if responderID == "" {
		responderID = claims.UserID
	}
	eta := req.ETA
	if eta == "" {
		eta = "15-20 minutes"
	}

	if err := h.db.AssignReport(r.Context(), id, responderID, responderName, eta, string(claims.Role)); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "REPORT_NOT_FOUND", "Incident report not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to assign case")
		return
	}

	updated, _ := h.db.GetReportByID(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 4 {
			id = parts[3]
		}
	}

	var req models.UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	validStatuses := map[models.ReportStatus]bool{
		models.StatusReported:   true,
		models.StatusAssigned:   true,
		models.StatusInProgress: true,
		models.StatusResolved:   true,
		models.StatusClosed:     true,
	}

	if !validStatuses[req.Status] {
		writeError(w, http.StatusBadRequest, "INVALID_STATUS", "Invalid status value provided")
		return
	}

	claims, ok := r.Context().Value(middleware.UserClaimsKey).(*auth.Claims)
	authorName := "Dispatcher"
	authorRole := "responder"
	if ok && claims != nil {
		authorName = claims.Name
		authorRole = string(claims.Role)
	}

	if err := h.db.UpdateReportStatus(r.Context(), id, req.Status, authorName, authorRole, req.Note); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "REPORT_NOT_FOUND", "Incident report not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to update status")
		return
	}

	updated, _ := h.db.GetReportByID(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) AddNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req models.AddNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	if strings.TrimSpace(req.Note) == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Note message cannot be empty")
		return
	}

	report, err := h.db.GetReportByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "REPORT_NOT_FOUND", "Incident report not found")
		return
	}

	authorName := req.AuthorName
	authorRole := "responder"
	if claims, ok := r.Context().Value(middleware.UserClaimsKey).(*auth.Claims); ok && claims != nil {
		authorName = claims.Name
		authorRole = string(claims.Role)
	}
	if authorName == "" {
		authorName = "Authorized Personnel"
	}

	entry := &models.ReportTimelineEntry{
		ReportID:   id,
		Status:     report.Status,
		AuthorName: authorName,
		AuthorRole: authorRole,
		Note:       req.Note,
		CreatedAt:  time.Now(),
	}

	if err := h.db.AddTimelineEntry(r.Context(), entry); err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to add timeline entry")
		return
	}

	writeJSON(w, http.StatusCreated, entry)
}

func (h *Handler) ExportReports(w http.ResponseWriter, r *http.Request) {
	reports, err := h.db.GetAllReportsForExport(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to retrieve export records")
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=pawsos_incident_export_%s.csv", time.Now().Format("20060102_150405")))

	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Header row
	_ = writer.Write([]string{
		"Report ID", "Animal Type", "Urgency", "Condition", "Address", "Area", "City",
		"Status", "Reporter Name", "Reporter Phone", "Assigned Responder", "Reported At",
	})

	for _, rep := range reports {
		assigned := "Unassigned"
		if rep.AssignedResponderName != nil {
			assigned = *rep.AssignedResponderName
		}

		_ = writer.Write([]string{
			rep.ID,
			rep.AnimalType,
			string(rep.Urgency),
			rep.Condition,
			rep.Address,
			rep.Area,
			rep.City,
			string(rep.Status),
			rep.ReporterName,
			rep.ReporterPhone,
			assigned,
			rep.CreatedAt.Format(time.RFC3339),
		})
	}
}

// ----------------- STATS -----------------

func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.db.GetStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to retrieve statistics")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// ----------------- HELPERS -----------------

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
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
