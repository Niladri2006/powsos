# 🐾 PawSOS — Production-Ready Animal Emergency & Rescue Platform

> **A high-performance, lightweight, full-stack application connecting citizens reporting distressed animals with authorized responders, rescue organizations, and veterinary clinics.**

---

## 🏗️ Architecture & Philosophy

PawSOS is built following the priority hierarchy:
**FUNCTIONALITY → PERFORMANCE → RELIABILITY → SECURITY → MAINTAINABILITY → UI POLISH**

- **Backend:** A single, lightweight, compiled Go service (`cmd/server`) utilizing standard library routing (`net/http`), bcrypt password hashing, HS256 JWT sessions, rate limiting, and structured logging.
- **Database:** Dual-driver SQL persistence supporting both pure-Go embedded SQLite (`modernc.org/sqlite`, zero CGo or external dependencies needed) and PostgreSQL (`github.com/lib/pq`) with connection pooling, migrations, and indexes.
- **Frontend:** Ultra-lightweight Vanilla HTML5, modern CSS3, and standard ES6 JavaScript. No heavy component libraries, no bloated dependencies, and no artificial AI styling (no glowing blobs or neon gradients).
- **Storage:** Secure local file storage for photo uploads with magic-byte MIME validation, size limits (5MB), and UUID safe naming.
- **Maps:** Leaflet & OpenStreetMap tile integration requiring zero paid API keys.

---

## 🚀 Running PawSOS

### 1. One-Click Local Launch (Linux / macOS)
```bash
./start.sh
```
This runs the compiled `./pawsos` binary (or compiles with Go if needed), initializes the database, and opens `http://localhost:8080`.

### 2. Windows Quick Launch
```cmd
start.bat
```

### 3. Run via Go Directly
```bash
go run ./cmd/server
```

### 4. Production Docker Deployment (PostgreSQL + Go)
```bash
docker-compose up -d --build
```
This launches:
- `pawsos_app` (Alpine-based container running the static Go binary as non-root user `pawsos`)
- `pawsos_postgres` (PostgreSQL 16 Alpine container with healthchecks and persistent volumes)

---

## 🔑 Default Accounts (Auto-Seeded)

| Persona | Email | Password | Role | Permissions |
|---|---|---|---|---|
| **Authorized Responder** | `responder@pawsos.org` | `responder123` | `responder` | View unmasked reporter contact, accept dispatches, update case status, post scene notes |
| **Central Dispatch Admin** | `admin@pawsos.org` | `adminpassword123` | `admin` | Full responder powers + database CSV export |
| **Citizen (Public)** | Anonymous / Self-registered | — | `citizen` | Submit reports with photos, track cases, privacy-masked public view |

---

## 📡 API Endpoints

All responses follow standard REST conventions and structured JSON envelopes.

| Method | Endpoint | Access | Purpose |
|---|---|---|---|
| `GET` | `/health` | Public | Application liveness, uptime, environment |
| `GET` | `/ready` | Public | Database readiness healthcheck |
| `POST` | `/api/v1/auth/register` | Public (Rate-limited) | Register new citizen or responder account |
| `POST` | `/api/v1/auth/login` | Public (Rate-limited) | Authenticate and obtain JWT token |
| `GET` | `/api/v1/auth/me` | Authenticated | Retrieve authenticated user profile |
| `GET` | `/api/v1/reports` | Public (Masked) / Responder | List filtered incident reports with pagination |
| `POST` | `/api/v1/reports` | Public (Rate-limited) | Submit new report (supports multipart photo upload) |
| `GET` | `/api/v1/reports/{id}` | Public (Masked) / Responder | Fetch report details with chronological timeline |
| `POST` | `/api/v1/reports/{id}/assign` | Responder / Admin | Assign case to responder with ETA |
| `POST` | `/api/v1/reports/{id}/status` | Responder / Admin | Update report status (`in_progress`, `resolved`, etc.) |
| `POST` | `/api/v1/reports/{id}/notes` | Responder / Admin | Add situational note to incident timeline |
| `GET` | `/api/v1/reports/export` | Admin Only | Download full database incident log as CSV |
| `GET` | `/api/v1/stats` | Public | Database totals: total, active, resolved, critical |

---

## 🧪 Automated Testing

PawSOS includes comprehensive automated integration and unit tests covering health, auth, permissions, report lifecycles, and privacy masking:

```bash
go test -v ./tests/...
```

Test suite output:
```
=== RUN   TestHealthAndReady
--- PASS: TestHealthAndReady (0.00s)
=== RUN   TestAuthWorkflow
--- PASS: TestAuthWorkflow (0.21s)
=== RUN   TestReportWorkflowAndPermissions
--- PASS: TestReportWorkflowAndPermissions (0.00s)
PASS
ok      pawsos/tests    0.225s
```

---

## 🔒 Security Measures

1. **Server-Side Authorization:** Role checks (`citizen`, `responder`, `admin`) are strictly enforced in Go middleware. Client state manipulation cannot grant responder privileges.
2. **Reporter Privacy Masking:** Public GET requests mask reporter phone numbers (e.g. `+1 555-••••`). Only verified responders assigned to the case receive the unmasked phone number.
3. **Upload Hardening:** Uploaded images are checked via HTTP MIME sniffing and Go's `image.DecodeConfig` to prevent polyglot file execution. Stored files are assigned random UUIDs.
4. **Rate Limiting:** Sliding-window rate limiters protect auth and submission routes against brute force and DDoS.
5. **Security Headers:** Enforces `X-Content-Type-Options: nosniff`, `X-Frame-Options: SAMEORIGIN`, `X-XSS-Protection`, and `Referrer-Policy`.
6. **Graceful Shutdown:** Intercepts SIGINT and SIGTERM with a 10-second timeout to drain open connections safely.

---

## 📄 License
MIT License. Open-source animal welfare software.
