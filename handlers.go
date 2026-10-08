package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"net/mail"
	"path/filepath"
	"sort"
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
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	req.Email = strings.TrimSpace(req.Email)
	req.Email = strings.ToLower(req.Email)
	req.Name = strings.TrimSpace(req.Name)
	parsedEmail, emailErr := mail.ParseAddress(req.Email)
	if emailErr != nil || parsedEmail.Address != req.Email || len(req.Email) > 254 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "A valid email address is required")
		return
	}
	if len(req.Password) < 12 || len(req.Password) > 72 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Password must be between 12 and 72 bytes.")
		return
	}
	if req.Name == "" || len(req.Name) > 120 || len(req.Phone) > 64 || len(req.Organization) > 255 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Enter a name and keep contact/organization details within the allowed length.")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SERVER_ERROR", "Failed to secure password")
		return
	}

	user := &models.User{
		ID:            uuid.New().String(),
		Name:          req.Name,
		Email:         req.Email,
		PasswordHash:  hash,
		Role:          models.RoleCitizen,
		RequestedRole: requestedRole(req.Role),
		Phone:         req.Phone,
		Organization:  req.Organization,
		CreatedAt:     time.Now(),
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
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Email and password are required")
		return
	}
	if len(req.Password) > 72 {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid email or password")
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

func requestedRole(role models.UserRole) models.UserRole {
	if role == models.RoleResponder {
		return models.RoleResponder
	}
	return models.RoleCitizen
}

func (h *Handler) CreateReport(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	var err error

	var report models.Report
	var photoFile multipart.File
	var photoHeader *multipart.FileHeader
	var photoSource string
	var photoCapturedAt *time.Time
	var photoLocationAccuracy *float64
	var photoLatitude, photoLongitude *float64
	var confirmDuplicate bool

	if strings.HasPrefix(contentType, "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, (h.cfg.MaxUploadSizeMB+2)*1024*1024)
		if err := r.ParseMultipartForm(h.cfg.MaxUploadSizeMB * 1024 * 1024); err != nil {
			writeError(w, http.StatusBadRequest, "UPLOAD_ERROR", "That photo is too large or could not be read. Please choose a smaller image.")
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
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
		if err := assignCoordinates(&report, r.FormValue("latitude"), r.FormValue("longitude")); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_LOCATION", "Please capture your location or place the map pin before submitting.")
			return
		}
		report.LocationSource = strings.TrimSpace(r.FormValue("location_source"))
		report.LocationAccuracy, err = parseOptionalFloat(r.FormValue("location_accuracy"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_LOCATION", "Location accuracy must be a valid number.")
			return
		}
		report.LocationTimestamp, err = parseOptionalTime(r.FormValue("location_timestamp"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_LOCATION", "Location timestamp must be a valid date and time.")
			return
		}
		photoSource = strings.TrimSpace(r.FormValue("photo_source"))
		photoCapturedAt, err = parseOptionalTime(r.FormValue("photo_captured_at"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_IMAGE_METADATA", "Photo selection timestamp must be a valid date and time.")
			return
		}
		photoLocationAccuracy, err = parseOptionalFloat(r.FormValue("photo_location_accuracy"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_IMAGE_LOCATION", "Photo location accuracy must be a valid number.")
			return
		}
		photoLatitude, err = parseOptionalFloat(r.FormValue("photo_latitude"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_IMAGE_LOCATION", "Photo latitude must be a valid number.")
			return
		}
		photoLongitude, err = parseOptionalFloat(r.FormValue("photo_longitude"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_IMAGE_LOCATION", "Photo longitude must be a valid number.")
			return
		}
		if (photoLatitude == nil) != (photoLongitude == nil) ||
			(photoLatitude != nil && !validCoordinates(*photoLatitude, *photoLongitude)) {
			writeError(w, http.StatusBadRequest, "INVALID_IMAGE_LOCATION", "Photo location must include valid latitude and longitude.")
			return
		}
		confirmDuplicate = r.FormValue("confirm_duplicate") == "true"
		if file, fileHeader, err := r.FormFile("photo"); err == nil {
			photoFile, photoHeader = file, fileHeader
		} else if !errors.Is(err, http.ErrMissingFile) {
			writeError(w, http.StatusBadRequest, "INVALID_IMAGE", "The selected photo could not be read.")
			return
		}
	} else {
		var req models.CreateReportRequest
		if err := decodeJSONRequest(w, r, &req); err != nil {
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
		if req.Latitude == nil || req.Longitude == nil {
			writeError(w, http.StatusBadRequest, "INVALID_LOCATION", "Please capture your location or place the map pin before submitting.")
			return
		}
		report.Latitude = *req.Latitude
		report.Longitude = *req.Longitude
		report.LocationAccuracy = req.LocationAccuracy
		report.LocationTimestamp = req.LocationTimestamp
		report.LocationSource = strings.TrimSpace(req.LocationSource)
		report.ReporterName = strings.TrimSpace(req.ReporterName)
		report.ReporterPhone = strings.TrimSpace(req.ReporterPhone)
		photoSource = strings.TrimSpace(req.PhotoSource)
		photoCapturedAt = req.PhotoCapturedAt
		photoLocationAccuracy = req.PhotoLocationAccuracy
		if (req.PhotoLatitude == nil) != (req.PhotoLongitude == nil) ||
			(req.PhotoLatitude != nil && !validCoordinates(*req.PhotoLatitude, *req.PhotoLongitude)) {
			writeError(w, http.StatusBadRequest, "INVALID_IMAGE_LOCATION", "Photo location must include valid latitude and longitude.")
			return
		}
		photoLatitude, photoLongitude = req.PhotoLatitude, req.PhotoLongitude
		confirmDuplicate = req.ConfirmDuplicate
	}

	if report.AnimalType == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Animal type is required")
		return
	}
	if report.Condition == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Condition / injury summary is required")
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
	if len(report.AnimalType) > 64 || len(report.AnimalName) > 128 || len(report.Condition) > 500 ||
		len(report.Description) > 3000 || len(report.Address) > 500 || len(report.Area) > 128 ||
		len(report.City) > 128 || len(report.ReporterName) > 120 || len(report.ReporterPhone) > 64 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "One or more report details are too long. Shorten them and try again.")
		return
	}

	if report.Urgency != models.UrgencyCritical && report.Urgency != models.UrgencyUrgent && report.Urgency != models.UrgencyModerate {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Choose critical, urgent, or moderate severity.")
		return
	}
	if !validCoordinates(report.Latitude, report.Longitude) {
		writeError(w, http.StatusBadRequest, "INVALID_LOCATION", "The location is outside the valid range. Please adjust the map pin.")
		return
	}
	if report.LocationAccuracy != nil && (*report.LocationAccuracy <= 0 || *report.LocationAccuracy > 100000) {
		writeError(w, http.StatusBadRequest, "INVALID_LOCATION", "Location accuracy is invalid. Please capture location again or place the pin manually.")
		return
	}
	if photoLocationAccuracy != nil && (*photoLocationAccuracy <= 0 || *photoLocationAccuracy > 100000) {
		writeError(w, http.StatusBadRequest, "INVALID_IMAGE_LOCATION", "Photo location accuracy is invalid.")
		return
	}
	if report.LocationTimestamp != nil && (report.LocationTimestamp.After(time.Now().Add(5*time.Minute)) || time.Since(*report.LocationTimestamp) > 24*time.Hour) {
		writeError(w, http.StatusBadRequest, "INVALID_LOCATION", "The location capture time is invalid. Please capture location again.")
		return
	}
	if photoCapturedAt != nil && (photoCapturedAt.After(time.Now().Add(5*time.Minute)) || time.Since(*photoCapturedAt) > 24*time.Hour) {
		writeError(w, http.StatusBadRequest, "INVALID_IMAGE_METADATA", "The photo selection time is invalid. Please select the photo again.")
		return
	}
	if report.LocationTimestamp == nil {
		now := time.Now()
		report.LocationTimestamp = &now
	}
	if report.LocationSource == "" {
		report.LocationSource = "map_pin"
	}
	if report.LocationSource != "browser_geolocation" && report.LocationSource != "map_pin" {
		writeError(w, http.StatusBadRequest, "INVALID_LOCATION", "Location source must be browser location or a manually placed map pin.")
		return
	}
	if photoSource != "" && photoSource != "camera" && photoSource != "gallery" {
		writeError(w, http.StatusBadRequest, "INVALID_IMAGE", "Choose Take Photo or Existing Photo.")
		return
	}

	duplicate, err := h.db.FindDuplicateReport(r.Context(), report.AnimalType, report.Latitude, report.Longitude)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Could not check for a nearby report. Please try again.")
		return
	}
	if duplicate != nil {
		if !confirmDuplicate {
			writeJSON(w, http.StatusConflict, map[string]interface{}{
				"error":              map[string]string{"code": "POSSIBLE_DUPLICATE", "message": "There is already a recent report for a similar animal nearby. Check it before creating another case."},
				"possible_duplicate": map[string]string{"report_id": duplicate.ID},
			})
			return
		}
	}

	now := time.Now()
	report.ID = "PAW-" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))
	report.Status = models.StatusReported
	report.CreatedAt = now
	report.UpdatedAt = now

	if photoFile != nil {
		defer photoFile.Close()
		originalFilename := filepath.Base(photoHeader.Filename)
		if len(originalFilename) > 255 {
			writeError(w, http.StatusBadRequest, "INVALID_IMAGE", "The selected filename is too long. Rename the file and try again.")
			return
		}
		saved, saveErr := h.storage.SaveImage(photoFile, filepath.Base(photoHeader.Filename))
		if saveErr != nil {
			if errors.Is(saveErr, storage.ErrFileTooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "That photo is too large. Please choose a smaller image.")
				return
			}
			if errors.Is(saveErr, storage.ErrInvalidFileType) || errors.Is(saveErr, storage.ErrEmptyFile) {
				writeError(w, http.StatusBadRequest, "INVALID_IMAGE", "Choose a valid JPG or PNG photo.")
				return
			}
			writeError(w, http.StatusBadRequest, "INVALID_IMAGE", "That photo could not be validated. Choose another image.")
			return
		}
		report.PhotoURL = saved.Path
		captureTime := photoCapturedAt
		evidenceAccuracy := photoLocationAccuracy
		if evidenceAccuracy == nil {
			evidenceAccuracy = report.LocationAccuracy
		}
		evidenceLatitude, evidenceLongitude := report.Latitude, report.Longitude
		if photoLatitude != nil && photoLongitude != nil {
			evidenceLatitude = *photoLatitude
			evidenceLongitude = *photoLongitude
		}
		report.Evidence = &models.Evidence{
			ID:               "PSE-" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")),
			OriginalFilename: originalFilename,
			MIMEType:         saved.MIMEType,
			FileSize:         saved.Size,
			SHA256:           saved.SHA256,
			CaptureTimestamp: captureTime,
			UploadTimestamp:  now,
			Latitude:         &evidenceLatitude,
			Longitude:        &evidenceLongitude,
			LocationAccuracy: evidenceAccuracy,
			SourceType:       photoSourceOrGallery(photoSource),
			StorageReference: saved.OriginalReference,
		}
	}
	if err := h.db.CreateReport(r.Context(), &report); err != nil {
		if report.Evidence != nil {
			if cleanupErr := h.storage.DeleteFile(report.PhotoURL); cleanupErr != nil {
				log.Printf("[PAWSOS ERROR] Failed to remove image after report transaction failure (%s): %v", report.ID, cleanupErr)
			}
		}
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Your report could not be saved. Please try again.")
		return
	}

	writeJSON(w, http.StatusCreated, report)
}

func assignCoordinates(report *models.Report, latitude, longitude string) error {
	lat, err := strconv.ParseFloat(latitude, 64)
	if err != nil {
		return err
	}
	lng, err := strconv.ParseFloat(longitude, 64)
	if err != nil {
		return err
	}
	report.Latitude, report.Longitude = lat, lng
	return nil
}

func validCoordinates(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsInf(latitude, 0) && !math.IsNaN(longitude) && !math.IsInf(longitude, 0) &&
		latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}

func parseOptionalFloat(value string) (*float64, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil, errors.New("invalid number")
	}
	return &parsed, nil
}

func parseOptionalTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func photoSourceOrGallery(source string) string {
	if source == "camera" {
		return "camera_option"
	}
	return "gallery_upload"
}

func (h *Handler) GetReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	if rawLimit := q.Get("limit"); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit < 1 || parsedLimit > 100 {
			writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", "Choose a report page size between 1 and 100.")
			return
		}
		limit = parsedLimit
	}
	offset := 0
	if rawOffset := q.Get("offset"); rawOffset != "" {
		parsedOffset, err := strconv.Atoi(rawOffset)
		if err != nil || parsedOffset < 0 {
			writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", "Report page is invalid.")
			return
		}
		offset = parsedOffset
	}
	latitude, hasLatitude := parseQueryFloat(q.Get("latitude"))
	longitude, hasLongitude := parseQueryFloat(q.Get("longitude"))
	nearby := q.Has("latitude") || q.Has("longitude")
	if nearby && (!hasLatitude || !hasLongitude || !validCoordinates(latitude, longitude)) {
		writeError(w, http.StatusBadRequest, "INVALID_LOCATION", "A valid latitude and longitude are required to find nearby reports.")
		return
	}

	radiusKm := 5.0
	var err error
	if rawRadius := q.Get("radius_km"); rawRadius != "" {
		radiusKm, err = strconv.ParseFloat(rawRadius, 64)
		if err != nil || radiusKm <= 0 || radiusKm > 50 {
			writeError(w, http.StatusBadRequest, "INVALID_RADIUS", "Choose a search radius between 0 and 50 km.")
			return
		}
	}

	filter := database.ReportFilter{
		Status:  q.Get("status"),
		Urgency: q.Get("urgency"),
		Search:  q.Get("search"),
		Limit:   limit,
		Offset:  offset,
	}
	if nearby && filter.Status == "" {
		filter.Status = "active"
	}

	reports, total, err := h.db.GetReports(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to query reports")
		return
	}

	if nearby {
		preciseLocationAccess := h.hasPreciseLocationAccess(r.Context(), claimsFromRequest(r))
		filtered := make([]*models.Report, 0, len(reports))
		for _, report := range reports {
			target := report
			if !preciseLocationAccess {
				target = report.PublicReport()
			}
			distance := distanceMeters(latitude, longitude, target.Latitude, target.Longitude)
			if float64(distance) > radiusKm*1000 {
				continue
			}
			report.DistanceMeters = &distance
			filtered = append(filtered, report)
		}
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].Urgency != filtered[j].Urgency {
				return urgencyRank(filtered[i].Urgency) < urgencyRank(filtered[j].Urgency)
			}
			return *filtered[i].DistanceMeters < *filtered[j].DistanceMeters
		})
		for _, report := range filtered {
			approximateDistance := int(math.Round(float64(*report.DistanceMeters)/100) * 100)
			report.DistanceMeters = &approximateDistance
		}
		reports = filtered
		total = int64(len(reports))
	}

	masked := make([]*models.Report, len(reports))
	for i, rep := range reports {
		masked[i] = h.projectReport(r.Context(), rep, claimsFromRequest(r))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"reports": masked,
		"total":   total,
		"limit":   filter.Limit,
		"offset":  filter.Offset,
	})
}

