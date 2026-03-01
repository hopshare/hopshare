# Hopshare

Hopshare is a single Go web app that serves server-rendered HTML with Templ + HTMX + Alpine, backed by PostgreSQL.

## Project Layout
- `cmd/server/` — entrypoint wiring config, DB, and HTTP router.
- `cmd/migrate/` — applies SQL migrations.
- `cmd/bulkload/` — optional local data seeding helper.
- `internal/config/` — loads `HOPSHARE_*` environment variables.
- `internal/database/` — database/sql helpers for Postgres connections.
- `internal/http/` — HTTP router and handlers.
- `internal/service/` — business/domain logic.
- `web/templates/` — Templ view components.
- `web/static/` — JS/CSS assets (HTMX/Alpine).
- `docs/` — design notes.
- `scripts/` — dev-ops scripts.
- `deploy/` — deployment manifests and SQL migrations (`deploy/migrations/`).

## Prerequisites
- Go `1.24.x` (module toolchain is `go1.24.1`).
- PostgreSQL running locally or reachable by URL.
- `templ` CLI for template generation.

Install `templ` if needed:
- `go install github.com/a-h/templ/cmd/templ@latest`

## Environment Variables
Copy `.env.example` to `.env`, then export values into your shell:
- `cp .env.example .env`
- `set -a; source .env; set +a`

Important variables:
- `HOPSHARE_DB_URL` (required): Postgres connection string used by app and migration command.
- `HOPSHARE_ADDR` (optional): server bind address, default `:8080`.
- `HOPSHARE_ENV` (optional): environment label (for example `development`).
- `HOPSHARE_ADMINS` (optional): comma-separated usernames with admin access. Matching is case-insensitive and spaces are ignored.
- `HOPSHARE_TIMEZONE` (optional): IANA timezone name used for rendered timestamps (for example `America/New_York`, `UTC`). Invalid values fail startup.
- `FEATURE_EMAIL` (optional): enable email-centric flows (`true`/`false`, default `true`). When `false`, email verification is not required for new signups and Mailgun config is optional.
- `FEATURE_HOP_PICTURES` (optional): controls the Hop Details Pictures panel (`true`/`false`, default `false`). When `false`, the Pictures panel is hidden.
- `HOPSHARE_PUBLIC_BASE_URL` (optional): absolute base URL used to build password reset links in emails. Default `http://localhost:8080`.
- `HOPSHARE_COOKIE_SECURE` (optional): when `true`, auth/CSRF/post-auth cookies are marked `Secure`. Default `true` (production-safe). Set `false` only for local HTTP testing.
- `HOPSHARE_SESSION_ABSOLUTE_TTL` (optional): maximum session lifetime since login (Go duration, default `168h`).
- `HOPSHARE_SESSION_IDLE_TIMEOUT` (optional): maximum idle session time since last request activity (Go duration, default `24h`).
- `HOPSHARE_MAILGUN_API_BASE_URL` (optional): Mailgun API base URL. Default `https://api.mailgun.net`.
- `HOPSHARE_MAILGUN_DOMAIN` (required when `FEATURE_EMAIL=true`): Mailgun sending domain.
- `HOPSHARE_MAILGUN_API_KEY` (required when `FEATURE_EMAIL=true`): Mailgun API key.
- `HOPSHARE_MAILGUN_FROM_ADDRESS` (required when `FEATURE_EMAIL=true`): from address used for reset emails.

## Running Locally
1. Export env vars (`source .env` as shown above).
2. Generate templates:
   - `templ generate`
3. Apply migrations:
   - `go run ./cmd/migrate`
4. Start the app:
   - `go run ./cmd/server`

Notes:
- `cmd/server` also runs migrations on startup.
- Health endpoint: `GET /healthz` returns `200 OK`.

## Optional Local Seed Data
Generate sample members/orgs:
- `go run ./cmd/bulkload --members 100 --orgs 8`

Default generated member login pattern:
- Username: `member_<n>`
- Password: `password123`

## Testing
### Full Regression
- `go test ./...`

### Recommended Local Test Workflow
1. Create a dedicated test database (example: `hopshare_test`).
2. Export DB URL:
   - `export HOPSHARE_DB_URL='postgres://user:pass@localhost:5432/hopshare_test?sslmode=disable'`
3. Run migrations:
   - `go run ./cmd/migrate`
4. Run tests:
   - `go test ./... -count=1`

### Targeted Test Commands
- HTTP integration suite:
  - `go test ./internal/http -count=1 -v`
- Service suite:
  - `go test ./internal/service -count=1 -v`
- Admin-focused integration tests only:
  - `go test ./internal/http -run TestAdmin -count=1 -v`

### Integration Test Behavior
- Integration tests use real Postgres tables and real migrations.
- Test DB URL lookup in tests:
  1. `HOPSHARE_DB_URL`
  2. `DATABASE_URL`
- If neither is set, DB-backed integration tests are skipped.
- Test data accumulates unless you recreate/reset the DB.

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

## CI/CD (GitHub Actions + Quay)
This repo includes Podman-based GitHub Actions workflows for CI and image publishing to `quay.io/hopshare/hopshare`.

Workflows:
- `.github/workflows/ci.yml`
  - Triggers: pull requests and pushes to `main`.
  - Runs: `go test ./... -count=1`, `go vet ./...`, `podman build -f Containerfile`.
- `.github/workflows/publish-nightly.yml`
  - Triggers: nightly schedule (`0 7 * * *` UTC) and manual `workflow_dispatch`.
  - Runs tests/vet/build, then pushes:
    - `nightly-YYYYMMDD-HHMM` (UTC timestamp)
    - `nightly`
    - `sha-<12-char-commit>`
- `.github/workflows/publish-release.yml`
  - Trigger: GitHub Release published event.
  - Runs tests/vet/build, then pushes:
    - exact release tag (example: `v1.2.3`)
    - version without leading `v` when applicable (example: `1.2.3`)
    - `latest` for non-prerelease releases only

### Quay Robot Credentials
Create a Quay robot account with push access to `hopshare/hopshare`, then set these GitHub repository secrets:
- `QUAY_USERNAME` (example: `hopshare+ci`)
- `QUAY_PASSWORD` (robot token/password)

The publish workflows fail fast when these secrets are missing.

### Release Flow
1. Create/push a git tag (usually `vX.Y.Z`).
2. Create a GitHub Release from that tag.
3. The release workflow publishes the corresponding image tags to Quay.

## Admin + Security Notes
- Admin routes are under `/admin` and require:
  - authenticated user
  - username listed in `HOPSHARE_ADMINS`
- Non-admin access to `/admin` routes returns `403 Unauthorized`.
- Admin audit logging covers mutating actions only (not read-only views).
- Audit exports are themselves audited.

## Database migrations
- Add new SQL files to `deploy/migrations/` with a numeric prefix (e.g., `0002_add_tables.sql`). Files run in lexicographic order via the embedded migration runner.
- Apply pending migrations with `go run ./cmd/migrate` using `HOPSHARE_DB_URL`.
