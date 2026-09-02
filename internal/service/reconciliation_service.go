package service

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditDiscrepancy struct {
	AccountID            uuid.UUID `json:"account_id"`
	SnapshotBalanceCents int64     `json:"snapshot_balance_cents"`
	LedgerSumCents       int64     `json:"ledger_sum_cents"`
	DiscrepancyCents     int64     `json:"discrepancy_cents"`
}

type ReconciliationService struct {
	db *pgxpool.Pool
}

func NewReconciliationService(db *pgxpool.Pool) *ReconciliationService {
	return &ReconciliationService{db: db}
}

// RunFullAudit audita todas as contas cadastradas de forma atômica
func (s *ReconciliationService) RunFullAudit(ctx context.Context) ([]AuditDiscrepancy, error) {
	query := `
		SELECT 
			a.id,
			COALESCE(ab.current_balance_cents, 0) AS snapshot_balance,
			COALESCE(
				SUM(CASE WHEN le.entry_type = 'CREDIT' THEN le.amount_cents ELSE -le.amount_cents END), 0
			) AS ledger_sum
		FROM accounts a
		LEFT JOIN account_balances ab ON ab.account_id = a.id
		LEFT JOIN ledger_entries le ON le.account_id = a.id
		GROUP BY a.id, ab.current_balance_cents
	`

	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("falha ao consultar balanços: %w", err)
	}
	defer rows.Close()

	var discrepancies []AuditDiscrepancy

	for rows.Next() {
		var (
			accountID uuid.UUID
			snapshot  int64
			ledgerSum int64
		)
		if err := rows.Scan(&accountID, &snapshot, &ledgerSum); err != nil {
			return nil, err
		}

		diff := snapshot - ledgerSum
		if diff != 0 {
			discrepancies = append(discrepancies, AuditDiscrepancy{
				AccountID:            accountID,
				SnapshotBalanceCents: snapshot,
				LedgerSumCents:       ledgerSum,
				DiscrepancyCents:     diff,
			})

			// Grava discrepância para investigação do time de risco financeiro
			_, _ = s.db.Exec(ctx, `
				INSERT INTO audit_discrepancies (account_id, snapshot_balance_cents, ledger_sum_cents, discrepancy_cents)
				VALUES ($1, $2, $3, $4)
			`, accountID, snapshot, ledgerSum, diff)

			log.Printf("🚨 ALERTA CRÍTICO: Discrepância na conta %s! Snapshot: %d | Ledger: %d | Dif: %d\n",
				accountID, snapshot, ledgerSum, diff)
		}
	}

	return discrepancies, nil
}
