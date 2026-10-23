package domain_test

import (
	"testing"

	"github.com/devraphaeldiniz/fintech-core-engine/internal/domain"
	"github.com/google/uuid"
)

func TestDomainErrors(t *testing.T) {
	if domain.ErrInsufficientFunds.Error() != "saldo insuficiente" {
		t.Errorf("mensagem de erro inesperada: %v", domain.ErrInsufficientFunds)
	}

	if domain.ErrSameAccountTransfer.Error() != "conta de origem e destino não podem ser iguais" {
		t.Errorf("mensagem de erro inesperada: %v", domain.ErrSameAccountTransfer)
	}
}

func TestAccountCreation(t *testing.T) {
	accID := uuid.New()
	acc := domain.Account{
		ID:          accID,
		OwnerName:   "Raphael Diniz",
		AccountType: "CLIENT",
		Currency:    "BRL",
	}

	if acc.ID != accID {
		t.Fatalf("esperava ID %s, obteve %s", accID, acc.ID)
	}
	if acc.Currency != "BRL" {
		t.Fatalf("esperava moeda BRL, obteve %s", acc.Currency)
	}
}
