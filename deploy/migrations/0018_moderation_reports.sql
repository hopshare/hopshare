CREATE TABLE moderation_reports (
    id BIGSERIAL PRIMARY KEY,
    organization_id BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    hop_id BIGINT NOT NULL REFERENCES hops(id) ON DELETE CASCADE,
    report_type TEXT NOT NULL,
    hop_comment_id BIGINT,
    hop_image_id BIGINT,
    reported_member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
    content_member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
    content_summary TEXT NOT NULL DEFAULT '',
    reporter_details TEXT,
    status TEXT NOT NULL DEFAULT 'open',
    resolution_action TEXT,
    resolved_by_member_id BIGINT REFERENCES members(id) ON DELETE RESTRICT,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (report_type IN ('hop_comment', 'hop_image')),
    CHECK (
        (report_type = 'hop_comment' AND hop_comment_id IS NOT NULL AND hop_image_id IS NULL) OR
        (report_type = 'hop_image' AND hop_image_id IS NOT NULL AND hop_comment_id IS NULL)
    ),
    CHECK (status IN ('open', 'dismissed', 'actioned')),
    CHECK (
        (status = 'open' AND resolution_action IS NULL AND resolved_by_member_id IS NULL AND resolved_at IS NULL) OR
        (status IN ('dismissed', 'actioned') AND resolution_action IS NOT NULL AND resolved_by_member_id IS NOT NULL AND resolved_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX moderation_reports_open_comment_unique_idx
    ON moderation_reports (reported_member_id, hop_comment_id)
    WHERE hop_comment_id IS NOT NULL AND status = 'open';

CREATE UNIQUE INDEX moderation_reports_open_image_unique_idx
    ON moderation_reports (reported_member_id, hop_image_id)
    WHERE hop_image_id IS NOT NULL AND status = 'open';

CREATE INDEX moderation_reports_status_created_at_idx
    ON moderation_reports (status, created_at DESC);

CREATE INDEX moderation_reports_report_type_status_created_at_idx
    ON moderation_reports (report_type, status, created_at DESC);

CREATE INDEX moderation_reports_org_created_at_idx
    ON moderation_reports (organization_id, created_at DESC);
