package main

import (
	"context"
	"log"
	"time"

	_ "github.com/lib/pq"

	"hopshare/internal/config"
	"hopshare/internal/database"
	"hopshare/internal/database/migrate"
)

func main() {
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer db.Close()

	if err := migrate.Run(ctx, db); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}

	log.Println("migrations up to date")
}
