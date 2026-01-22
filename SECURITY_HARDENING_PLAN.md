# Security Hardening Plan — hopShare

Date: 2026-01-22

This plan prioritizes fixes that reduce account takeover risk, data exposure, and unauthorized state changes. Items are grouped by priority and include concrete implementation guidance, acceptance criteria, and notes for verification.

## P0 — Critical (Do these first)

### 1) Secure password reset flow
**Risks addressed:** account takeover, token leakage, indefinite token validity.

**Plan:**
- Remove reset links from HTML responses and logs.
- Store reset tokens in the database with **hashed tokens**, **creation time**, **expiry**, and **used_at**.
- Enforce token expiry (15–60 minutes) and one‑time use.
- Add rate limiting per email/IP for reset requests.

**Implementation notes:**
- Add a `password_reset_tokens` table with columns: `id`, `member_id`, `token_hash`, `created_at`, `expires_at`, `used_at`, `requested_ip`.
- Hash token with SHA‑256 before storage. Compare with `ConstantTimeCompare`.
- Delete or mark used tokens on success; clean up expired tokens via a periodic job or on access.
- Update `/forgot-password` to always return a generic message; no link in UI or logs.

**Acceptance criteria:**
- Reset tokens expire and cannot be reused.
- No reset links appear in logs or UI.
- Only email delivery conveys the token.

### 2) Add CSRF protection + remove state‑changing GET
**Risks addressed:** unauthorized actions via cross‑site requests.

**Plan:**
- Remove GET logout route; POST only.
- Add CSRF tokens to all HTML forms and verify on POST.

**Implementation notes:**
- Generate a per‑session CSRF token; store in session or signed cookie.
- Inject token into all forms in templ components (hidden input).
- Validate CSRF token in middleware for all POST routes.

**Acceptance criteria:**
- All POST endpoints reject missing/invalid CSRF tokens.
- `/logout` only accepts POST.

### 3) Harden session cookies
**Risks addressed:** session hijacking and replay.

**Plan:**
- Set `Secure`, `HttpOnly`, `SameSite=Strict` (or `Lax` if cross‑site flow is required).
- Implement session expiration (absolute + idle).
- Rotate session ID on login and password change.

**Implementation notes:**
- Extend `SessionManager` to track created/last_used timestamps.
- Reject expired tokens in `withUser`.
- Consider persistent store if running multiple instances.

**Acceptance criteria:**
- Cookies are Secure in production.
- Sessions expire automatically and are rotated on login/password change.

## P1 — High

### 4) Stop logging sensitive PII
**Risks addressed:** data exposure in logs.

**Plan:**
- Remove signup logs that include email, city/state, interests.
- Never log reset links.

**Acceptance criteria:**
- Logs do not include user PII beyond member IDs or minimal metadata.

### 5) Secure file upload handling
**Risks addressed:** XSS, malware, content‑type spoofing.

**Plan:**
- Whitelist image types (`image/jpeg`, `image/png`, `image/webp`).
- Reject SVG and unknown content types.
- Re‑encode server‑side to a safe format (optional but recommended).
- Add `X-Content-Type-Options: nosniff` header.

**Acceptance criteria:**
- Only whitelisted formats accepted.
- Uploaded SVGs are rejected.

### 6) Add rate limiting to auth endpoints
**Risks addressed:** brute force, enumeration.

**Plan:**
- Add IP/user based rate limits for:
  - `/login`
  - `/forgot-password`
  - `/signup`
- Consider exponential backoff or temporary lockout on repeated failed login attempts.

**Acceptance criteria:**
- Repeated attempts are throttled (verify in tests/logs).

## P2 — Medium

### 7) Add global security headers
**Risks addressed:** clickjacking, XSS, data leakage.

**Plan:**
- Add middleware to set:
  - `Content-Security-Policy` (start with strict‑by‑default, allow needed CDNs)
  - `X-Frame-Options: DENY`
  - `Referrer-Policy: no-referrer`
  - `Permissions-Policy: camera=(), microphone=(), geolocation=()`
  - `X-Content-Type-Options: nosniff`

**Acceptance criteria:**
- Headers are present on all HTML responses.

### 8) Add password strength requirements
**Risks addressed:** weak passwords.

**Plan:**
- Enforce minimum length (>= 12), and a mix of character classes.
- Provide clear UI validation errors.

**Acceptance criteria:**
- Weak passwords are rejected consistently on signup and password change.

### 9) Audit message and hop privacy authorization
**Risks addressed:** data exposure across orgs.

**Plan:**
- Add explicit tests ensuring hop/image/comment/message access requires proper membership and hop association.

**Acceptance criteria:**
- Tests cover org boundary and privacy checks.

## P3 — Long‑term

### 10) Centralized audit logging
**Plan:**
- Add audit trail for: login, password change, profile changes, offer acceptance/decline.
- Store minimally required metadata; no PII in logs.

### 11) Secrets management
**Plan:**
- Ensure secrets not logged and use env vars or a secrets manager.

## Verification Checklist

- [ ] Reset tokens expire and are one‑time use
- [ ] CSRF token enforced for all POST forms
- [ ] Logout is POST only
- [ ] Session cookie is Secure + SameSite + HttpOnly
- [ ] Rate limiting for login/reset/signup
- [ ] Upload restrictions and `nosniff`
- [ ] Security headers present
- [ ] PII removed from logs

## Notes

- This plan assumes a single Go binary with `net/http` and Templ. Middleware is the best place to implement headers, CSRF, and rate limiting without touching every handler.
- If you want, I can implement the P0/P1 items in a follow‑up pass.
