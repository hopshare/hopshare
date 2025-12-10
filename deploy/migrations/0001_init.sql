CREATE TABLE members (
    id BIGSERIAL PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    preferred_contact_method TEXT NOT NULL CHECK (preferred_contact_method IN ('email', 'phone', 'other')),
    preferred_contact TEXT NOT NULL,
    profile_picture_url TEXT,
    city TEXT,
    state TEXT,
    interests TEXT,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Administrators are special members with cross-organization powers.
CREATE TABLE administrators (
    member_id BIGINT PRIMARY KEY REFERENCES members(id) ON DELETE CASCADE,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    granted_by BIGINT REFERENCES members(id),
    note TEXT
);

CREATE TABLE organizations (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    logo_url TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_by BIGINT REFERENCES members(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Memberships capture member/organization relationships and roles.
CREATE TABLE organization_memberships (
    id BIGSERIAL PRIMARY KEY,
    organization_id BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('member', 'owner')),
    is_primary_owner BOOLEAN NOT NULL DEFAULT FALSE,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    left_at TIMESTAMPTZ,
    CHECK (left_at IS NULL OR left_at > joined_at),
    CHECK (NOT is_primary_owner OR role = 'owner'),
    CHECK (NOT is_primary_owner OR left_at IS NULL)
);

-- Only one active membership per member/org at a time; allows history when left_at is set.
CREATE UNIQUE INDEX organization_memberships_active_member_org_idx
    ON organization_memberships (organization_id, member_id)
    WHERE left_at IS NULL;

-- Only one primary owner per organization, and a member can only be primary for one org.
CREATE UNIQUE INDEX organization_memberships_primary_owner_per_org_idx
    ON organization_memberships (organization_id)
    WHERE is_primary_owner;

CREATE UNIQUE INDEX organization_memberships_primary_owner_per_member_idx
    ON organization_memberships (member_id)
    WHERE is_primary_owner;

-- Requests to join organizations; approval handled by owners/admins in application logic.
CREATE TABLE membership_requests (
    id BIGSERIAL PRIMARY KEY,
    organization_id BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'rejected')),
    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at TIMESTAMPTZ,
    decided_by BIGINT REFERENCES members(id),
    decision_note TEXT
);

CREATE UNIQUE INDEX membership_requests_pending_idx
    ON membership_requests (organization_id, member_id)
    WHERE status = 'pending';

-- Invitations cover both member and owner invites issued by owners or admins.
CREATE TABLE organization_invitations (
    id BIGSERIAL PRIMARY KEY,
    organization_id BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    invited_member_id BIGINT REFERENCES members(id) ON DELETE CASCADE,
    invited_email TEXT,
    role TEXT NOT NULL CHECK (role IN ('member', 'owner')),
    invited_by BIGINT REFERENCES members(id),
    status TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'declined', 'expired')),
    invited_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX organization_invitations_pending_member_idx
    ON organization_invitations (organization_id, invited_member_id)
    WHERE status = 'pending' AND invited_member_id IS NOT NULL;

CREATE UNIQUE INDEX organization_invitations_pending_email_idx
    ON organization_invitations (organization_id, invited_email)
    WHERE status = 'pending' AND invited_email IS NOT NULL;
