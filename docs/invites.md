**Final Build-Ready Spec**

**1. Final Business Rules**
1. Signup has 2 modes: invited and non-invited.
2. Invited flow is only available when `FEATURE_EMAIL=true`.
3. If `FEATURE_EMAIL=false`, invite feature is disabled end-to-end, and Invite tab is hidden.
4. Invite quota is org-level: max 30 successfully sent invites per org per app-timezone day.
5. Owners may submit multiple blasts per day until the 30 successful-send limit is reached.
6. Blast is partial-success: valid/sendable emails are sent, others are skipped and reported.
7. Input format is comma-separated emails; normalize by trim, lowercase, dedupe, validate.
8. Max 30 is enforced after normalization and after subtracting already-used quota.
9. Existing active org members are screened out before send and included in an owner inbox summary.
10. Invite links expire exactly 14x24 hours after invite creation.
11. If the same org+email is invited again, any previous pending invite for that email is expired first.
12. Invite acceptance requires email identity match with invited email.
13. Logged-in email mismatch vs invite email is blocked with explicit “log out and use invited email” UX.
14. Expired invite has dedicated UX page with recovery guidance.
15. If org becomes disabled before acceptance, acceptance fails with friendly “org no longer exists” message.
16. Invite acceptance must be idempotent (double-click/retry safe).

---

**2. Endpoint List**

| Method | Path | Auth | Purpose | Request | Success | Errors/Notes |
|---|---|---|---|---|---|---|
| `POST` | `/organizations/manage` | Owner | Send invite blast via existing manage route | `action=invites`, `org_id`, `invite_emails` | Redirect to manage page with summary and tab=`invite` | If feature off, treat as unavailable; if not owner, forbidden |
| `GET` | `/invite` | Public | Invite landing and routing | `token` | Valid token routes user into correct flow | Invalid/expired/disabled-org pages |
| `GET` | `/signup` | Public | Invite-aware signup form | optional `invite_token` | Prefills/locks invited email when token valid | If token invalid/expired, show invite error |
| `POST` | `/signup` | Public | Create account with optional invite context | existing fields + optional `invite_token` | Redirect `/signup-success` (invite context preserved for verify) | Email uniqueness unchanged |
| `GET` | `/verify-email` | Public | Verify email, then accept invite if `invite_token` present | `token`, optional `invite_token` | Email verified; if invite valid and email matches, membership auto-created | Invite mismatch/expired handled with deterministic message |
| `POST` | `/verify-email/resend` | Public | Resend verify email with optional invite context | `email`, optional `invite_token` | Standard resend response | If invite token present, resend link carries it |
| `GET` | `/organizations/manage` | Owner | Show Invite tab with quota/status | `org_id`, `tab=invite` | Invite UI rendered with daily remaining count | Invite tab hidden when feature off |

---

**3. Service Contracts (Go-level)**

1. `SendOrganizationInviteBlast(ctx, db, params) (BlastResult, error)`  
`params`: `OrgID`, `OwnerMemberID`, `RawEmails`, `Now`, `AppTZ`, `PublicBaseURL`  
`BlastResult`: `SentCount`, `RemainingToday`, `InvalidEmails`, `DuplicateEmails`, `AlreadyMemberEmails`, `QuotaSkippedEmails`, `SendFailedEmails`, `ExpiredPreviousPendingCount`.

2. `ResolveOrganizationInvite(ctx, db, rawToken, now) (InviteResolution, error)`  
Returns invite row + org + email + status checks, without mutating.

3. `AcceptOrganizationInvite(ctx, db, rawToken, memberID, memberEmail, now) (AcceptResult, error)`  
Idempotent. Creates membership if needed, updates invite status to accepted, returns org slug and flags like `AlreadyMember`.

4. `ExpirePendingInviteForEmail(ctx, db, orgID, emailLower, now) (int, error)`  
Used before creating a new invite for same org+email.

5. `CountSuccessfulInvitesToday(ctx, db, orgID, dayStart, dayEnd) (int, error)`  
Org-level quota counter in app timezone window.

6. `ParseAndNormalizeInviteEmails(raw string) (normalized []string, invalid []string, duplicates []string)`  
Comma required; trim/lowercase/dedupe.

7. `SendOwnerInviteBlastSummaryMessage(ctx, db, ownerID, orgID, blastResult) error`  
Uses internal inbox message system with detailed summary.

8. `SendOrganizationInviteEmail(ctx, toEmail, inviteURL, orgName, inviterDisplayName, expiresAt) error`  
Add to mail sender interface/implementation.

---

**4. DB Changes (New Migration, e.g. `0026_organization_invites_v2.sql`)**

1. Alter `organization_invitations` for tokenized invite links:
- Add `token_id TEXT`
- Add `token_hash TEXT`
- Add `expires_at TIMESTAMPTZ`
- Add `accepted_at TIMESTAMPTZ`
- Add `accepted_member_id BIGINT REFERENCES members(id) ON DELETE SET NULL`
- Keep existing `status` model; actively use `pending`, `accepted`, `expired`.

