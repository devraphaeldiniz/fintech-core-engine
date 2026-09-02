package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	TransfersTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fintech_transfers_total",
			Help: "Total de requisicoes de transferencia processadas pela engine",
		},
		[]string{"status", "reason"},
	)

	TransferDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "fintech_transfer_duration_seconds",
			Help:    "Latencia do ciclo transacional atomico da transferencia",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5},
		},
	)

	LedgerEntriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fintech_ledger_entries_total",
			Help: "Total de lancamentos contabeis registrados no ledger imutavel",
		},
		[]string{"entry_type"},
	)

	DistributedLocksActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "fintech_active_distributed_locks",
			Help: "Numero de locks distribuidos ativos no Redis simultaneamente",
		},
	)
)
