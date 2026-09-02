package worker

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PartitionWorker struct {
	db *pgxpool.Pool
}

func NewPartitionWorker(db *pgxpool.Pool) *PartitionWorker {
	return &PartitionWorker{db: db}
}

// Start roda a manutenção de partições imediatamente na inicialização e depois a cada 24 horas
func (w *PartitionWorker) Start(ctx context.Context) {
	log.Println("✓ Partition Maintenance Worker iniciado")

	// Executa uma vez no startup
	w.maintain(ctx)

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Partition Maintenance Worker finalizado.")
			return
		case <-ticker.C:
			w.maintain(ctx)
		}
	}
}

func (w *PartitionWorker) maintain(ctx context.Context) {
	_, err := w.db.Exec(ctx, "SELECT maintain_ledger_partitions(12);")
	if err != nil {
		log.Printf("⚠️ Erro na manutenção automática de partições: %v\n", err)
		return
	}
	log.Println("✓ Partições do ledger verificadas e provisionadas para os próximos 12 meses")
}