2. Index/constraint updates:
- Replace pending email unique index with case-insensitive expression index:  
  unique on `(organization_id, LOWER(invited_email))` where `status='pending' and invited_email is not null`.
- Add unique index on `token_id`.
- Add index on `(organization_id, invited_at)` for day-window quota counting.
- Add active-expiry index on `(status, expires_at)` for fast expiry checks.

3. Token storage format:
- Store `token_id` + SHA-256 `token_hash` (same security model as member tokens).
- Raw invite token delivered as `token_id.secret`; only hash is persisted for secret.

4. Backward compatibility:
- Existing rows can remain nullable for new columns if table previously unused.
- New app writes must always populate token/expiry columns for newly created invites.

---

**5. Invite Flow Specs**

1. New invited user:
- Owner sends invite.
- Recipient opens `/invite?token=...`.
- If no account, route to invite-aware signup with invited email locked.
- Signup sends verification email.
- Verification link includes `invite_token`.
- `/verify-email` verifies account then auto-accepts invite, then redirects to login with success and org context.

2. Existing verified user:
- Opens `/invite?token=...`.
- If logged out, login then return to invite URL.
- If logged-in email matches invited email, auto-accept and redirect to org page.
- If already active member, no duplicate membership; success UX still shown.

3. Existing unverified user:
- Invite landing routes to verify flow (resend endpoint supports invite context).
- After verify, invite is accepted automatically.

4. Mismatch case:
- Logged-in user email != invited email: block acceptance and show “log out and use invited email.”

5. Expired/invalid/disabled-org cases:
- Dedicated pages:
  - Expired: “Invite expired. Contact an owner for a new invite.”
  - Disabled org: “Sorry, this Organization no longer exists...”
  - Invalid token: generic invalid invite message.

---

**6. Non-Invited Onboarding Wizard**

1. Replace no-org two-button panel on My hopShare with a short wizard.
2. Step 1 explains join vs create responsibilities in plain language.
3. Step 2 choose path:
- Join path: go to org search/list, then request membership.
- Create path: go to create-org page.
4. Step 3 (post-create): redirect owner to Manage Organization with Invite tab emphasized for initial blast.

---

**7. Daily Quota/Time Rules**

1. Timezone source: app timezone config (`HOPSHARE_TIMEZONE`).
2. Daily quota window: `00:00:01` to `23:59:59` in app timezone.
3. Expiry interval: exactly `14 * 24h` from invite creation timestamp.
4. Quota counts only successfully sent invites (not invalid/skipped/failed sends).

---

**8. Test Matrix**

| Scope | ID | Scenario | Expected |
|---|---|---|---|
| Service | INV-SVC-01 | parse comma list with spaces/case/dupes | normalized lowercase unique list |
| Service | INV-SVC-02 | invalid email formats mixed with valid | partial success classification |
| Service | INV-SVC-03 | org-level quota with remaining slots | sends only up to remaining slots |
| Service | INV-SVC-04 | existing active members in recipient list | skipped and reported |
| Service | INV-SVC-05 | same email reinvited while pending | previous pending invite expired |
| Service | INV-SVC-06 | invite token validation success | resolves org/email/status |
| Service | INV-SVC-07 | invite token expired | returns expired error |
| Service | INV-SVC-08 | email mismatch on accept | returns mismatch error |
| Service | INV-SVC-09 | idempotent accept called twice | second call no-op success |
| Service | INV-SVC-10 | accept when org disabled | deterministic disabled-org error |
| HTTP | INV-HTTP-01 | owner opens manage page | Invite tab visible when feature on |
| HTTP | INV-HTTP-02 | feature email off | Invite tab hidden |
| HTTP | INV-HTTP-03 | non-owner posts invite action | forbidden/unauthorized |
| HTTP | INV-HTTP-04 | invite blast partial success | success message + detailed summary |
| HTTP | INV-HTTP-05 | invite blast quota exceeded | sends up to limit and reports skipped |
| HTTP | INV-HTTP-06 | `/invite` invalid token | invalid invite page |
| HTTP | INV-HTTP-07 | `/invite` expired token | expired page |
| HTTP | INV-HTTP-08 | `/invite` org disabled | disabled-org page |
| HTTP | INV-HTTP-09 | logged-in mismatch email | blocked with logout/login guidance |
| HTTP | INV-HTTP-10 | existing verified user accept | redirected to org with success |
| HTTP | INV-HTTP-11 | signup from invite + verify | auto-joins org after verify |
| HTTP | INV-HTTP-12 | verify resend with invite context | resend link includes invite token |
| HTTP | INV-HTTP-13 | feature off direct invite URL hit | unavailable behavior |
| Integration | INV-INT-01 | two owners blasting same org/day | shared org quota enforced |
| Integration | INV-INT-02 | concurrent double accept same token | one membership, stable success path |
| Integration | INV-INT-03 | invite + membership request race | one active membership, no corruption |
| Integration | INV-INT-04 | owner summary inbox message | contains sent/skipped/failure details |
| Integration | INV-INT-05 | timezone day boundary behavior | quota resets at app-timezone boundary |

---

**9. Out-of-Date Rule Update**
1. Remove “max 5 organizations per member” from docs and enforce no cap as current policy.