package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"hopshare/internal/auth"
	"hopshare/internal/config"
	"hopshare/internal/database"
	"hopshare/internal/database/migrate"
	httpserver "hopshare/internal/http"
	"hopshare/internal/worker"
	"hopshare/web/templates"
)

func main() {
	// Load configuration from environment.
	cfg := config.Load()
	if err := templates.SetAppTimezone(cfg.Timezone); err != nil {
		log.Fatalf("configure app timezone: %v", err)
	}
	appLocation, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		log.Fatalf("load app timezone: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer db.Close()

	if err := migrate.Run(ctx, db); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}

	var passwordResetEmailSender httpserver.PasswordResetEmailSender
	if cfg.FeatureEmail {
		passwordResetEmailSender, err = httpserver.NewMailgunPasswordResetEmailSender(httpserver.MailgunPasswordResetEmailSenderConfig{
			APIBaseURL:  cfg.MailgunAPIBaseURL,
			Domain:      cfg.MailgunDomain,
			APIKey:      cfg.MailgunAPIKey,
			FromAddress: cfg.MailgunFromAddress,
		})
		if err != nil {
			log.Fatalf("configure password reset email sender: %v", err)
		}
	}

	sessionManager, err := auth.NewPostgresSessionManager(db, auth.SessionManagerConfig{
		AbsoluteTTL: cfg.SessionAbsoluteTTL,
		IdleTimeout: cfg.SessionIdleTimeout,
	})
	if err != nil {
		log.Fatalf("configure session manager: %v", err)
	}

	handler := httpserver.NewRouterWithOptions(db, httpserver.RouterOptions{
		Sessions:                 sessionManager,
		AdminEmails:              cfg.AdminEmails,
		FeatureEmail:             &cfg.FeatureEmail,
		FeatureHopPictures:       &cfg.FeatureHopPictures,
		AvatarImageMaxBytes:      &cfg.AvatarImageSize,
		PublicBaseURL:            cfg.PublicBaseURL,
		PasswordResetEmailSender: passwordResetEmailSender,
		CookieSecure:             &cfg.CookieSecure,
		AppLocation:              appLocation,
	})

	if cfg.WorkersEnabled {
		backgroundRunner, err := worker.NewRunner(db, cfg.WorkerPollInterval, worker.DefaultJobs(worker.JobConfig{
			ExpireDueHopsInterval:         cfg.ExpireHopsInterval,
			DeleteExpiredSessionsInterval: cfg.SessionGCInterval,
			ExpireNotificationAge:         cfg.ExpireNotificationAge,
			ExpireNotificationInterval:    cfg.ExpireNotificationInterval,
		}))
		if err != nil {
			log.Fatalf("configure background workers: %v", err)
		}
		go func() {
			if err := backgroundRunner.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("background workers stopped: %v", err)
			}
		}()
	}

	server := &http.Server{
		Addr:         cfg.Addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}
