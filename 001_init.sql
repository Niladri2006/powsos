-- PawSOS Database Initial Schema Migration
-- Compatible with PostgreSQL and SQLite

CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'citizen',
    phone VARCHAR(64),
    organization VARCHAR(255),
    created_at TIMESTAMP NOT NULL
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
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS report_timeline (
    id SERIAL PRIMARY KEY,
    report_id VARCHAR(64) NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    status VARCHAR(32) NOT NULL,
    author_name VARCHAR(255) NOT NULL,
    author_role VARCHAR(32) NOT NULL,
    note TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_reports_status ON reports(status);
CREATE INDEX IF NOT EXISTS idx_reports_urgency ON reports(urgency);
CREATE INDEX IF NOT EXISTS idx_reports_created_at ON reports(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_reports_assigned ON reports(assigned_responder_id);
CREATE INDEX IF NOT EXISTS idx_timeline_report_id ON report_timeline(report_id);
