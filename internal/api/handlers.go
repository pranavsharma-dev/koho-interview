package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/pranavsharma-dev/koho-interview/internal/bank"
)

const maxAmountCents = 1_000_000_000_000 // 10^12, per SPEC.md

type Handler struct {
	store *bank.Store
}

func New(store *bank.Store) *Handler {
	return &Handler{store: store}
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(errorBody{Error: errorDetail{Code: code, Message: message}}); err != nil {
		log.Printf("write error response: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json response: %v", err)
	}
}

type createAccountRequest struct {
	StartingBalanceCents int64 `json:"starting_balance_cents"`
}

type accountResponse struct {
	ID           int64 `json:"id"`
	BalanceCents int64 `json:"balance_cents"`
}

// StartingBalanceCents is a plain int64, not a pointer: a missing field decodes
// to 0, which is a valid starting balance, so there is no need to distinguish
// "missing" from "explicitly 0" here (unlike transfer amounts, which must be > 0).
func (h *Handler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	if req.StartingBalanceCents < 0 || req.StartingBalanceCents > maxAmountCents {
		writeError(w, http.StatusBadRequest, "invalid_request", "starting_balance_cents must be between 0 and 10^12")
		return
	}

	id, balance, err := h.store.CreateAccount(r.Context(), req.StartingBalanceCents)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, accountResponse{ID: id, BalanceCents: balance})
}

func (h *Handler) GetAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "account_not_found", "account not found")
		return
	}

	balance, err := h.store.GetAccount(r.Context(), id)
	if errors.Is(err, bank.ErrAccountNotFound) {
		writeError(w, http.StatusNotFound, "account_not_found", "account not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	writeJSON(w, http.StatusOK, accountResponse{ID: id, BalanceCents: balance})
}

type transactionResponse struct {
	TransferID            int64     `json:"transfer_id"`
	Direction             string    `json:"direction"`
	CounterpartyAccountID int64     `json:"counterparty_account_id"`
	AmountCents           int64     `json:"amount_cents"`
	CreatedAt             time.Time `json:"created_at"`
}

func (h *Handler) GetAccountHistory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "account_not_found", "account not found")
		return
	}

	txns, err := h.store.GetTransactionHistory(r.Context(), id)
	if errors.Is(err, bank.ErrAccountNotFound) {
		writeError(w, http.StatusNotFound, "account_not_found", "account not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	resp := make([]transactionResponse, 0, len(txns))
	for _, txn := range txns {
		resp = append(resp, transactionResponse{
			TransferID:            txn.TransferID,
			Direction:             txn.Direction,
			CounterpartyAccountID: txn.CounterpartyAccountID,
			AmountCents:           txn.AmountCents,
			CreatedAt:             txn.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

type transferRequest struct {
	FromAccountID int64 `json:"from_account_id"`
	ToAccountID   int64 `json:"to_account_id"`
	AmountCents   int64 `json:"amount_cents"`
}

type transferResponse struct {
	ID            int64     `json:"id"`
	FromAccountID int64     `json:"from_account_id"`
	ToAccountID   int64     `json:"to_account_id"`
	AmountCents   int64     `json:"amount_cents"`
	CreatedAt     time.Time `json:"created_at"`
}

const maxIdempotencyKeyLen = 255

func (h *Handler) CreateTransfer(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required")
		return
	}
	if len(idempotencyKey) > maxIdempotencyKeyLen {
		writeError(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key must be at most 255 characters")
		return
	}

	var req transferRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	if req.AmountCents <= 0 || req.AmountCents > maxAmountCents {
		writeError(w, http.StatusBadRequest, "invalid_request", "amount_cents must be between 1 and 10^12")
		return
	}
	if req.FromAccountID == req.ToAccountID {
		writeError(w, http.StatusBadRequest, "invalid_request", "from_account_id and to_account_id must differ")
		return
	}

	transfer, replayed, err := h.store.Transfer(r.Context(), idempotencyKey, req.FromAccountID, req.ToAccountID, req.AmountCents)
	switch {
	case errors.Is(err, bank.ErrAccountNotFound):
		writeError(w, http.StatusNotFound, "account_not_found", "account not found")
		return
	case errors.Is(err, bank.ErrInsufficientFunds):
		writeError(w, http.StatusConflict, "insufficient_funds", "insufficient funds")
		return
	case errors.Is(err, bank.ErrKeyReused):
		writeError(w, http.StatusUnprocessableEntity, "idempotency_key_reused", "idempotency key already used with a different request")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, transferResponse{
		ID:            transfer.ID,
		FromAccountID: transfer.FromAccountID,
		ToAccountID:   transfer.ToAccountID,
		AmountCents:   transfer.AmountCents,
		CreatedAt:     transfer.CreatedAt,
	})
}
