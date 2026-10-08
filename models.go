package models

import (
	"math"
	"time"
)

type UserRole string

const (
	RoleCitizen   UserRole = "citizen"
	RoleResponder UserRole = "responder"
	RoleAdmin     UserRole = "admin"
)

type User struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Email         string    `json:"email"`
	PasswordHash  string    `json:"-"`
	Role          UserRole  `json:"role"`
	RequestedRole UserRole  `json:"requested_role,omitempty"`
	Verified      bool      `json:"verified"`
	Phone         string    `json:"phone,omitempty"`
	Organization  string    `json:"organization,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type ReportStatus string

const (
	StatusReported      ReportStatus = "reported"
	StatusAssigned      ReportStatus = "assigned"
	StatusInProgress    ReportStatus = "in_progress"
	StatusOnTheWay      ReportStatus = "on_the_way"
	StatusArrived       ReportStatus = "arrived"
	StatusAnimalSecured ReportStatus = "animal_secured"
	StatusTransporting  ReportStatus = "transporting"
	StatusAtVet         ReportStatus = "at_vet"
	StatusResolved      ReportStatus = "resolved"
	StatusClosed        ReportStatus = "closed"
)

type UrgencyLevel string

const (
	UrgencyCritical UrgencyLevel = "critical"
	UrgencyUrgent   UrgencyLevel = "urgent"
	UrgencyModerate UrgencyLevel = "moderate"
)

type Report struct {
	ID                    string                `json:"id"`
	AnimalType            string                `json:"animal_type"`
	AnimalName            string                `json:"animal_name"`
	Urgency               UrgencyLevel          `json:"urgency"`
	Condition             string                `json:"condition"`
	Description           string                `json:"description,omitempty"`
	PhotoURL              string                `json:"photo_url,omitempty"`
	Status                ReportStatus          `json:"status"`
	ReporterName          string                `json:"reporter_name,omitempty"`
	ReporterPhone         string                `json:"reporter_phone,omitempty"`
	Address               string                `json:"address,omitempty"`
	Area                  string                `json:"area,omitempty"`
	City                  string                `json:"city,omitempty"`
	Latitude              float64               `json:"latitude"`
	Longitude             float64               `json:"longitude"`
	LocationAccuracy      *float64              `json:"location_accuracy,omitempty"`
	LocationTimestamp     *time.Time            `json:"location_timestamp,omitempty"`
	LocationSource        string                `json:"location_source,omitempty"`
	ETA                   string                `json:"eta,omitempty"`
	DistanceMeters        *int                  `json:"distance_meters,omitempty"`
	Evidence              *Evidence             `json:"evidence,omitempty"`
	AssignedResponderID   *string               `json:"assigned_responder_id,omitempty"`
	AssignedResponderName *string               `json:"assigned_responder_name,omitempty"`
	CreatedAt             time.Time             `json:"created_at"`
	UpdatedAt             time.Time             `json:"updated_at"`
	Timeline              []ReportTimelineEntry `json:"timeline,omitempty"`
}

type Evidence struct {
	ID               string     `json:"evidence_id"`
	OriginalFilename string     `json:"original_filename"`
	MIMEType         string     `json:"mime_type"`
	FileSize         int64      `json:"file_size"`
	SHA256           string     `json:"sha256"`
	CaptureTimestamp *time.Time `json:"capture_timestamp,omitempty"`
	UploadTimestamp  time.Time  `json:"upload_timestamp"`
	Latitude         *float64   `json:"latitude,omitempty"`
	Longitude        *float64   `json:"longitude,omitempty"`
	LocationAccuracy *float64   `json:"location_accuracy,omitempty"`
	SourceType       string     `json:"source_type"`
	StorageReference string     `json:"storage_reference"`
}

type ReportTimelineEntry struct {
	ID         int64        `json:"id"`
	ReportID   string       `json:"report_id"`
	Status     ReportStatus `json:"status"`
	AuthorName string       `json:"author_name"`
	AuthorRole string       `json:"author_role"`
	Note       string       `json:"note"`
	CreatedAt  time.Time    `json:"created_at"`
}

// DTOs
type RegisterRequest struct {
	Name         string   `json:"name"`
	Email        string   `json:"email"`
	Password     string   `json:"password"`
	Role         UserRole `json:"role"`
	Phone        string   `json:"phone,omitempty"`
	Organization string   `json:"organization,omitempty"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type CreateReportRequest struct {
	AnimalType            string       `json:"animal_type"`
	AnimalName            string       `json:"animal_name,omitempty"`
	Urgency               UrgencyLevel `json:"urgency"`
	Condition             string       `json:"condition"`
	Description           string       `json:"description,omitempty"`
	Address               string       `json:"address"`
	Area                  string       `json:"area,omitempty"`
	City                  string       `json:"city,omitempty"`
	Latitude              *float64     `json:"latitude"`
	Longitude             *float64     `json:"longitude"`
	LocationAccuracy      *float64     `json:"location_accuracy,omitempty"`
	LocationTimestamp     *time.Time   `json:"location_timestamp,omitempty"`
	LocationSource        string       `json:"location_source,omitempty"`
	PhotoSource           string       `json:"photo_source,omitempty"`
	PhotoCapturedAt       *time.Time   `json:"photo_captured_at,omitempty"`
	PhotoLocationAccuracy *float64     `json:"photo_location_accuracy,omitempty"`
	PhotoLatitude         *float64     `json:"photo_latitude,omitempty"`
	PhotoLongitude        *float64     `json:"photo_longitude,omitempty"`
	ConfirmDuplicate      bool         `json:"confirm_duplicate,omitempty"`
	ReporterName          string       `json:"reporter_name"`
	ReporterPhone         string       `json:"reporter_phone"`
}

type AssignReportRequest struct {
	ResponderID   string `json:"responder_id,omitempty"`
	ResponderName string `json:"responder_name"`
	ETA           string `json:"eta"`
	Vehicle       string `json:"vehicle,omitempty"`
}

type UpdateStatusRequest struct {
	Status ReportStatus `json:"status"`
	Note   string       `json:"note"`
}

type AddNoteRequest struct {
	Note       string `json:"note"`
	AuthorName string `json:"author_name,omitempty"`
}

type ResponderVerificationRequest struct {
	Verified bool `json:"verified"`
}

type ResponderApplicationRequest struct {
	Phone        string `json:"phone,omitempty"`
	Organization string `json:"organization,omitempty"`
}

// PublicReport removes private details and reduces coordinate precision for
// public maps and case summaries.
func (r *Report) PublicReport() *Report {
	res := *r
	res.ReporterName = ""
	res.ReporterPhone = ""
	res.Address = ""
	res.Area = ""
	res.City = ""
	res.AssignedResponderID = nil
	res.AssignedResponderName = nil
	res.Latitude = roundCoordinate(res.Latitude)
	res.Longitude = roundCoordinate(res.Longitude)
	res.LocationAccuracy = nil
	res.LocationTimestamp = nil
	res.LocationSource = ""
	res.Evidence = nil
	for i := range res.Timeline {
		res.Timeline[i].AuthorName = ""
		res.Timeline[i].AuthorRole = ""
		res.Timeline[i].Note = ""
	}
	return &res
}

func roundCoordinate(value float64) float64 {
	return math.Round(value*1000) / 1000
}

type StatsResponse struct {
	TotalReports int64 `json:"total_reports"`
	ActiveCases  int64 `json:"active_cases"`
	Resolved     int64 `json:"resolved_cases"`
	Critical     int64 `json:"critical_cases"`
}