func (h *Handler) hasPreciseLocationAccess(ctx context.Context, claims *auth.Claims) bool {
	_, err := h.authorizedResponder(ctx, claims)
	return err == nil
}

func parseQueryFloat(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(raw, 64)
	return value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func distanceMeters(lat1, lng1, lat2, lng2 float64) int {
	const earthRadius = 6371000
	toRadians := func(value float64) float64 { return value * math.Pi / 180 }
	dLat, dLng := toRadians(lat2-lat1), toRadians(lng2-lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRadians(lat1))*math.Cos(toRadians(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return int(math.Round(earthRadius * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))))
}

func urgencyRank(level models.UrgencyLevel) int {
	switch level {
	case models.UrgencyCritical:
		return 0
	case models.UrgencyUrgent:
		return 1
	default:
		return 2
	}
}

func claimsFromRequest(r *http.Request) *auth.Claims {
	claims, _ := r.Context().Value(middleware.UserClaimsKey).(*auth.Claims)
	return claims
}

func (h *Handler) projectReport(ctx context.Context, report *models.Report, claims *auth.Claims) *models.Report {
	if claims == nil {
		return report.PublicReport()
	}
	user, err := h.db.GetUserByID(ctx, claims.UserID)
	if err != nil || (user.Role != models.RoleAdmin && (user.Role != models.RoleResponder || !user.Verified)) {
		return report.PublicReport()
	}
	if user.Role == models.RoleAdmin {
		return report
	}
	projected := *report
	if report.AssignedResponderID == nil || *report.AssignedResponderID != user.ID {
		projected.ReporterName = ""
		projected.ReporterPhone = ""
		projected.Evidence = nil
	}
	return &projected
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
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Could not load incident report")
		return
	}

	writeJSON(w, http.StatusOK, h.projectReport(r.Context(), report, claimsFromRequest(r)))
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
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	claims, ok := r.Context().Value(middleware.UserClaimsKey).(*auth.Claims)
	if !ok || claims == nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Responder login required")
		return
	}

	actor, err := h.db.GetUserByID(r.Context(), claims.UserID)
	if err != nil || (actor.Role != models.RoleAdmin && (actor.Role != models.RoleResponder || !actor.Verified)) {
		writeError(w, http.StatusForbidden, "RESPONDER_NOT_VERIFIED", "Only verified responders can accept a case.")
		return
	}
	report, err := h.db.GetReportByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "REPORT_NOT_FOUND", "Incident report not found")
		return
	}
	responderID := actor.ID
	if actor.Role == models.RoleAdmin && req.ResponderID != "" {
		responderID = req.ResponderID
	}
	responder, err := h.db.GetUserByID(r.Context(), responderID)
	if err != nil || responder.Role != models.RoleResponder || !responder.Verified {
		writeError(w, http.StatusBadRequest, "INVALID_RESPONDER", "Select a verified responder.")
		return
	}
	if actor.Role != models.RoleAdmin && report.AssignedResponderID != nil && *report.AssignedResponderID != actor.ID {
		writeError(w, http.StatusConflict, "CASE_ALREADY_ASSIGNED", "This case has already been accepted by another responder.")
		return
	}
	eta := req.ETA
	if len(eta) > 128 {
		writeError(w, http.StatusBadRequest, "INVALID_ETA", "ETA must be 128 characters or fewer.")
		return
	}

	if err := h.db.AssignReport(r.Context(), id, responderID, responder.Name, eta, string(actor.Role)); err != nil {
		if errors.Is(err, database.ErrConflict) {
			writeError(w, http.StatusConflict, "CASE_ALREADY_ASSIGNED", "This case has already been accepted by another responder.")
			return
		}
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "REPORT_NOT_FOUND", "Incident report not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to assign case")
		return
	}

	updated, err := h.db.GetReportByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "REPORT_RELOAD_FAILED", "The case was accepted, but its updated details could not be reloaded.")
		return
	}
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
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	validStatuses := map[models.ReportStatus]bool{
		models.StatusReported:      true,
		models.StatusAssigned:      true,
		models.StatusInProgress:    true,
		models.StatusOnTheWay:      true,
		models.StatusArrived:       true,
		models.StatusAnimalSecured: true,
		models.StatusTransporting:  true,
		models.StatusAtVet:         true,
		models.StatusResolved:      true,
		models.StatusClosed:        true,
	}

	if !validStatuses[req.Status] {
		writeError(w, http.StatusBadRequest, "INVALID_STATUS", "Invalid status value provided")
		return
	}

	claims := claimsFromRequest(r)
	actor, err := h.authorizedResponder(r.Context(), claims)
	if err != nil {
		writeError(w, http.StatusForbidden, "RESPONDER_NOT_VERIFIED", "Only verified responders can update a case.")
		return
	}
	report, err := h.db.GetReportByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "REPORT_NOT_FOUND", "Incident report not found")
		return
	}
	if actor.Role != models.RoleAdmin && (report.AssignedResponderID == nil || *report.AssignedResponderID != actor.ID) {
		writeError(w, http.StatusForbidden, "CASE_NOT_ASSIGNED", "Only the responder assigned to this case can update it.")
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if len(req.Note) > 500 {
		writeError(w, http.StatusBadRequest, "NOTE_TOO_LONG", "Field notes must be 500 characters or fewer.")
		return
	}

	if err := h.db.UpdateReportStatus(r.Context(), id, req.Status, actor.Name, string(actor.Role), req.Note); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "REPORT_NOT_FOUND", "Incident report not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to update status")
		return
	}

	updated, err := h.db.GetReportByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "REPORT_RELOAD_FAILED", "The status update was recorded, but the updated report could not be reloaded.")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) AddNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req models.AddNoteRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}

	req.Note = strings.TrimSpace(req.Note)
	if req.Note == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Note message cannot be empty")
		return
	}
	if len(req.Note) > 500 {
		writeError(w, http.StatusBadRequest, "NOTE_TOO_LONG", "Field notes must be 500 characters or fewer.")
		return
	}

	claims := claimsFromRequest(r)
	actor, authErr := h.authorizedResponder(r.Context(), claims)
	if authErr != nil {
		writeError(w, http.StatusForbidden, "RESPONDER_NOT_VERIFIED", "Only verified responders can add field notes.")
		return
	}
	report, err := h.db.GetReportByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "REPORT_NOT_FOUND", "Incident report not found")
		return
	}

	if actor.Role != models.RoleAdmin && (report.AssignedResponderID == nil || *report.AssignedResponderID != actor.ID) {
		writeError(w, http.StatusForbidden, "CASE_NOT_ASSIGNED", "Only the responder assigned to this case can add field notes.")
		return
	}

	entry := &models.ReportTimelineEntry{
		ReportID:   id,
		Status:     report.Status,
		AuthorName: actor.Name,
		AuthorRole: string(actor.Role),
		Note:       req.Note,
		CreatedAt:  time.Now(),
	}

	if err := h.db.AddTimelineEntry(r.Context(), entry); err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to add timeline entry")
		return
	}

	writeJSON(w, http.StatusCreated, entry)
}

