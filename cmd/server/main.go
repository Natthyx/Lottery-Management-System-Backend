package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/Natthyx/lottery-system/internal/auth"
	"github.com/Natthyx/lottery-system/internal/booking"
	"github.com/Natthyx/lottery-system/internal/config"
	"github.com/Natthyx/lottery-system/internal/db"
	"github.com/Natthyx/lottery-system/internal/event"
	"github.com/Natthyx/lottery-system/internal/lottery"
	"github.com/Natthyx/lottery-system/internal/middleware"
)

func main() {
	if err := run(); err != nil {
		log.Fatal().Err(err).Msg("server exited with error")
	}
}

func run() error {
	configureLogger()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// ── Datastores ─────────────────────────────────────────
	pgPool, err := db.NewPool(rootCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pgPool.Close()

	if cfg.MigrateOnStart {
		if err := db.RunMigrations(rootCtx, pgPool); err != nil {
			return err
		}
	}

	redisClient, err := db.NewRedis(rootCtx, cfg.RedisOptions)
	if err != nil {
		return err
	}
	defer redisClient.Close()

	// ── Services ───────────────────────────────────────────
	authSvc := auth.NewService(pgPool, cfg.JWTSecret, cfg.JWTExpiry)
	eventSvc := event.NewService(pgPool)
	bookingSvc := booking.NewService(pgPool, redisClient, cfg.LockTTL)
	lotterySvc := lottery.NewService(pgPool)

	if cfg.BootstrapAdminEmail != "" && cfg.BootstrapAdminPassword != "" {
		if err := authSvc.BootstrapAdmin(rootCtx,
			cfg.BootstrapAdminEmail,
			cfg.BootstrapAdminPassword,
			cfg.BootstrapAdminName,
		); err != nil {
			return err
		}
		log.Info().Str("email", cfg.BootstrapAdminEmail).Msg("bootstrap admin ensured")
	}

	// ── Handlers ───────────────────────────────────────────
	authHandler := auth.NewHandler(authSvc)
	eventHandler := event.NewHandler(eventSvc)
	bookingHandler := booking.NewHandler(bookingSvc)
	lotteryHandler := lottery.NewHandler(lotterySvc)

	// ── Router ─────────────────────────────────────────────
	r := chi.NewRouter()

	// Order matters. Logger must be outermost so a panic inside Recoverer
	// is still logged as a completed request.
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(chiMiddleware.RealIP)
	r.Use(chiMiddleware.RequestID)
	r.Use(middleware.BodyLimit(cfg.MaxRequestBytes))
	r.Use(chiMiddleware.Timeout(30 * time.Second))
	if len(cfg.CORSAllowedOrigins) > 0 {
		r.Use(middleware.CORS(cfg.CORSAllowedOrigins))
	}
	r.Use(httprate.LimitByIP(cfg.APILimitRequests, cfg.APILimitWindow))

	// ── Health & readiness ─────────────────────────────────
	r.Get("/health", livenessHandler())
	r.Get("/ready", readinessHandler(pgPool, redisClient))

	// ── Public routes ──────────────────────────────────────
	r.Group(func(r chi.Router) {
		// Stricter rate limit specifically for auth endpoints — protects
		// against credential brute-force without throttling normal API use.
		r.Group(func(r chi.Router) {
			r.Use(httprate.LimitByIP(cfg.AuthLimitRequests, cfg.AuthLimitWindow))
			r.Post("/auth/register", authHandler.Register)
			r.Post("/auth/login", authHandler.Login)
		})

		r.Get("/events", eventHandler.List)
		r.Get("/events/{id}", eventHandler.GetByID)
		r.Get("/events/{eventID}/results", lotteryHandler.Results)
	})

	// ── Authenticated routes ───────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(cfg.JWTSecret))

		r.Get("/auth/me", authHandler.Me)
		r.Post("/events/{eventID}/book", bookingHandler.Book)
		r.Get("/me/bookings", bookingHandler.MyBookings)

		// ── Admin-only ──────────────────────────────────────
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAdmin)

			r.Post("/events", eventHandler.Create)
			r.Put("/events/{id}/close", eventHandler.Close)
			r.Put("/events/{id}/cancel", eventHandler.Cancel)
			r.Get("/events/{eventID}/bookings", bookingHandler.ListByEvent)
			r.Post("/events/{eventID}/draw", lotteryHandler.Draw)
			r.Post("/admin/users/{id}/promote", authHandler.PromoteToAdmin)
		})
	})

	// ── HTTP server ────────────────────────────────────────
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info().Str("port", cfg.Port).Str("env", cfg.Env).Msg("HTTP server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// ── Wait for shutdown signal or server failure ─────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		log.Info().Str("signal", sig.String()).Msg("shutdown signal received")
	case err := <-serverErr:
		log.Error().Err(err).Msg("server failed")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("forced shutdown")
		return err
	}
	log.Info().Msg("server stopped cleanly")
	return nil
}

func configureLogger() {
	if os.Getenv("PRETTY") == "true" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	} else {
		zerolog.TimeFieldFormat = time.RFC3339Nano
	}
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}
