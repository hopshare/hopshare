# Hopshare

Starter scaffold for the Hopshare application. The service is a single Go binary that serves HTML via Templ/HTMX/Alpine with Postgres as the data store and a minimal in-memory auth/session layer for demo flows.

## Layout
- `cmd/server/` — entrypoint wiring config, DB, and HTTP router.
- `internal/config/` — loads `HOPSHARE_*` environment variables.
- `internal/database/` — database/sql helpers for Postgres connections.
- `internal/http/` — HTTP router and handlers.
- `web/templates/` — Templ view components.
- `web/static/` — JS/CSS assets (HTMX/Alpine).
- `docs/` — design notes.
- `scripts/` — dev-ops scripts.
- `deploy/` — deployment manifests and SQL migrations (`deploy/migrations/`).

## Getting Started
1. Copy `.env.example` to `.env` and set `HOPSHARE_DB_URL` (required).
2. Generate templates: `templ generate`.
3. Fetch modules: `go mod tidy` (will download `templ` and other deps).
4. Run migrations: `go run ./cmd/migrate` (uses the Go migration runner, no psql needed).
5. Run the server: `go run ./cmd/server` (also runs migrations on startup).

Health endpoint: `GET /healthz` returns `200 OK`.

## Testing

### Quick test run
- `go test ./...`

### Database-backed integration tests
The HTTP and service integration tests use a real Postgres connection:
- `internal/http/*_integration_test.go`
- `internal/service/service_test.go`

These tests read `HOPSHARE_DB_URL` first, then `DATABASE_URL`. If neither is set, they are skipped.

Recommended: use a dedicated disposable database for tests.

Example setup:
1. Create a test database (example name: `hopshare_test`).
2. Export URL:
   - `export HOPSHARE_DB_URL='postgres://user:pass@localhost:5432/hopshare_test?sslmode=disable'`
3. Apply migrations:
   - `go run ./cmd/migrate`

Run test suites:
- HTTP integration tests:
  - `go test ./internal/http -count=1 -v`
- Service integration tests:
  - `go test ./internal/service -count=1 -v`
- Full repo (with DB enabled):
  - `go test ./... -count=1`

Notes:
- Integration tests create and update real rows. Reusing the same DB is supported, but data will accumulate.
- If you want a clean run each time, recreate the test database before running tests.
- Handler logs may include expected error messages during negative test cases; this does not necessarily indicate a failing test.

## Container / Podman
Use the provided `Containerfile` and scripts in `deploy/scripts/` to build and run Hopshare with Postgres in a Podman pod.

Requirements:
- Podman installed and running.

Build image manually:
- `podman build -t hopshare:local -f Containerfile .`

Start Hopshare + Postgres pod:
- `deploy/scripts/start.sh`

Stop Hopshare + Postgres pod:
- `deploy/scripts/stop.sh`

Default runtime values (can be overridden with env vars):
- `POD_NAME=hopshare`
- `APP_IMAGE=hopshare:local`
- `APP_CONTAINER=hopshare-app`
- `DB_CONTAINER=hopshare-db`
- `APP_PORT=8080`
- `DB_PORT=5432`
- `DB_NAME=hopshare`
- `DB_USER=hopshare`
- `DB_PASSWORD=hopshare`
- `DB_DATA_DIR=deploy/data/postgres`

Example custom run:
- `APP_PORT=9090 DB_PASSWORD=supersecret deploy/scripts/start.sh`

The app is exposed at `http://localhost:${APP_PORT}` and Postgres data persists under `deploy/data/postgres` by default.

## Demo Web Flows
- Landing page at `/` with calls to action for Login and Request to join.
- Login at `/login` for demo user `demo@hopshare.org` / `password123` (sets a cookie-based session).
- Request to join at `/signup` posts to `/signup-success` confirmation.
- Forgot/reset password at `/forgot-password` → `/reset-password?token=...` (in-memory tokens) updates the demo password.
- Authenticated home at `/my-hopshare` (redirects to `/login` when not signed in).
- Logout via `/logout` clears the session and returns to `/`.

## Database migrations
- Add new SQL files to `deploy/migrations/` with a numeric prefix (e.g., `0002_add_tables.sql`). Files run in lexicographic order via the embedded migration runner.
- Apply pending migrations with `go run ./cmd/migrate` using `HOPSHARE_DB_URL` (or `DATABASE_URL`) for the Postgres connection string.
