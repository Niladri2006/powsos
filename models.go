package models

import (
	"strings"
	"time"
)

type UserRole string

const (
	RoleCitizen   UserRole = "citizen"
	RoleResponder UserRole = "responder"
	RoleAdmin     UserRole = "admin"
)

type User struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         UserRole  `json:"role"`
	Phone        string    `json:"phone,omitempty"`
	Organization string    `json:"organization,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type ReportStatus string

const (
	StatusReported   ReportStatus = "reported"
	StatusAssigned   ReportStatus = "assigned"
	StatusInProgress ReportStatus = "in_progress"
	StatusResolved   ReportStatus = "resolved"
	StatusClosed     ReportStatus = "closed"
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
	ReporterName          string                `json:"reporter_name"`
	ReporterPhone         string                `json:"reporter_phone"`
	Address               string                `json:"address"`
	Area                  string                `json:"area,omitempty"`
	City                  string                `json:"city,omitempty"`
	Latitude              float64               `json:"latitude"`
	Longitude             float64               `json:"longitude"`
	AssignedResponderID   *string               `json:"assigned_responder_id,omitempty"`
	AssignedResponderName *string               `json:"assigned_responder_name,omitempty"`
	CreatedAt             time.Time             `json:"created_at"`
	UpdatedAt             time.Time             `json:"updated_at"`
	Timeline              []ReportTimelineEntry `json:"timeline,omitempty"`
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

// MaskedReport returns a public representation with privacy protections
func (r *Report) MaskedReport(isAuthorizedResponder bool) *Report {
	res := *r
	if !isAuthorizedResponder && res.ReporterPhone != "" {
		phone := strings.TrimSpace(res.ReporterPhone)
		if len(phone) >= 7 {
			res.ReporterPhone = phone[:len(phone)-4] + "••••"
		} else {
			res.ReporterPhone = "••••••"
		}
	}
	return &res
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
	AnimalType    string       `json:"animal_type"`
	AnimalName    string       `json:"animal_name,omitempty"`
	Urgency       UrgencyLevel `json:"urgency"`
	Condition     string       `json:"condition"`
	Description   string       `json:"description,omitempty"`
	Address       string       `json:"address"`
	Area          string       `json:"area,omitempty"`
	City          string       `json:"city,omitempty"`
	Latitude      float64      `json:"latitude"`
	Longitude     float64      `json:"longitude"`
	ReporterName  string       `json:"reporter_name"`
	ReporterPhone string       `json:"reporter_phone"`
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

type StatsResponse struct {
	TotalReports int64 `json:"total_reports"`
	ActiveCases  int64 `json:"active_cases"`
	Resolved     int64 `json:"resolved_cases"`
	Critical     int64 `json:"critical_cases"`
}
