package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

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
