package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/devraphaeldiniz/fintech-core-engine/internal/domain"
	platformRedis "github.com/devraphaeldiniz/fintech-core-engine/internal/platform/redis"
	"github.com/devraphaeldiniz/fintech-core-engine/internal/platform/telemetry"
	"github.com/devraphaeldiniz/fintech-core-engine/internal/repository"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type TransferService struct {
	repo  *repository.LedgerRepository
	redis *redis.Client
}

func NewTransferService(repo *repository.LedgerRepository, rdb *redis.Client) *TransferService {
	return &TransferService{
		repo:  repo,
		redis: rdb,
	}
}

func (s *TransferService) CreateAccount(ctx context.Context, ownerName, accountType string, initialBalanceCents int64) (*domain.Account, error) {
	acc, err := s.repo.CreateAccount(ctx, ownerName, accountType)
	if err != nil {
		return nil, err
	}

	if initialBalanceCents > 0 {
		if err := s.repo.DepositInitialFunds(ctx, acc.ID, initialBalanceCents); err != nil {
			return nil, err
		}
		telemetry.LedgerEntriesTotal.WithLabelValues("CREDIT").Inc()
	}
	return acc, nil
}

func (s *TransferService) GetBalance(ctx context.Context, accountID uuid.UUID) (*domain.AccountBalance, error) {
	acc, err := s.repo.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}

	balance, err := s.repo.GetAccountBalance(ctx, accountID)
	if err != nil {
		return nil, err
	}

	return &domain.AccountBalance{
		AccountID:    acc.ID,
		BalanceCents: balance,
		Currency:     acc.Currency,
	}, nil
}

func (s *TransferService) ExecuteTransfer(
	ctx context.Context,
	idempotencyKey string,
	fromID, toID uuid.UUID,
	amountCents int64,
	description string,
) (*domain.Transaction, error) {
	start := time.Now()
	defer func() {
		telemetry.TransferDuration.Observe(time.Since(start).Seconds())
	}()

	if fromID == toID {
		telemetry.TransfersTotal.WithLabelValues("failed", "same_account").Inc()
		return nil, domain.ErrSameAccountTransfer
	}
	if amountCents <= 0 {
		telemetry.TransfersTotal.WithLabelValues("failed", "invalid_amount").Inc()
		return nil, fmt.Errorf("valor deve ser maior que zero")
	}

	// 1. Verificacao de Idempotencia
	existing, err := s.repo.GetTransactionByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		telemetry.TransfersTotal.WithLabelValues("success", "idempotent_replay").Inc()
		return existing, nil
	}

	// 2. Lock Distribuido no Redis
	lock := platformRedis.NewLock(s.redis, "account:"+fromID.String(), 5*time.Second)
	acquired, err := lock.AcquireWithRetry(ctx, 3*time.Second)
	if err != nil || !acquired {
		telemetry.TransfersTotal.WithLabelValues("rejected", "lock_contention").Inc()
		return nil, domain.ErrLockFailed
	}

	telemetry.DistributedLocksActive.Inc()
	defer func() {
		_ = lock.Release(ctx)
		telemetry.DistributedLocksActive.Dec()
	}()

	// 3. Validacao da conta destino
	if _, err := s.repo.GetAccount(ctx, toID); err != nil {
		telemetry.TransfersTotal.WithLabelValues("failed", "destination_not_found").Inc()
		return nil, fmt.Errorf("conta de destino invalida: %w", err)
	}

	// 4. Execucao atomica no Postgres
	tx, err := s.repo.TransferAtomic(ctx, idempotencyKey, fromID, toID, amountCents, description)
	if err != nil {
		if errors.Is(err, domain.ErrInsufficientFunds) {
			telemetry.TransfersTotal.WithLabelValues("rejected", "insufficient_funds").Inc()
		} else {
			telemetry.TransfersTotal.WithLabelValues("failed", "db_error").Inc()
		}
		return nil, err
	}

	telemetry.TransfersTotal.WithLabelValues("success", "settled").Inc()
	telemetry.LedgerEntriesTotal.WithLabelValues("DEBIT").Inc()
	telemetry.LedgerEntriesTotal.WithLabelValues("CREDIT").Inc()

	return tx, nil
}