func (h *Handler) authorizedResponder(ctx context.Context, claims *auth.Claims) (*models.User, error) {
	if claims == nil {
		return nil, errors.New("missing identity")
	}
	user, err := h.db.GetUserByID(ctx, claims.UserID)
	if err != nil {
		return nil, err
	}
	if user.Role != models.RoleAdmin && (user.Role != models.RoleResponder || !user.Verified) {
		return nil, errors.New("responder is not verified")
	}
	return user, nil
}

func (h *Handler) GetPendingResponders(w http.ResponseWriter, r *http.Request) {
	if _, err := h.authorizedAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, "ADMIN_REQUIRED", "Administrator access is required.")
		return
	}
	users, err := h.db.GetPendingResponders(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Could not load responder applications.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"responders": users})
}

func (h *Handler) ApplyResponder(w http.ResponseWriter, r *http.Request) {
	claims := claimsFromRequest(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Sign in before requesting responder verification.")
		return
	}
	var req models.ResponderApplicationRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid responder application.")
		return
	}
	req.Phone = strings.TrimSpace(req.Phone)
	req.Organization = strings.TrimSpace(req.Organization)
	if len(req.Phone) > 64 || len(req.Organization) > 255 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Contact and organization details exceed the allowed length.")
		return
	}
	if err := h.db.RequestResponderReview(r.Context(), claims.UserID, req.Phone, req.Organization); err != nil {
		if errors.Is(err, database.ErrConflict) {
			writeError(w, http.StatusConflict, "APPLICATION_NOT_ALLOWED", "This account cannot submit another responder application.")
			return
		}
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Could not submit the responder application.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending_review"})
}

