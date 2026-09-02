package worker

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OutboxWorker struct {
	db *pgxpool.Pool
}

func NewOutboxWorker(db *pgxpool.Pool) *OutboxWorker {
	return &OutboxWorker{db: db}
}

func (w *OutboxWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	log.Println("✓ Outbox Event Worker iniciado (Processamento Assíncrono Ativo)")

	for {
		select {
		case <-ctx.Done():
			log.Println("Encerrando Outbox Worker...")
			return
		case <-ticker.C:
			w.processPendingEvents(ctx)
		}
	}
}

func (w *OutboxWorker) processPendingEvents(ctx context.Context) {
	query := `
		SELECT id, event_type, payload
		FROM outbox_events
		WHERE status = 'PENDING'
		ORDER BY created_at ASC
		LIMIT 10
		FOR UPDATE SKIP LOCKED
	`

	rows, err := w.db.Query(ctx, query)
	if err != nil {
		return
	}
	defer rows.Close()

	type Event struct {
		ID        uuid.UUID
		EventType string
		Payload   []byte
	}

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.EventType, &e.Payload); err == nil {
			events = append(events, e)
		}
	}

	for _, e := range events {
		log.Printf("[OUTBOX WORKER] Evento disparado: %s | ID: %s\n", e.EventType, e.ID)

		_, _ = w.db.Exec(ctx, `
			UPDATE outbox_events
			SET status = 'PROCESSED', processed_at = NOW()
			WHERE id = $1
		`, e.ID)
	}
}
