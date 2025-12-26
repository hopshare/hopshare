# Repository Guidelines

## Architecture & Layout
- Single Go binary serving HTML via Templ + HTMX + Alpine; assets live on the filesystem under `web/templates` and `web/static`. No REST layer—handlers respond to HTMX requests and form posts.
- Back end stays framework-light: stdlib `net/http`, Templ rendering, and Postgres via `database/sql`. Keep domain logic in service packages callable without HTTP so perf/scale tests can hit the core directly.
- Configuration via env vars prefixed `HOPSHARE_` (e.g., `HOPSHARE_DB_URL`); keep `.env.example` current.

## Project Structure
- `cmd/server/` — entrypoint wiring config, DB, router; keep thin.
- `internal/<domain>/` — business logic (e.g., `timebank`, `accounts`); `internal/http` for routes/handlers; `internal/database` for DB queries.
- `web/templates/` — Templ files; `web/static/` — JS/CSS (HTMX, Alpine; add Tailwind or bundlers only if necessary).
- `docs/` for design notes; `scripts/` for dev ops; `deploy/` for container manifests; avoid committing large binaries.

## Build, Run, Dev
- `go run ./cmd/server` — start the site; requires Postgres running and env vars set.
- `go test ./...` — unit + integration (use build tags like `//go:build integration` when hitting DB).
- `go test -run TestName -bench . ./internal/...` — focused behavior and benchmark runs for service packages.
- `gofmt -w .` and `go vet ./...` before commit; `go mod tidy` when imports change.

## Coding Style & Naming
- Standard Go style; exported symbols documented; avoid stutter (`timebank.Service` over `timebank.TimebankService`).
- Templates favor small, composable components; prefer HTMX swaps and Alpine state over custom JS bundles.
- Errors: wrap with context (`fmt.Errorf("create user: %w", err)`) and use typed/sentinel errors for business rules.
- Keep code as simple as possible, avoid any clever tricks or techniques. MUST use standard library as much as possible, keep external dependencies to a MINIMUM.
- NEVER edit the TODO.md file- that file is only for manual changes by me. Ignore any changes in TODO.md that might differ from the current git commit.

## Testing Guidelines
- Mirror tests next to code (`internal/timebank/service_test.go`); use table-driven cases.
- Default to unit tests with fakes; integration tests hit Postgres via test DB and migrations.
- Perf/scale: write benchmarks against service layer (no HTTP) and run with `go test -bench . ./internal/...`.

## Commit & Pull Request Guidelines
- Imperative subjects; Conventional Commit prefixes welcome. Keep commits focused and mention issue IDs.
- PRs should state intent, tests run, env/config changes, and include UI screenshots or payload examples for HTMX endpoints.

## Security & Configuration
- Never commit secrets; use `.env.example` or `config.example.yaml` for defaults.
- Review dependency updates; pin minimal required versions and avoid logging sensitive data.
