package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/devraphaeldiniz/fintech-core-engine/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LedgerRepository struct {
	db *pgxpool.Pool
}

func NewLedgerRepository(db *pgxpool.Pool) *LedgerRepository {
	return &LedgerRepository{db: db}
}

func (r *LedgerRepository) CreateAccount(ctx context.Context, ownerName, accountType string) (*domain.Account, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	query := `
		INSERT INTO accounts (owner_name, account_type, currency)
		VALUES ($1, $2, 'BRL')
		RETURNING id, owner_name, account_type, currency, created_at
	`
	var acc domain.Account
	err = tx.QueryRow(ctx, query, ownerName, accountType).Scan(
		&acc.ID, &acc.OwnerName, &acc.AccountType, &acc.Currency, &acc.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO account_balances (account_id, current_balance_cents, version)
		VALUES ($1, 0, 1)
		ON CONFLICT (account_id) DO NOTHING
	`, acc.ID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &acc, nil
}

func (r *LedgerRepository) GetAccount(ctx context.Context, accountID uuid.UUID) (*domain.Account, error) {
	query := `SELECT id, owner_name, account_type, currency, created_at FROM accounts WHERE id = $1`
	var acc domain.Account
	err := r.db.QueryRow(ctx, query, accountID).Scan(
		&acc.ID, &acc.OwnerName, &acc.AccountType, &acc.Currency, &acc.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAccountNotFound
		}
		return nil, err
	}
	return &acc, nil
}

func (r *LedgerRepository) GetAccountBalance(ctx context.Context, accountID uuid.UUID) (int64, error) {
	var balance int64
	query := `SELECT current_balance_cents FROM account_balances WHERE account_id = $1`
	err := r.db.QueryRow(ctx, query, accountID).Scan(&balance)
	if err == nil {
		return balance, nil
	}

	ledgerQuery := `
		SELECT COALESCE(
			SUM(CASE WHEN entry_type = 'CREDIT' THEN amount_cents ELSE -amount_cents END), 0
		) as balance
		FROM ledger_entries
		WHERE account_id = $1
	`
	err = r.db.QueryRow(ctx, ledgerQuery, accountID).Scan(&balance)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return balance, nil
}

func (r *LedgerRepository) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*domain.Transaction, error) {
	query := `SELECT id, idempotency_key, amount_cents, status, description, created_at FROM transactions WHERE idempotency_key = $1`
	var tx domain.Transaction
	err := r.db.QueryRow(ctx, query, key).Scan(
		&tx.ID, &tx.IdempotencyKey, &tx.AmountCents, &tx.Status, &tx.Description, &tx.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &tx, nil
}

func (r *LedgerRepository) DepositInitialFunds(ctx context.Context, accountID uuid.UUID, amountCents int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	txID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO transactions (id, idempotency_key, amount_cents, status, description)
		VALUES ($1, $2, $3, 'SETTLED', 'Depósito Inicial')
	`, txID, uuid.NewString(), amountCents)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (transaction_id, account_id, entry_type, amount_cents)
		VALUES ($1, $2, 'CREDIT', $3)
	`, txID, accountID, amountCents)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE account_balances
		SET current_balance_cents = current_balance_cents + $1, updated_at = NOW()
		WHERE account_id = $2
	`, amountCents, accountID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *LedgerRepository) TransferAtomic(
	ctx context.Context,
	idempotencyKey string,
	fromID, toID uuid.UUID,
	amountCents int64,
	description string,
) (*domain.Transaction, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Prevenção de Deadlock: ordenação léxica dos IDs para lock pessimista
	firstID, secondID := fromID, toID
	if fromID.String() > toID.String() {
		firstID, secondID = toID, fromID
	}

	var dummy int
	err = tx.QueryRow(ctx, `SELECT 1 FROM account_balances WHERE account_id = $1 FOR UPDATE`, firstID).Scan(&dummy)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `SELECT 1 FROM account_balances WHERE account_id = $1 FOR UPDATE`, secondID).Scan(&dummy)
	if err != nil {
		return nil, err
	}

	var currentBalance int64
	err = tx.QueryRow(ctx, `SELECT current_balance_cents FROM account_balances WHERE account_id = $1`, fromID).Scan(&currentBalance)
	if err != nil {
		return nil, err
	}
	if currentBalance < amountCents {
		return nil, domain.ErrInsufficientFunds
	}

	// 1. Criar transação
	txRecord := domain.Transaction{
		ID:             uuid.New(),
		IdempotencyKey: idempotencyKey,
		AmountCents:    amountCents,
		Status:         "SETTLED",
		Description:    description,
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO transactions (id, idempotency_key, amount_cents, status, description)
		VALUES ($1, $2, $3, $4, $5)
	`, txRecord.ID, txRecord.IdempotencyKey, txRecord.AmountCents, txRecord.Status, txRecord.Description)
	if err != nil {
		return nil, err
	}

	// 2. Double-Entry Bookkeeping
	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (transaction_id, account_id, entry_type, amount_cents)
		VALUES ($1, $2, 'DEBIT', $3), ($1, $4, 'CREDIT', $3)
	`, txRecord.ID, fromID, amountCents, toID)
	if err != nil {
		return nil, err
	}

	// 3. Atualização de Snapshots O(1)
	_, err = tx.Exec(ctx, `UPDATE account_balances SET current_balance_cents = current_balance_cents - $1, updated_at = NOW() WHERE account_id = $2`, amountCents, fromID)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE account_balances SET current_balance_cents = current_balance_cents + $1, updated_at = NOW() WHERE account_id = $2`, amountCents, toID)
	if err != nil {
		return nil, err
	}

	// 4. Transactional Outbox Pattern
	payload, _ := json.Marshal(map[string]interface{}{
		"transaction_id":  txRecord.ID,
		"from_account_id": fromID,
		"to_account_id":   toID,
		"amount_cents":    amountCents,
		"status":          "SETTLED",
	})

	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (event_type, payload, status)
		VALUES ('TRANSFER_COMPLETED', $1, 'PENDING')
	`, payload)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &txRecord, nil
}
