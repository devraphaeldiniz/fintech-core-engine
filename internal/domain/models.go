package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInsufficientFunds   = errors.New("saldo insuficiente")
	ErrAccountNotFound     = errors.New("conta não encontrada")
	ErrSameAccountTransfer = errors.New("conta de origem e destino não podem ser iguais")
	ErrIdempotencyConflict = errors.New("transação com esta chave de idempotência já processada")
	ErrLockFailed          = errors.New("conta temporariamente bloqueada por outra transação, tente novamente")
)

type Account struct {
	ID          uuid.UUID `json:"id"`
	OwnerName   string    `json:"owner_name"`
	AccountType string    `json:"account_type"` // 'CLIENT', 'MERCHANT'
	Currency    string    `json:"currency"`
	CreatedAt   time.Time `json:"created_at"`
}

type Transaction struct {
	ID             uuid.UUID `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	AmountCents    int64     `json:"amount_cents"`
	Status         string    `json:"status"` // 'SETTLED', 'FAILED'
	Description    string    `json:"description"`
	CreatedAt      time.Time `json:"created_at"`
}

type LedgerEntry struct {
	ID            uuid.UUID `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	EntryType     string    `json:"entry_type"` // 'DEBIT' ou 'CREDIT'
	AmountCents   int64     `json:"amount_cents"`
	CreatedAt     time.Time `json:"created_at"`
}

type AccountBalance struct {
	AccountID   uuid.UUID `json:"account_id"`
	BalanceCents int64    `json:"balance_cents"`
	Currency    string    `json:"currency"`
}