func (h *Handler) VerifyResponder(w http.ResponseWriter, r *http.Request) {
	if _, err := h.authorizedAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, "ADMIN_REQUIRED", "Administrator access is required.")
		return
	}
	var req models.ResponderVerificationRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body")
		return
	}
	id := r.PathValue("id")
	if err := h.db.VerifyResponder(r.Context(), id, req.Verified); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESPONDER_NOT_FOUND", "Responder application not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Could not update responder verification.")
		return
	}
	user, err := h.db.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Responder was updated but the profile could not be retrieved.")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (h *Handler) authorizedAdmin(r *http.Request) (*models.User, error) {
	claims := claimsFromRequest(r)
	user, err := h.authorizedResponder(r.Context(), claims)
	if err != nil || user.Role != models.RoleAdmin {
		return nil, errors.New("administrator access is required")
	}
	return user, nil
}

func (h *Handler) ExportReports(w http.ResponseWriter, r *http.Request) {
	if _, err := h.authorizedAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, "ADMIN_REQUIRED", "Administrator access is required.")
		return
	}
	reports, err := h.db.GetAllReportsForExport(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "Failed to retrieve export records")
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=pawsos_incident_export_%s.csv", time.Now().Format("20060102_150405")))

	writer := csv.NewWriter(w)

	// Header row
	if err := writer.Write([]string{
		"Report ID", "Animal Type", "Urgency", "Condition", "Address", "Area", "City",
		"Status", "Reporter Name", "Reporter Phone", "Assigned Responder", "Reported At",
	}); err != nil {
		log.Printf("[PAWSOS ERROR] Failed to write report export header: %v", err)
		return
	}

	for _, rep := range reports {
		assigned := "Unassigned"
		if rep.AssignedResponderName != nil {
			assigned = *rep.AssignedResponderName
		}

		values := []string{
			rep.ID, rep.AnimalType, string(rep.Urgency), rep.Condition, rep.Address, rep.Area,
			rep.City, string(rep.Status), rep.ReporterName, rep.ReporterPhone, assigned,
			rep.CreatedAt.Format(time.RFC3339),
		}
		for i := range values {
			values[i] = safeCSVCell(values[i])
		}
		if err := writer.Write(values); err != nil {
			log.Printf("[PAWSOS ERROR] Failed while writing report export: %v", err)
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		log.Printf("[PAWSOS ERROR] Failed to flush report export: %v", err)
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

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, destination interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request contains multiple JSON values")
		}
		return err
	}
	return nil
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

func safeCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", []rune(trimmed)[0]) {
		return "'" + value
	}
	return value
}
