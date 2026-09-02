package handler

import (
	"encoding/json"
	"net/http"

	"github.com/devraphaeldiniz/fintech-core-engine/internal/domain"
	"github.com/devraphaeldiniz/fintech-core-engine/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type HTTPHandler struct {
	svc *service.TransferService
}

func NewHTTPHandler(svc *service.TransferService) *HTTPHandler {
	return &HTTPHandler{svc: svc}
}

type CreateAccountRequest struct {
	OwnerName           string `json:"owner_name"`
	AccountType         string `json:"account_type"`
	InitialBalanceCents int64  `json:"initial_balance_cents"`
}

func (h *HTTPHandler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	var req CreateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Payload inválido")
		return
	}

	acc, err := h.svc.CreateAccount(r.Context(), req.OwnerName, req.AccountType, req.InitialBalanceCents)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, acc)
}

func (h *HTTPHandler) GetBalance(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	accountID, err := uuid.Parse(idStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "ID de conta inválido")
		return
	}

	balance, err := h.svc.GetBalance(r.Context(), accountID)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, balance)
}

type TransferRequest struct {
	FromAccountID uuid.UUID `json:"from_account_id"`
	ToAccountID   uuid.UUID `json:"to_account_id"`
	AmountCents   int64     `json:"amount_cents"`
	Description   string    `json:"description"`
}

func (h *HTTPHandler) Transfer(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := r.Header.Get("X-Idempotency-Key")
	if idempotencyKey == "" {
		respondError(w, http.StatusBadRequest, "Header 'X-Idempotency-Key' é obrigatório")
		return
	}

	var req TransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Payload inválido")
		return
	}

	tx, err := h.svc.ExecuteTransfer(
		r.Context(),
		idempotencyKey,
		req.FromAccountID,
		req.ToAccountID,
		req.AmountCents,
		req.Description,
	)
	if err != nil {
		if err == domain.ErrInsufficientFunds || err == domain.ErrSameAccountTransfer {
			respondError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if err == domain.ErrLockFailed {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, tx)
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
