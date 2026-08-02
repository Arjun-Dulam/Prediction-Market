package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/joho/godotenv/autoload"

	"go-backend/internal/auth"
	"go-backend/internal/cache"
	"go-backend/internal/database"
	"go-backend/internal/engine"
	"go-backend/internal/metrics"
	"go-backend/internal/store"
	"go-backend/internal/trading"
	"go-backend/internal/ws"
)

type Server struct {
	port int

	db      database.Service
	engine  engine.Gateway
	trading *trading.Service
	hub     *ws.Hub
	quotes  *cache.RedisQuotes
	auth    *auth.Service
	metrics *metrics.Registry
}

func NewServer() *http.Server {
	port, _ := strconv.Atoi(os.Getenv("PORT"))
	if port == 0 {
		port = 8080
	}
	matchingEngine, err := engine.New(os.Getenv("ENGINE_ADDR"))
	if err != nil {
		matchingEngine, _ = engine.New("localhost:50051")
	}

	db := database.New()
	projector := store.NewProjector(db.DB())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := projector.Migrate(ctx); err != nil {
		panic(fmt.Sprintf("migrate database: %v", err))
	}
	authService, err := auth.New(db.DB(), envOr("JWT_SECRET", "development-secret-change-me"))
	if err != nil {
		panic(fmt.Sprintf("initialize authentication: %v", err))
	}
	batchSize := 32
	if configured := os.Getenv("ORDER_BATCH_SIZE"); configured != "" {
		value, err := strconv.Atoi(configured)
		if err != nil || value < 1 || value > 32 {
			panic("ORDER_BATCH_SIZE must be between 1 and 32")
		}
		batchSize = value
	}
	registry := metrics.New()
	durable, err := trading.NewWithConfig(trading.Config{
		WALPath: "data/orders.wal", SnapshotPath: "data/orderbook.snapshot",
		Engine: engine.TradingAdapter{Client: matchingEngine}, Projector: projector, Sync: true, Observe: registry.Observe, OrderBatchSize: batchSize,
	})
	if err != nil {
		panic(fmt.Sprintf("initialize durable state: %v", err))
	}
	// Replay may be longer than the earlier migration deadline. Give engine
	// reconstruction its own timeout after durable recovery has finished.
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer recoveryCancel()
	if err := durable.RecoverEngine(recoveryCtx); err != nil {
		_ = durable.Close()
		panic(fmt.Sprintf("recover matching engine state: %v", err))
	}
	api := &Server{
		port: port,

		db:      db,
		engine:  matchingEngine,
		trading: durable,
		hub:     ws.NewHub(),
		quotes:  cache.NewRedisQuotes(envOr("REDIS_ADDR", "localhost:6379")),
		auth:    authService,
		metrics: registry,
	}
	snapshotStop := make(chan struct{})
	snapshotDone := make(chan struct{})
	go func() {
		defer close(snapshotDone)
		interval := 30 * time.Second
		if configured := os.Getenv("SNAPSHOT_INTERVAL"); configured != "" {
			if parsed, parseErr := time.ParseDuration(configured); parseErr == nil && parsed > 0 {
				interval = parsed
			}
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := durable.Snapshot(); err != nil {
					log.Printf("periodic snapshot: %v", err)
				} else {
					api.metrics.Snapshot()
				}
			case <-snapshotStop:
				return
			}
		}
	}()

	// Declare Server config
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", api.port),
		Handler:      api.RegisterRoutes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	server.RegisterOnShutdown(func() {
		close(snapshotStop)
		<-snapshotDone
		if err := durable.Snapshot(); err != nil {
			log.Printf("snapshot durable state: %v", err)
		}
		if err := durable.Close(); err != nil {
			log.Printf("close durable state: %v", err)
		}
		if err := api.quotes.Close(); err != nil {
			log.Printf("close Redis: %v", err)
		}
		if err := matchingEngine.Close(); err != nil {
			log.Printf("close matching engine: %v", err)
		}
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	})

	return server
}

// NewHandler exposes the HTTP surface with an injected engine dependency for
// tests and load benchmarks.
func NewHandler(matchingEngine engine.Gateway) http.Handler {
	return (&Server{engine: matchingEngine, hub: ws.NewHub(), metrics: metrics.New()}).registerRoutes(false)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
