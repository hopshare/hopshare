# Hopshare

Starter scaffold for the Hopshare application. The service is a single Go binary that will serve HTML via Templ/HTMX/Alpine with Postgres as the data store.

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
1. Copy `.env.example` to `.env` and set `HOPSHARE_DB_URL`.
2. Import a Postgres driver in `cmd/server/main.go` (for example, pgx stdlib) so `database/sql` knows how to talk to Postgres.
3. Run migrations: `go run ./cmd/migrate` (uses the Go migration runner, no psql needed).
4. Run the server: `go run ./cmd/server` (also runs migrations on startup).

Health endpoint: `GET /healthz` returns `200 OK`.

## Database migrations
- Add new SQL files to `deploy/migrations/` with a numeric prefix (e.g., `0002_add_tables.sql`). Files run in lexicographic order via the embedded migration runner.
- Apply pending migrations with `go run ./cmd/migrate` using `HOPSHARE_DB_URL` (or `DATABASE_URL`) for the Postgres connection string.
