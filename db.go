package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
	"pawsos/internal/auth"
	"pawsos/internal/models"
)

var (
	ErrNotFound = errors.New("record not found")
	ErrConflict = errors.New("record already exists")
)

type DB struct {
	*sql.DB
	driver string
}

type ReportFilter struct {
	Status  string
	Urgency string
	Search  string
	Limit   int
	Offset  int
}

// Connect opens a connection pool and verifies database connectivity
func Connect(driver, source string) (*DB, error) {
	sqlDriver := driver
	if driver == "sqlite" || driver == "sqlite3" {
		sqlDriver = "sqlite"
	} else if driver == "postgres" || driver == "postgresql" {
		sqlDriver = "postgres"
	}

	conn, err := sql.Open(sqlDriver, source)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure pool parameters for performance and stability
	if sqlDriver == "sqlite" {
		// SQLite works best with 1 writer to prevent file locking contention
		conn.SetMaxOpenConns(1)
	} else {
		conn.SetMaxOpenConns(25)
		conn.SetMaxIdleConns(5)
		conn.SetConnMaxLifetime(15 * time.Minute)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("database ping failed: %w", err)
	}

	db := &DB{DB: conn, driver: sqlDriver}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("database migration failed: %w", err)
	}

	return db, nil
}

func (db *DB) rebind(query string) string {
	if db.driver != "postgres" {
		return query
	}
	var b strings.Builder
	idx := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			b.WriteString("$")
			b.WriteString(strconv.Itoa(idx))
			idx++
		} else {
			b.WriteByte(query[i])
		}
	}
	return b.String()
}

func (db *DB) migrate() error {
	var schema string
	if db.driver == "postgres" {
		schema = `
		CREATE TABLE IF NOT EXISTS users (
			id VARCHAR(64) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			email VARCHAR(255) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			role VARCHAR(32) NOT NULL DEFAULT 'citizen',
			phone VARCHAR(64),
			organization VARCHAR(255),
			created_at TIMESTAMPTZ NOT NULL
		);

		CREATE TABLE IF NOT EXISTS reports (
			id VARCHAR(64) PRIMARY KEY,
			animal_type VARCHAR(64) NOT NULL,
			animal_name VARCHAR(128),
			urgency VARCHAR(32) NOT NULL DEFAULT 'urgent',
			condition TEXT NOT NULL,
			description TEXT,
			photo_url TEXT,
			status VARCHAR(32) NOT NULL DEFAULT 'reported',
			reporter_name VARCHAR(255) NOT NULL,
			reporter_phone VARCHAR(64) NOT NULL,
			address TEXT NOT NULL,
			area VARCHAR(128),
			city VARCHAR(128),
			latitude DOUBLE PRECISION NOT NULL,
			longitude DOUBLE PRECISION NOT NULL,
			assigned_responder_id VARCHAR(64),
			assigned_responder_name VARCHAR(255),
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		);

		CREATE TABLE IF NOT EXISTS report_timeline (
			id BIGSERIAL PRIMARY KEY,
			report_id VARCHAR(64) NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
			status VARCHAR(32) NOT NULL,
			author_name VARCHAR(255) NOT NULL,
			author_role VARCHAR(32) NOT NULL,
			note TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_reports_status ON reports(status);
		CREATE INDEX IF NOT EXISTS idx_reports_urgency ON reports(urgency);
		CREATE INDEX IF NOT EXISTS idx_reports_created_at ON reports(created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_reports_assigned ON reports(assigned_responder_id);
		CREATE INDEX IF NOT EXISTS idx_timeline_report_id ON report_timeline(report_id);
		`
	} else {
		schema = `
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'citizen',
			phone TEXT,
			organization TEXT,
			created_at DATETIME NOT NULL
		);

		CREATE TABLE IF NOT EXISTS reports (
			id TEXT PRIMARY KEY,
			animal_type TEXT NOT NULL,
			animal_name TEXT,
			urgency TEXT NOT NULL DEFAULT 'urgent',
			condition TEXT NOT NULL,
			description TEXT,
			photo_url TEXT,
			status TEXT NOT NULL DEFAULT 'reported',
			reporter_name TEXT NOT NULL,
			reporter_phone TEXT NOT NULL,
			address TEXT NOT NULL,
			area TEXT,
			city TEXT,
			latitude REAL NOT NULL,
			longitude REAL NOT NULL,
			assigned_responder_id TEXT,
			assigned_responder_name TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);

		CREATE TABLE IF NOT EXISTS report_timeline (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			report_id TEXT NOT NULL,
			status TEXT NOT NULL,
			author_name TEXT NOT NULL,
			author_role TEXT NOT NULL,
			note TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			FOREIGN KEY (report_id) REFERENCES reports(id) ON DELETE CASCADE
		);

		CREATE INDEX IF NOT EXISTS idx_reports_status ON reports(status);
		CREATE INDEX IF NOT EXISTS idx_reports_urgency ON reports(urgency);
		CREATE INDEX IF NOT EXISTS idx_reports_created_at ON reports(created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_reports_assigned ON reports(assigned_responder_id);
		CREATE INDEX IF NOT EXISTS idx_timeline_report_id ON report_timeline(report_id);
		`
	}

	_, err := db.Exec(schema)
	return err
}

