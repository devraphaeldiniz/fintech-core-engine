package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/devraphaeldiniz/fintech-core-engine/internal/handler"
	appMiddleware "github.com/devraphaeldiniz/fintech-core-engine/internal/middleware"
	"github.com/devraphaeldiniz/fintech-core-engine/internal/repository"
	"github.com/devraphaeldiniz/fintech-core-engine/internal/service"
	"github.com/devraphaeldiniz/fintech-core-engine/internal/worker"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbURL := getEnv("DB_URL", "postgres://fintech_user:fintech_secret_password@127.0.0.1:5433/fintech_db?sslmode=disable")
	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Erro ao conectar no PostgreSQL: %v\n", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		log.Fatalf("Falha de ping no PostgreSQL: %v\n", err)
	}
	log.Println("✓ PostgreSQL conectado com sucesso")

	redisAddr := getEnv("REDIS_URL", "127.0.0.1:6379")
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Falha de ping no Redis: %v\n", err)
	}
	log.Println("✓ Redis conectado com sucesso")

	// Workers em Background
	outboxWorker := worker.NewOutboxWorker(dbPool)
	go outboxWorker.Start(ctx)

	partitionWorker := worker.NewPartitionWorker(dbPool)
	go partitionWorker.Start(ctx)

	repo := repository.NewLedgerRepository(dbPool)
	svc := service.NewTransferService(repo, rdb)
	reconcileSvc := service.NewReconciliationService(dbPool)
	h := handler.NewHTTPHandler(svc)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Idempotency-Key", "X-API-Key"},
		AllowCredentials: true,
	}))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "healthy",
			"engine":  "enterprise-core-ledger",
			"version": "2.3.0",
		})
	})
	r.Handle("/metrics", promhttp.Handler())

	r.Group(func(protected chi.Router) {
		protected.Use(appMiddleware.APIKeyAuth(dbPool, "accounts:write"))
		protected.Post("/accounts", h.CreateAccount)
	})

	r.Group(func(protected chi.Router) {
		protected.Use(appMiddleware.APIKeyAuth(dbPool, "accounts:read"))
		protected.Get("/accounts/{id}/balance", h.GetBalance)
	})

	r.Group(func(protected chi.Router) {
		protected.Use(appMiddleware.APIKeyAuth(dbPool, "transfers:write"))
		protected.Post("/transfers", h.Transfer)
	})

	r.Group(func(admin chi.Router) {
		admin.Use(appMiddleware.APIKeyAuth(dbPool, "admin"))
		admin.Post("/admin/reconciliation", func(w http.ResponseWriter, r *http.Request) {
			discrepancies, err := reconcileSvc.RunFullAudit(r.Context())
			w.Header().Set("Content-Type", "application/json")
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}

			status := "ALL_ACCOUNTS_BALANCED"
			if len(discrepancies) > 0 {
				status = "DISCREPANCIES_DETECTED"
			}

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"audit_status":        status,
				"discrepancies_count": len(discrepancies),
				"discrepancies":       discrepancies,
				"audited_at":          time.Now().Format(time.RFC3339),
			})
		})
	})

	server := &http.Server{
		Addr:         ":8080",
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("🚀 Fintech Core Engine v2.3 rodando na porta :8080 (Docker)\n")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Erro no servidor: %v\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("Encerrando aplicação...")
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Erro no encerramento: %v\n", err)
	}
	log.Println("Aplicação finalizada.")
}
