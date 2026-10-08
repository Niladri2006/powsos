package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
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
			location_accuracy DOUBLE PRECISION,
			location_timestamp TIMESTAMPTZ,
			location_source VARCHAR(32),
			eta VARCHAR(128),
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

		CREATE TABLE IF NOT EXISTS report_evidence (
			id VARCHAR(64) PRIMARY KEY,
			report_id VARCHAR(64) NOT NULL UNIQUE REFERENCES reports(id) ON DELETE CASCADE,
			original_filename TEXT NOT NULL,
			mime_type VARCHAR(128) NOT NULL,
			file_size BIGINT NOT NULL,
			sha256 VARCHAR(64) NOT NULL,
			capture_timestamp TIMESTAMPTZ,
			upload_timestamp TIMESTAMPTZ NOT NULL,
			latitude DOUBLE PRECISION,
			longitude DOUBLE PRECISION,
			location_accuracy DOUBLE PRECISION,
			source_type VARCHAR(32) NOT NULL,
			storage_reference TEXT NOT NULL
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
			location_accuracy REAL,
			location_timestamp DATETIME,
			location_source TEXT,
			eta TEXT,
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

		CREATE TABLE IF NOT EXISTS report_evidence (
			id TEXT PRIMARY KEY,
			report_id TEXT NOT NULL UNIQUE,
			original_filename TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			capture_timestamp DATETIME,
			upload_timestamp DATETIME NOT NULL,
			latitude REAL,
			longitude REAL,
			location_accuracy REAL,
			source_type TEXT NOT NULL,
			storage_reference TEXT NOT NULL,
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
	if err != nil {
		return err
	}
	return db.ensureReportColumns()
}

func (db *DB) ensureReportColumns() error {
	if db.driver == "postgres" {
		for _, column := range []struct{ name, kind string }{
			{"requested_role", "VARCHAR(32) NOT NULL DEFAULT 'citizen'"},
			{"verified", "BOOLEAN NOT NULL DEFAULT FALSE"},
		} {
			if _, err := db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS ` + column.name + ` ` + column.kind); err != nil {
				return err
			}
		}
		for _, column := range []struct{ name, kind string }{
			{"location_accuracy", "DOUBLE PRECISION"},
			{"location_timestamp", "TIMESTAMPTZ"},
			{"location_source", "VARCHAR(32)"},
			{"eta", "VARCHAR(128)"},
		} {
			if _, err := db.Exec(`ALTER TABLE reports ADD COLUMN IF NOT EXISTS ` + column.name + ` ` + column.kind); err != nil {
				return err
			}
		}
		return nil
	}

	for _, column := range []struct{ table, name, kind string }{
		{"users", "requested_role", "TEXT NOT NULL DEFAULT 'citizen'"},
		{"users", "verified", "INTEGER NOT NULL DEFAULT 0"},
		{"reports", "location_accuracy", "REAL"},
		{"reports", "location_timestamp", "DATETIME"},
		{"reports", "location_source", "TEXT"},
		{"reports", "eta", "TEXT"},
	} {
		rows, err := db.Query("PRAGMA table_info(" + column.table + ")")
		if err != nil {
			return err
		}
		found := false
		for rows.Next() {
			var cid int
			var name, dataType string
			var notNull, primaryKey int
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				return err
			}
			if name == column.name {
				found = true
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if !found {
			if _, err := db.Exec("ALTER TABLE " + column.table + " ADD COLUMN " + column.name + " " + column.kind); err != nil {
				return err
			}
		}
	}
	return nil
}

// ----------------- USERS -----------------

func (db *DB) CreateUser(ctx context.Context, u *models.User) error {
	query := db.rebind(`
		INSERT INTO users (id, name, email, password_hash, role, requested_role, verified, phone, organization, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)

	_, err := db.ExecContext(ctx, query,
		u.ID, u.Name, u.Email, u.PasswordHash, string(u.Role), string(u.RequestedRole), u.Verified, u.Phone, u.Organization, u.CreatedAt,
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
		SELECT id, name, email, password_hash, role, requested_role, verified, phone, organization, created_at
		FROM users WHERE LOWER(email) = LOWER(?) LIMIT 1
	`)

	var u models.User
	var phone, org sql.NullString
	var roleStr, requestedRole string

	err := db.QueryRowContext(ctx, query, email).Scan(
		&u.ID, &u.Name, &u.Email, &u.PasswordHash, &roleStr, &requestedRole, &u.Verified, &phone, &org, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	u.Role = models.UserRole(roleStr)
	u.RequestedRole = models.UserRole(requestedRole)
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
		SELECT id, name, email, password_hash, role, requested_role, verified, phone, organization, created_at
		FROM users WHERE id = ? LIMIT 1
	`)

	var u models.User
	var phone, org sql.NullString
	var roleStr, requestedRole string

	err := db.QueryRowContext(ctx, query, id).Scan(
		&u.ID, &u.Name, &u.Email, &u.PasswordHash, &roleStr, &requestedRole, &u.Verified, &phone, &org, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	u.Role = models.UserRole(roleStr)
	u.RequestedRole = models.UserRole(requestedRole)
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

func (db *DB) RemoveLegacyDemoData(ctx context.Context) error {
	legacyUserPassword, err := auth.HashPassword(uuid.NewString())
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	userQuery := db.rebind(`
		UPDATE users
		SET name = ?, password_hash = ?, role = ?, requested_role = ?, verified = ?, phone = NULL, organization = NULL
		WHERE LOWER(email) IN (?, ?)
	`)
	if _, err := tx.ExecContext(ctx, userQuery, "Disabled legacy account", legacyUserPassword,
		string(models.RoleCitizen), string(models.RoleCitizen), false,
		"admin@pawsos.org", "responder@pawsos.org"); err != nil {
		return err
	}

	ids := []string{"PAW-4810", "PAW-4812", "PAW-4813", "PAW-4814"}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	for _, table := range []string{"report_evidence", "report_timeline", "reports"} {
		query := db.rebind("DELETE FROM " + table + " WHERE report_id IN (" + marks + ")")
		if table == "reports" {
			query = db.rebind("DELETE FROM reports WHERE id IN (" + marks + ")")
		}
		args := make([]interface{}, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ----------------- REPORTS -----------------

func (db *DB) CreateReport(ctx context.Context, r *models.Report) error {
	query := db.rebind(`
		INSERT INTO reports (
			id, animal_type, animal_name, urgency, condition, description, photo_url,
			status, reporter_name, reporter_phone, address, area, city, latitude, longitude,
			assigned_responder_id, assigned_responder_name, location_accuracy, location_timestamp,
			location_source, eta, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, query,
		r.ID, r.AnimalType, r.AnimalName, string(r.Urgency), r.Condition, r.Description, r.PhotoURL,
		string(r.Status), r.ReporterName, r.ReporterPhone, r.Address, r.Area, r.City, r.Latitude, r.Longitude,
		r.AssignedResponderID, r.AssignedResponderName, r.LocationAccuracy, r.LocationTimestamp, r.LocationSource, r.ETA,
		r.CreatedAt, r.UpdatedAt,
	)
	if err != nil {
		return err
	}

	entry := &models.ReportTimelineEntry{
		ReportID:   r.ID,
		Status:     r.Status,
		AuthorName: "Reporter",
		AuthorRole: "citizen",
		Note:       "Report submitted.",
		CreatedAt:  r.CreatedAt,
	}
	timelineQuery := db.rebind(`
		INSERT INTO report_timeline (report_id, status, author_name, author_role, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if _, err := tx.ExecContext(ctx, timelineQuery, entry.ReportID, string(entry.Status), entry.AuthorName, entry.AuthorRole, entry.Note, entry.CreatedAt); err != nil {
		return err
	}
	if r.Evidence != nil {
		if err := insertEvidence(ctx, tx, db.driver, r.ID, r.Evidence); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) GetReportByID(ctx context.Context, id string) (*models.Report, error) {
	query := db.rebind(`
		SELECT id, animal_type, animal_name, urgency, condition, description, photo_url,
		       status, reporter_name, reporter_phone, address, area, city, latitude, longitude,
		       assigned_responder_id, assigned_responder_name, location_accuracy, location_timestamp,
		       location_source, eta, created_at, updated_at
		FROM reports WHERE id = ? LIMIT 1
	`)

	var r models.Report
	var animalName, desc, photoURL, area, city sql.NullString
	var assignedID, assignedName sql.NullString
	var urgencyStr, statusStr string
	var locationAccuracy sql.NullFloat64
	var locationTimestamp sql.NullTime
	var locationSource, eta sql.NullString

	err := db.QueryRowContext(ctx, query, id).Scan(
		&r.ID, &r.AnimalType, &animalName, &urgencyStr, &r.Condition, &desc, &photoURL,
		&statusStr, &r.ReporterName, &r.ReporterPhone, &r.Address, &area, &city, &r.Latitude, &r.Longitude,
		&assignedID, &assignedName, &locationAccuracy, &locationTimestamp, &locationSource, &eta, &r.CreatedAt, &r.UpdatedAt,
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
	if locationAccuracy.Valid {
		r.LocationAccuracy = &locationAccuracy.Float64
	}
	if locationTimestamp.Valid {
		r.LocationTimestamp = &locationTimestamp.Time
	}
	if locationSource.Valid {
		r.LocationSource = locationSource.String
	}
	if eta.Valid {
		r.ETA = eta.String
	}

	timeline, err := db.GetTimelineForReport(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	r.Timeline = timeline
	r.Evidence, err = db.GetEvidence(ctx, r.ID)
	if err != nil {
		return nil, err
	}

	return &r, nil
}

func (db *DB) GetReports(ctx context.Context, filter ReportFilter) ([]*models.Report, int64, error) {
	var whereClauses []string
	var args []interface{}

	if filter.Status == "active" {
		whereClauses = append(whereClauses, "status IN ('reported', 'assigned', 'in_progress', 'on_the_way', 'arrived', 'animal_secured', 'transporting', 'at_vet')")
	} else if filter.Status != "" && filter.Status != "all" {
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
	if limit <= 0 {
		limit = 50
	}
	if limit > 10000 {
		limit = 10000
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	selectSQL := fmt.Sprintf(`
		SELECT id, animal_type, animal_name, urgency, condition, description, photo_url,
		       status, reporter_name, reporter_phone, address, area, city, latitude, longitude,
		       assigned_responder_id, assigned_responder_name, location_accuracy, location_timestamp,
		       location_source, eta, created_at, updated_at
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
		var locationAccuracy sql.NullFloat64
		var locationTimestamp sql.NullTime
		var locationSource, eta sql.NullString

		if err := rows.Scan(
			&r.ID, &r.AnimalType, &animalName, &urgencyStr, &r.Condition, &desc, &photoURL,
			&statusStr, &r.ReporterName, &r.ReporterPhone, &r.Address, &area, &city, &r.Latitude, &r.Longitude,
			&assignedID, &assignedName, &locationAccuracy, &locationTimestamp, &locationSource, &eta, &r.CreatedAt, &r.UpdatedAt,
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
		if locationAccuracy.Valid {
			r.LocationAccuracy = &locationAccuracy.Float64
		}
		if locationTimestamp.Valid {
			r.LocationTimestamp = &locationTimestamp.Time
		}
		if locationSource.Valid {
			r.LocationSource = locationSource.String
		}
		if eta.Valid {
			r.ETA = eta.String
		}

		reports = append(reports, &r)
	}

	return reports, total, rows.Err()
}

func (db *DB) UpdateReportStatus(ctx context.Context, id string, status models.ReportStatus, authorName, authorRole, note string) error {
	now := time.Now()
	query := db.rebind("UPDATE reports SET status = ?, updated_at = ? WHERE id = ?")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, query, string(status), now, id)
	if err != nil {
		return err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}

	if note == "" {
		note = fmt.Sprintf("Status updated to %s", status)
	}

	if err := addTimelineEntry(ctx, tx, db.driver, &models.ReportTimelineEntry{
		ReportID:   id,
		Status:     status,
		AuthorName: authorName,
		AuthorRole: authorRole,
		Note:       note,
		CreatedAt:  now,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) AssignReport(ctx context.Context, id, responderID, responderName, eta, authorRole string) error {
	now := time.Now()
	query := db.rebind("UPDATE reports SET assigned_responder_id = ?, assigned_responder_name = ?, status = ?, eta = ?, updated_at = ? WHERE id = ? AND (assigned_responder_id IS NULL OR assigned_responder_id = ?)")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, query, responderID, responderName, string(models.StatusAssigned), eta, now, id, responderID)
	if err != nil {
		return err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrConflict
	}

	note := fmt.Sprintf("Case accepted by %s.", responderName)
	if eta != "" {
		note = fmt.Sprintf("%s ETA reported: %s", note, eta)
	}
	if err := addTimelineEntry(ctx, tx, db.driver, &models.ReportTimelineEntry{
		ReportID:   id,
		Status:     models.StatusAssigned,
		AuthorName: responderName,
		AuthorRole: authorRole,
		Note:       note,
		CreatedAt:  now,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) AddTimelineEntry(ctx context.Context, entry *models.ReportTimelineEntry) error {
	return addTimelineEntry(ctx, db, db.driver, entry)
}

type timelineExecutor interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

func addTimelineEntry(ctx context.Context, executor timelineExecutor, driver string, entry *models.ReportTimelineEntry) error {
	query := `
		INSERT INTO report_timeline (report_id, status, author_name, author_role, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	if driver == "postgres" {
		var b strings.Builder
		index := 1
		for _, char := range query {
			if char == '?' {
				fmt.Fprintf(&b, "$%d", index)
				index++
			} else {
				b.WriteRune(char)
			}
		}
		query = b.String()
	}
	_, err := executor.ExecContext(ctx, query,
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

func insertEvidence(ctx context.Context, tx *sql.Tx, driver, reportID string, evidence *models.Evidence) error {
	query := `
		INSERT INTO report_evidence (
			id, report_id, original_filename, mime_type, file_size, sha256, capture_timestamp,
			upload_timestamp, latitude, longitude, location_accuracy, source_type, storage_reference
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	if driver == "postgres" {
		var b strings.Builder
		idx := 1
		for _, ch := range query {
			if ch == '?' {
				fmt.Fprintf(&b, "$%d", idx)
				idx++
			} else {
				b.WriteRune(ch)
			}
		}
		query = b.String()
	}
	_, err := tx.ExecContext(ctx, query,
		evidence.ID, reportID, evidence.OriginalFilename, evidence.MIMEType, evidence.FileSize,
		evidence.SHA256, evidence.CaptureTimestamp, evidence.UploadTimestamp, evidence.Latitude,
		evidence.Longitude, evidence.LocationAccuracy, evidence.SourceType, evidence.StorageReference,
	)
	return err
}

func (db *DB) GetEvidence(ctx context.Context, reportID string) (*models.Evidence, error) {
	query := db.rebind(`
		SELECT id, original_filename, mime_type, file_size, sha256, capture_timestamp,
		       upload_timestamp, latitude, longitude, location_accuracy, source_type, storage_reference
		FROM report_evidence WHERE report_id = ? LIMIT 1
	`)
	var evidence models.Evidence
	var capturedAt sql.NullTime
	var latitude, longitude, accuracy sql.NullFloat64
	err := db.QueryRowContext(ctx, query, reportID).Scan(
		&evidence.ID, &evidence.OriginalFilename, &evidence.MIMEType, &evidence.FileSize,
		&evidence.SHA256, &capturedAt, &evidence.UploadTimestamp, &latitude, &longitude,
		&accuracy, &evidence.SourceType, &evidence.StorageReference,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if capturedAt.Valid {
		evidence.CaptureTimestamp = &capturedAt.Time
	}
	if latitude.Valid {
		evidence.Latitude = &latitude.Float64
	}
	if longitude.Valid {
		evidence.Longitude = &longitude.Float64
	}
	if accuracy.Valid {
		evidence.LocationAccuracy = &accuracy.Float64
	}
	return &evidence, nil
}

func (db *DB) FindDuplicateReport(ctx context.Context, animalType string, latitude, longitude float64) (*models.Report, error) {
	reports, _, err := db.GetReports(ctx, ReportFilter{Status: "active", Limit: 100})
	if err != nil {
		return nil, err
	}
	for _, report := range reports {
		if !strings.EqualFold(report.AnimalType, animalType) || time.Since(report.CreatedAt) > 6*time.Hour {
			continue
		}
		dLat := (report.Latitude - latitude) * math.Pi / 180
		dLng := (report.Longitude - longitude) * math.Pi / 180
		lat1 := latitude * math.Pi / 180
		lat2 := report.Latitude * math.Pi / 180
		a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
		distance := 6371000 * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
		if distance <= 150 {
			return report, nil
		}
	}
	return nil, nil
}

func (db *DB) GetPendingResponders(ctx context.Context) ([]models.User, error) {
	query := db.rebind(`
		SELECT id, name, email, role, requested_role, verified, phone, organization, created_at
		FROM users WHERE requested_role = ? AND role = ? ORDER BY created_at ASC
	`)
	rows, err := db.QueryContext(ctx, query, string(models.RoleResponder), string(models.RoleCitizen))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []models.User
	for rows.Next() {
		var user models.User
		var role, requested string
		var phone, organization sql.NullString
		if err := rows.Scan(&user.ID, &user.Name, &user.Email, &role, &requested, &user.Verified, &phone, &organization, &user.CreatedAt); err != nil {
			return nil, err
		}
		user.Role, user.RequestedRole = models.UserRole(role), models.UserRole(requested)
		if phone.Valid {
			user.Phone = phone.String
		}
		if organization.Valid {
			user.Organization = organization.String
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (db *DB) VerifyResponder(ctx context.Context, id string, verified bool) error {
	role := models.RoleCitizen
	requestedRole := models.RoleCitizen
	if verified {
		role = models.RoleResponder
		requestedRole = models.RoleResponder
	}
	query := db.rebind(`UPDATE users SET role = ?, verified = ?, requested_role = ? WHERE id = ? AND requested_role = ?`)
	result, err := db.ExecContext(ctx, query, string(role), verified, string(requestedRole), id, string(models.RoleResponder))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) RequestResponderReview(ctx context.Context, userID, phone, organization string) error {
	query := db.rebind(`UPDATE users SET requested_role = ?, phone = COALESCE(NULLIF(?, ''), phone), organization = COALESCE(NULLIF(?, ''), organization) WHERE id = ? AND role = ?`)
	result, err := db.ExecContext(ctx, query, string(models.RoleResponder), phone, organization, userID, string(models.RoleCitizen))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrConflict
	}
	return nil
}

func (db *DB) GetStats(ctx context.Context) (*models.StatsResponse, error) {
	stats := &models.StatsResponse{}

	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reports").Scan(&stats.TotalReports); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, db.rebind("SELECT COUNT(*) FROM reports WHERE status IN ('reported', 'assigned', 'in_progress', 'on_the_way', 'arrived', 'animal_secured', 'transporting', 'at_vet')")).Scan(&stats.ActiveCases); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, db.rebind("SELECT COUNT(*) FROM reports WHERE status = 'resolved'")).Scan(&stats.Resolved); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, db.rebind("SELECT COUNT(*) FROM reports WHERE urgency = 'critical' AND status NOT IN ('resolved', 'closed')")).Scan(&stats.Critical); err != nil {
		return nil, err
	}

	return stats, nil
}

func (db *DB) GetAllReportsForExport(ctx context.Context) ([]*models.Report, error) {
	const pageSize = 1000
	var allReports []*models.Report
	for offset := 0; ; offset += pageSize {
		reports, _, err := db.GetReports(ctx, ReportFilter{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		allReports = append(allReports, reports...)
		if len(reports) < pageSize {
			return allReports, nil
		}
	}
}