// ----------------- USERS -----------------

func (db *DB) CreateUser(ctx context.Context, u *models.User) error {
	query := db.rebind(`
		INSERT INTO users (id, name, email, password_hash, role, phone, organization, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)

	_, err := db.ExecContext(ctx, query,
		u.ID, u.Name, u.Email, u.PasswordHash, string(u.Role), u.Phone, u.Organization, u.CreatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate") {
			return ErrConflict
		}
		return err
	}
	return nil
}

func (db *DB) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	query := db.rebind(`
		SELECT id, name, email, password_hash, role, phone, organization, created_at
		FROM users WHERE LOWER(email) = LOWER(?) LIMIT 1
	`)

	var u models.User
	var phone, org sql.NullString
	var roleStr string

	err := db.QueryRowContext(ctx, query, email).Scan(
		&u.ID, &u.Name, &u.Email, &u.PasswordHash, &roleStr, &phone, &org, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	u.Role = models.UserRole(roleStr)
	if phone.Valid {
		u.Phone = phone.String
	}
	if org.Valid {
		u.Organization = org.String
	}
	return &u, nil
}

func (db *DB) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	query := db.rebind(`
		SELECT id, name, email, password_hash, role, phone, organization, created_at
		FROM users WHERE id = ? LIMIT 1
	`)

	var u models.User
	var phone, org sql.NullString
	var roleStr string

	err := db.QueryRowContext(ctx, query, id).Scan(
		&u.ID, &u.Name, &u.Email, &u.PasswordHash, &roleStr, &phone, &org, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	u.Role = models.UserRole(roleStr)
	if phone.Valid {
		u.Phone = phone.String
	}
	if org.Valid {
		u.Organization = org.String
	}
	return &u, nil
}

func (db *DB) CountUsers(ctx context.Context) (int64, error) {
	var count int64
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

// ----------------- REPORTS -----------------

func (db *DB) CreateReport(ctx context.Context, r *models.Report) error {
	query := db.rebind(`
		INSERT INTO reports (
			id, animal_type, animal_name, urgency, condition, description, photo_url,
			status, reporter_name, reporter_phone, address, area, city, latitude, longitude,
			assigned_responder_id, assigned_responder_name, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)

	_, err := db.ExecContext(ctx, query,
		r.ID, r.AnimalType, r.AnimalName, string(r.Urgency), r.Condition, r.Description, r.PhotoURL,
		string(r.Status), r.ReporterName, r.ReporterPhone, r.Address, r.Area, r.City, r.Latitude, r.Longitude,
		r.AssignedResponderID, r.AssignedResponderName, r.CreatedAt, r.UpdatedAt,
	)
	if err != nil {
		return err
	}

	// Add initial timeline entry
	return db.AddTimelineEntry(ctx, &models.ReportTimelineEntry{
		ReportID:   r.ID,
		Status:     r.Status,
		AuthorName: r.ReporterName,
		AuthorRole: "citizen",
		Note:       "Report registered with PawSOS dispatch.",
		CreatedAt:  r.CreatedAt,
	})
}

func (db *DB) GetReportByID(ctx context.Context, id string) (*models.Report, error) {
	query := db.rebind(`
		SELECT id, animal_type, animal_name, urgency, condition, description, photo_url,
		       status, reporter_name, reporter_phone, address, area, city, latitude, longitude,
		       assigned_responder_id, assigned_responder_name, created_at, updated_at
		FROM reports WHERE id = ? LIMIT 1
	`)

	var r models.Report
	var animalName, desc, photoURL, area, city sql.NullString
	var assignedID, assignedName sql.NullString
	var urgencyStr, statusStr string

	err := db.QueryRowContext(ctx, query, id).Scan(
		&r.ID, &r.AnimalType, &animalName, &urgencyStr, &r.Condition, &desc, &photoURL,
		&statusStr, &r.ReporterName, &r.ReporterPhone, &r.Address, &area, &city, &r.Latitude, &r.Longitude,
		&assignedID, &assignedName, &r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	r.Urgency = models.UrgencyLevel(urgencyStr)
	r.Status = models.ReportStatus(statusStr)
	if animalName.Valid {
		r.AnimalName = animalName.String
	}
	if desc.Valid {
		r.Description = desc.String
	}
	if photoURL.Valid {
		r.PhotoURL = photoURL.String
	}
	if area.Valid {
		r.Area = area.String
	}
	if city.Valid {
		r.City = city.String
	}
	if assignedID.Valid {
		r.AssignedResponderID = &assignedID.String
	}
	if assignedName.Valid {
		r.AssignedResponderName = &assignedName.String
	}

	// Fetch timeline
	timeline, err := db.GetTimelineForReport(ctx, r.ID)
	if err == nil {
		r.Timeline = timeline
	}

	return &r, nil
}

func (db *DB) GetReports(ctx context.Context, filter ReportFilter) ([]*models.Report, int64, error) {
	var whereClauses []string
	var args []interface{}

	if filter.Status != "" && filter.Status != "all" {
		whereClauses = append(whereClauses, "status = ?")
		args = append(args, filter.Status)
	}

	if filter.Urgency != "" && filter.Urgency != "all" {
		whereClauses = append(whereClauses, "urgency = ?")
		args = append(args, filter.Urgency)
	}

	if filter.Search != "" {
		whereClauses = append(whereClauses, "(LOWER(id) LIKE ? OR LOWER(animal_type) LIKE ? OR LOWER(address) LIKE ? OR LOWER(condition) LIKE ?)")
		pattern := "%" + strings.ToLower(filter.Search) + "%"
		args = append(args, pattern, pattern, pattern, pattern)
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Count total matching
	countQuery := db.rebind("SELECT COUNT(*) FROM reports" + whereSQL)
	var total int64
	if err := db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Limit and Offset
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	selectSQL := fmt.Sprintf(`
		SELECT id, animal_type, animal_name, urgency, condition, description, photo_url,
		       status, reporter_name, reporter_phone, address, area, city, latitude, longitude,
		       assigned_responder_id, assigned_responder_name, created_at, updated_at
		FROM reports %s
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, whereSQL)

	fetchArgs := append(args, limit, offset)
	query := db.rebind(selectSQL)

	rows, err := db.QueryContext(ctx, query, fetchArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var reports []*models.Report
	for rows.Next() {
		var r models.Report
		var animalName, desc, photoURL, area, city sql.NullString
		var assignedID, assignedName sql.NullString
		var urgencyStr, statusStr string

		if err := rows.Scan(
			&r.ID, &r.AnimalType, &animalName, &urgencyStr, &r.Condition, &desc, &photoURL,
			&statusStr, &r.ReporterName, &r.ReporterPhone, &r.Address, &area, &city, &r.Latitude, &r.Longitude,
			&assignedID, &assignedName, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, 0, err
		}

		r.Urgency = models.UrgencyLevel(urgencyStr)
		r.Status = models.ReportStatus(statusStr)
		if animalName.Valid {
			r.AnimalName = animalName.String
		}
		if desc.Valid {
			r.Description = desc.String
		}
		if photoURL.Valid {
			r.PhotoURL = photoURL.String
		}
		if area.Valid {
			r.Area = area.String
		}
		if city.Valid {
			r.City = city.String
		}
		if assignedID.Valid {
			r.AssignedResponderID = &assignedID.String
		}
		if assignedName.Valid {
			r.AssignedResponderName = &assignedName.String
		}

		reports = append(reports, &r)
	}

	return reports, total, rows.Err()
}

func (db *DB) UpdateReportStatus(ctx context.Context, id string, status models.ReportStatus, authorName, authorRole, note string) error {
	now := time.Now()
	query := db.rebind("UPDATE reports SET status = ?, updated_at = ? WHERE id = ?")
	res, err := db.ExecContext(ctx, query, string(status), now, id)
	if err != nil {
		return err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return ErrNotFound
	}

	if note == "" {
		note = fmt.Sprintf("Status updated to %s", status)
	}

	return db.AddTimelineEntry(ctx, &models.ReportTimelineEntry{
		ReportID:   id,
		Status:     status,
		AuthorName: authorName,
		AuthorRole: authorRole,
		Note:       note,
		CreatedAt:  now,
	})
}

func (db *DB) AssignReport(ctx context.Context, id, responderID, responderName, eta, authorRole string) error {
	now := time.Now()
	query := db.rebind("UPDATE reports SET assigned_responder_id = ?, assigned_responder_name = ?, status = ?, updated_at = ? WHERE id = ?")
	res, err := db.ExecContext(ctx, query, responderID, responderName, string(models.StatusAssigned), now, id)
	if err != nil {
		return err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return ErrNotFound
	}

	note := fmt.Sprintf("Assigned to responder %s. Estimated arrival: %s", responderName, eta)
	return db.AddTimelineEntry(ctx, &models.ReportTimelineEntry{
		ReportID:   id,
		Status:     models.StatusAssigned,
		AuthorName: responderName,
		AuthorRole: authorRole,
		Note:       note,
		CreatedAt:  now,
	})
}

func (db *DB) AddTimelineEntry(ctx context.Context, entry *models.ReportTimelineEntry) error {
	query := db.rebind(`
		INSERT INTO report_timeline (report_id, status, author_name, author_role, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	_, err := db.ExecContext(ctx, query,
		entry.ReportID, string(entry.Status), entry.AuthorName, entry.AuthorRole, entry.Note, entry.CreatedAt,
	)
	return err
}

func (db *DB) GetTimelineForReport(ctx context.Context, reportID string) ([]models.ReportTimelineEntry, error) {
	query := db.rebind(`
		SELECT id, report_id, status, author_name, author_role, note, created_at
		FROM report_timeline WHERE report_id = ? ORDER BY created_at ASC
	`)

	rows, err := db.QueryContext(ctx, query, reportID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []models.ReportTimelineEntry
	for rows.Next() {
		var e models.ReportTimelineEntry
		var statusStr string
		if err := rows.Scan(&e.ID, &e.ReportID, &statusStr, &e.AuthorName, &e.AuthorRole, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Status = models.ReportStatus(statusStr)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (db *DB) GetStats(ctx context.Context) (*models.StatsResponse, error) {
	stats := &models.StatsResponse{}

	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reports").Scan(&stats.TotalReports)
	_ = db.QueryRowContext(ctx, db.rebind("SELECT COUNT(*) FROM reports WHERE status IN ('reported', 'assigned', 'in_progress')")).Scan(&stats.ActiveCases)
	_ = db.QueryRowContext(ctx, db.rebind("SELECT COUNT(*) FROM reports WHERE status = 'resolved'")).Scan(&stats.Resolved)
	_ = db.QueryRowContext(ctx, db.rebind("SELECT COUNT(*) FROM reports WHERE urgency = 'critical' AND status != 'resolved'")).Scan(&stats.Critical)

	return stats, nil
}

func (db *DB) GetAllReportsForExport(ctx context.Context) ([]*models.Report, error) {
	reports, _, err := db.GetReports(ctx, ReportFilter{Limit: 10000})
	return reports, err
}

func (db *DB) SeedDefaultData(ctx context.Context) error {
	// Seed Admin and Responder users if table is empty
	userCount, err := db.CountUsers(ctx)
	if err == nil && userCount == 0 {
		adminPass, _ := auth.HashPassword("adminpassword123")
		admin := &models.User{
			ID:           uuid.New().String(),
			Name:         "Lead Dispatcher Admin",
			Email:        "admin@pawsos.org",
			PasswordHash: adminPass,
			Role:         models.RoleAdmin,
			Phone:        "+1 555-0199",
			Organization: "PawSOS Central Command",
			CreatedAt:    time.Now().Add(-72 * time.Hour),
		}
		_ = db.CreateUser(ctx, admin)

		responderPass, _ := auth.HashPassword("responder123")
		responder := &models.User{
			ID:           uuid.New().String(),
			Name:         "Unit 4 - Sarah Jenkins",
			Email:        "responder@pawsos.org",
			PasswordHash: responderPass,
			Role:         models.RoleResponder,
			Phone:        "+1 555-0248",
			Organization: "City Animal Rescue Taskforce",
			CreatedAt:    time.Now().Add(-48 * time.Hour),
		}
		_ = db.CreateUser(ctx, responder)
	}

	// Seed realistic demo reports if reports table is empty
	var reportCount int64
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reports").Scan(&reportCount)
	if reportCount == 0 {
		now := time.Now()
		cases := []struct {
			id, animal, urgency, condition, desc, address, area, city, status string
			lat, lng                                                          float64
			hoursAgo                                                          int
		}{
			{
				id:        "PAW-4812",
				animal:    "dog",
				urgency:   "critical",
				condition: "Fractured right leg, unable to walk, bleeding slightly near shoulder",
				desc:      "Found on median strip after vehicle strike. Responsive but in acute shock.",
				address:   "342 Elm Street, near intersection with 4th Ave",
				area:      "Downtown",
				city:      "Metropolis",
				status:    "in_progress",
				lat:       37.7749,
				lng:       -122.4194,
				hoursAgo:  3,
			},
			{
				id:        "PAW-4813",
				animal:    "cat",
				urgency:   "urgent",
				condition: "Trapped in deep stormwater storm drain, crying for several hours",
				desc:      "Kitten approximately 3-4 months old. Visible at bottom of grate, about 6 feet down.",
				address:   "89 Pine Valley Road, curb adjacent to school park",
				area:      "Westside",
				city:      "Metropolis",
				status:    "assigned",
				lat:       37.7833,
				lng:       -122.4167,
				hoursAgo:  2,
			},
			{
				id:        "PAW-4814",
				animal:    "dog",
				urgency:   "moderate",
				condition: "Severe dehydration, severe mange and skin infection, limping",
				desc:      "Wandering around supermarket loading dock looking for food scraps.",
				address:   "1200 Industrial Parkway, dock #4",
				area:      "North Industrial",
				city:      "Metropolis",
				status:    "reported",
				lat:       37.7650,
				lng:       -122.4300,
				hoursAgo:  1,
			},
			{
				id:        "PAW-4810",
				animal:    "bird",
				urgency:   "moderate",
				condition: "Damaged left wing, grounded on pedestrian footpath",
				desc:      "Red-tailed hawk unable to take off. Secured in safe perimeter by bystander.",
				address:   "Civic Plaza Park, south fountain",
				area:      "Civic Center",
				city:      "Metropolis",
				status:    "resolved",
				lat:       37.7790,
				lng:       -122.4180,
				hoursAgo:  18,
			},
		}

		for _, c := range cases {
			t := now.Add(-time.Duration(c.hoursAgo) * time.Hour)
			rep := &models.Report{
				ID:            c.id,
				AnimalType:    c.animal,
				Urgency:       models.UrgencyLevel(c.urgency),
				Condition:     c.condition,
				Description:   c.desc,
				Status:        models.ReportStatus(c.status),
				ReporterName:  "Citizen Witness",
				ReporterPhone: "+1 555-0182",
				Address:       c.address,
				Area:          c.area,
				City:          c.city,
				Latitude:      c.lat,
				Longitude:     c.lng,
				CreatedAt:     t,
				UpdatedAt:     t,
			}
			if c.status == "assigned" || c.status == "in_progress" {
				respID := "unit-4"
				respName := "Unit 4 - Sarah Jenkins"
				rep.AssignedResponderID = &respID
				rep.AssignedResponderName = &respName
			}
			_ = db.CreateReport(ctx, rep)
		}
	}

	return nil
}
