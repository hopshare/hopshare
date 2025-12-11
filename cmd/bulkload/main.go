package main

import (
	"context"
	"flag"
	"log"
	"time"

	_ "github.com/lib/pq"

	"hopshare/internal/config"
	"hopshare/internal/database"
	"hopshare/internal/database/bulkload"
	"hopshare/internal/database/migrate"
)

func main() {
	var memberCount int
	var orgCount int
	flag.IntVar(&memberCount, "members", 0, "number of members to generate")
	flag.IntVar(&orgCount, "orgs", 0, "number of organizations to generate")
	flag.Parse()

	if memberCount <= 0 {
		log.Fatalf("members must be greater than zero")
	}
	if orgCount < 0 {
		log.Fatalf("orgs must be zero or greater")
	}

	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer db.Close()

	if err := migrate.Run(ctx, db); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}

	result, err := bulkload.Load(ctx, db, memberCount, orgCount)
	if err != nil {
		log.Fatalf("bulk load failed: %v", err)
	}

	log.Printf("bulk load complete: members=%d orgs=%d memberships=%d unassigned_members=%d",
		result.MembersCreated, result.OrganizationsCreated, result.MembershipsCreated, result.UnassignedMembers)
}
