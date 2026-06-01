package main

import (
	"context"
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

	"github.com/natannan/lottery-system/internal/auth"
	"github.com/natannan/lottery-system/internal/booking"
	"github.com/natannan/lottery-system/internal/config"
	"github.com/natannan/lottery-system/internal/db"
	"github.com/natannan/lottery-system/internal/event"
	"github.com/natannan/lottery-system/internal/lottery"
	"github.com/natannan/lottery-system/internal/middleware"
)

func main() {
	// Structured JSON logging — easy to ingest into Datadog / Loki / CloudWatch
	if os.Getenv("PRETTY") == "true" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	} else {
		zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	}
	log.Info().Msg("starting lottery system")

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	ctx := context.Background()

	pgPool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to PostgreSQL")
	}
	defer pgPool.Close()

	redisClient, err := db.NewRedis(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to Redis")
	}
	defer redisClient.Close()

	// Services
	authSvc    := auth.NewService(pgPool, cfg.JWTSecret, cfg.JWTExpiry)
	eventSvc   := event.NewService(pgPool)
	bookingSvc := booking.NewService(pgPool, redisClient, cfg.LockTTL)
	lotterySvc := lottery.NewService(pgPool)

	// Handlers
	authHandler    := auth.NewHandler(authSvc)
	eventHandler   := event.NewHandler(eventSvc)
	bookingHandler := booking.NewHandler(bookingSvc)
	lotteryHandler := lottery.NewHandler(lotterySvc)

	// Router
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Use(chiMiddleware.RealIP)
	r.Use(chiMiddleware.RequestID)
	r.Use(httprate.LimitByIP(100, 1*time.Minute))
	r.Use(chiMiddleware.Timeout(30 * time.Second))

	// Public routes
	r.Group(func(r chi.Router) {
		r.Route("/auth", authHandler.Routes())
		r.Get("/events", eventHandler.List)
		r.Get("/events/{id}", eventHandler.GetByID)
		r.Get("/events/{eventID}/results", lotteryHandler.Results)
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		})
	})

	// Authenticated routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(cfg.JWTSecret))

		r.Post("/events/{eventID}/book", bookingHandler.Book)
		r.Get("/me/bookings", bookingHandler.MyBookings)

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAdmin)
			r.Post("/events", eventHandler.Create)
			r.Post("/events/{eventID}/draw", lotteryHandler.Draw)
		})
	})

	// Graceful shutdown: wait for in-flight requests on SIGTERM (Kubernetes)
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info().Str("port", cfg.Port).Msg("HTTP server listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutdown signal received, draining connections...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("forced shutdown")
	}
	log.Info().Msg("server stopped cleanly")
}
