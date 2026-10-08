# PawSOS

PawSOS helps people report animals in distress and lets authorized responders record case updates. The public map uses approximate locations; private contact and operational details are restricted by the API.

---

## 🏗️ Architecture & Philosophy

PawSOS is built following the priority hierarchy:
**FUNCTIONALITY → PERFORMANCE → RELIABILITY → SECURITY → MAINTAINABILITY → UI POLISH**

- **Backend:** A single, lightweight, compiled Go service (`cmd/server`) utilizing standard library routing (`net/http`), bcrypt password hashing, HS256 JWT sessions, rate limiting, and structured logging.
- **Database:** Dual-driver SQL persistence supporting both pure-Go embedded SQLite (`modernc.org/sqlite`, zero CGo or external dependencies needed) and PostgreSQL (`github.com/lib/pq`) with connection pooling, migrations, and indexes.
- **Frontend:** Ultra-lightweight Vanilla HTML5, modern CSS3, and standard ES6 JavaScript. No heavy component libraries, no bloated dependencies, and no artificial AI styling (no glowing blobs or neon gradients).
- **Storage:** Validated JPG/PNG uploads retain a private original and a metadata-stripped display copy. Local container storage is not durable across ephemeral deployments.
- **Maps:** Leaflet & OpenStreetMap tile integration requiring zero paid API keys.

---

## Running PawSOS

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

## Production configuration

Production requires `JWT_SECRET` with at least 32 characters. New public registrations receive citizen access only. To provision the first administrator, set both `ADMIN_EMAIL` and `ADMIN_PASSWORD`; the password must contain 12–72 bytes. No default accounts or sample incident reports are created.

The Render blueprint uses SQLite and `/tmp` upload storage on the free plan. Both are ephemeral: database records and uploaded images may be lost when the service restarts or is redeployed. Configure durable database and object/file storage before relying on PawSOS for ongoing operations. Responder applications require an administrator to review and verify them; a badge is not a substitute for external identity or qualification checks.

PawSOS does not send push notifications or guarantee rescue dispatch. Case pages refresh recorded status periodically; nearby reports and the responder queue are bounded to the most recent 100 reports. No locally verified veterinary directory is configured. Browser-reported camera selection time and GPS are useful context, not independently authenticated evidence.

The report form asks the user to capture a browser location before opening the camera option. It records the location available when a camera-option image is selected separately from any later map-pin adjustment. This records browser-supplied context; it does not verify that the image was taken by a camera or that the supplied GPS data is authentic. Gallery uploads are labeled separately.

## Accounts and permissions

- **Citizen:** May create reports and look up a report by reference code.
- **Responder applicant:** A citizen may request review; access is granted only after administrator verification.
- **Verified responder:** May accept and update cases. Reporter contact and evidence details are limited to the assigned responder.
- **Administrator:** May review responder applications and export reports.

---

## 📡 API Endpoints

All responses follow standard REST conventions and structured JSON envelopes.

| Method | Endpoint | Access | Purpose |
|---|---|---|---|
| `GET` | `/health` | Public | Application liveness, uptime, environment |
| `GET` | `/ready` | Public | Database readiness healthcheck |
| `POST` | `/api/v1/auth/register` | Public (Rate-limited) | Register a citizen account; optionally request responder review |
| `POST` | `/api/v1/auth/login` | Public (Rate-limited) | Authenticate and obtain JWT token |
| `GET` | `/api/v1/auth/me` | Authenticated | Retrieve authenticated user profile |
| `POST` | `/api/v1/responders/application` | Authenticated | Request responder review |
| `GET` | `/api/v1/responders/pending` | Admin only | List pending responder applications |
| `POST` | `/api/v1/responders/{id}/verification` | Admin only | Approve or decline an application |
| `GET` | `/api/v1/reports` | Public (Masked) / Verified responder / Admin | List filtered incident reports with pagination |
| `POST` | `/api/v1/reports` | Public (Rate-limited) | Submit new report (supports multipart photo upload) |
| `GET` | `/api/v1/reports/{id}` | Public (Masked) / Responder | Fetch report details with chronological timeline |
| `POST` | `/api/v1/reports/{id}/assign` | Verified responder / Admin | Accept case and record optional ETA |
| `POST` | `/api/v1/reports/{id}/status` | Assigned responder / Admin | Record report status |
| `POST` | `/api/v1/reports/{id}/notes` | Assigned responder / Admin | Add situational note to incident timeline |
| `GET` | `/api/v1/reports/export` | Admin Only | Download full database incident log as CSV |
| `GET` | `/api/v1/stats` | Public | Database totals: total, active, resolved, critical |

---

## Automated testing

Run the automated Go tests:

```bash
go test ./...
```

---

## 🔒 Security Measures

1. **Server-Side Authorization:** Role checks (`citizen`, `responder`, `admin`) are strictly enforced in Go middleware. Client state manipulation cannot grant responder privileges.
2. **Reporter Privacy:** Public GET responses omit reporter identity/contact and reduce coordinate precision. Only the assigned verified responder or an administrator receives restricted case details.
3. **Upload Hardening:** JPG/PNG image bytes are validated and dimensions are bounded. Originals are stored privately with a SHA-256 digest; a separate metadata-stripped display copy is served publicly. Camera/gallery provenance is browser-reported and is not independently authenticated.
4. **Rate Limiting:** Sliding-window rate limiters protect auth and submission routes against brute force and DDoS.
5. **Security Headers:** Enforces `X-Content-Type-Options: nosniff`, `X-Frame-Options: SAMEORIGIN`, `X-XSS-Protection`, and `Referrer-Policy`.
6. **Graceful Shutdown:** Intercepts SIGINT and SIGTERM with a 10-second timeout to drain open connections safely.

---

## 📄 License
MIT License. Open-source animal welfare software.
