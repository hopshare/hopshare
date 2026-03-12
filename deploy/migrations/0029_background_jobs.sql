CREATE TABLE background_jobs (
    name TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    interval_seconds INT NOT NULL,
    lease_seconds INT NOT NULL,
    next_run_at TIMESTAMPTZ NOT NULL,
    lease_until TIMESTAMPTZ,
    locked_by TEXT,
    state TEXT NOT NULL DEFAULT 'idle',
    last_started_at TIMESTAMPTZ,
    last_finished_at TIMESTAMPTZ,
    last_success_at TIMESTAMPTZ,
    last_duration_ms INT,
    consecutive_failures INT NOT NULL DEFAULT 0,
    last_error TEXT,
    last_result TEXT,
    CHECK (interval_seconds > 0),
    CHECK (lease_seconds > 0),
    CHECK (last_duration_ms IS NULL OR last_duration_ms >= 0),
    CHECK (consecutive_failures >= 0)
);

CREATE INDEX background_jobs_next_run_idx
    ON background_jobs (next_run_at)
    WHERE enabled = TRUE;

CREATE INDEX hops_expire_due_idx
    ON hops (expires_at)
    WHERE status IN ('open', 'accepted') AND expires_at IS NOT NULL;
