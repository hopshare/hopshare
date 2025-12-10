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
