package bulkload

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"time"

	"hopshare/internal/service"
	"hopshare/internal/types"
)

// Result captures statistics from a bulk load run.
type Result struct {
	MembersCreated       int
	OrganizationsCreated int
	MembershipsCreated   int
	UnassignedMembers    int
}

// Load inserts members and organizations, assigning members to orgs with random coverage.
func Load(ctx context.Context, db *sql.DB, memberCount, orgCount int) (Result, error) {
	if db == nil {
		return Result{}, service.ErrNilDB
	}
	if memberCount < 0 || orgCount < 0 {
		return Result{}, fmt.Errorf("counts must be non-negative")
	}
	if orgCount > 0 && memberCount == 0 {
		return Result{}, fmt.Errorf("cannot create organizations without members")
	}

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	// 5–30% of members will have no organization.
	unassignedRate := 0.05 + rnd.Float64()*0.25

	passwordHash, err := service.HashPassword("password123")
	if err != nil {
		return Result{}, err
	}

	members := make([]types.Member, 0, memberCount)
	for i := 0; i < memberCount; i++ {
		email := fmt.Sprintf("member_%d@example.com", i)
		username := fmt.Sprintf("member_%d", i)
		member := types.Member{
			FirstName:              fmt.Sprintf("Member%d", i),
			LastName:               "Hopshare",
			Username:               username,
			Email:                  email,
			PasswordHash:           passwordHash,
			PreferredContactMethod: types.ContactMethodEmail,
			PreferredContact:       email,
			Enabled:                true,
			Verified:               true,
		}
		created, err := service.CreateMember(ctx, db, member)
		if err != nil {
			return Result{}, fmt.Errorf("create member %s: %w", email, err)
		}
		members = append(members, created)
	}

	type orgSeed struct {
		org     types.Organization
		ownerID int64
	}
	orgs := make([]orgSeed, 0, orgCount)
	for i := 0; i < orgCount; i++ {
		owner := members[rnd.Intn(len(members))]
		orgName := fmt.Sprintf("Organization %d", i+1)
		org, err := service.CreateOrganization(ctx, db, orgName, owner.ID)
		if err != nil {
			return Result{}, fmt.Errorf("create organization %s: %w", orgName, err)
		}
		orgs = append(orgs, orgSeed{org: org, ownerID: owner.ID})
	}

	unassigned := make(map[int64]bool, len(members))
	for _, m := range members {
		if rnd.Float64() < unassignedRate {
			unassigned[m.ID] = true
		}
	}

	const maxOrgsPerMember = 5
	memberOrgCount := make(map[int64]int, len(members))
	membershipsCreated := 0

	stmt, err := db.PrepareContext(ctx, `
		INSERT INTO organization_memberships (organization_id, member_id, role, is_primary_owner)
		VALUES ($1, $2, 'member', FALSE)
		ON CONFLICT DO NOTHING
	`)
	if err != nil {
		return Result{}, fmt.Errorf("prepare membership insert: %w", err)
	}
	defer stmt.Close()

	for _, o := range orgs {
		// Randomly assign between 20% and 80% of members to this org.
		assignRate := 0.2 + rnd.Float64()*0.6
		memberOrgCount[o.ownerID]++
		membershipsCreated++
		for _, m := range members {
			if m.ID == o.ownerID {
				continue // owner already added by CreateOrganization
			}
			if unassigned[m.ID] {
				continue
			}
			if memberOrgCount[m.ID] >= maxOrgsPerMember {
				continue
			}
			if rnd.Float64() > assignRate {
				continue
			}
			if _, err := stmt.ExecContext(ctx, o.org.ID, m.ID); err != nil {
				return Result{}, fmt.Errorf("assign member %d to org %d: %w", m.ID, o.org.ID, err)
			}
			memberOrgCount[m.ID]++
			membershipsCreated++
		}
	}

	unassignedMembers := 0
	for _, m := range members {
		if memberOrgCount[m.ID] == 0 {
			unassignedMembers++
		}
	}

	return Result{
		MembersCreated:       len(members),
		OrganizationsCreated: len(orgs),
		MembershipsCreated:   membershipsCreated,
		UnassignedMembers:    unassignedMembers,
	}, nil
}
