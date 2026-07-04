// Package httpapi is the inbound (driving) HTTP adapter. It translates between
// HTTP and the use cases and maps domain errors to status codes — it contains no
// business rules. Handlers log through the request-scoped, correlated logger
// (srog.Ctx) but never construct or pass a logger explicitly.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/fullexample/internal/app"
	"github.com/dvislobokov/srog/fullexample/internal/domain"
)

// OrderUseCases is the port the handler depends on; *app.OrderService satisfies
// it. Declaring the interface here (consumer side) keeps the adapter decoupled
// from the concrete service.
type OrderUseCases interface {
	CreateOrder(ctx context.Context, in app.CreateOrderInput) (domain.Order, error)
	GetOrder(ctx context.Context, id string) (domain.Order, error)
}

type Handler struct {
	orders OrderUseCases
}

func NewHandler(orders OrderUseCases) *Handler {
	return &Handler{orders: orders}
}

func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	var req createOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	order, err := h.orders.CreateOrder(r.Context(), app.CreateOrderInput{
		Customer:    req.Customer,
		AmountCents: req.AmountCents,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toResponse(order))
}

func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	order, err := h.orders.GetOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toResponse(order))
}

// writeDomainError maps domain errors to HTTP status codes — the one place that
// knows both vocabularies.
func writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, r, http.StatusNotFound, err)
	case errors.Is(err, domain.ErrInvalidCustomer), errors.Is(err, domain.ErrInvalidAmount):
		writeError(w, r, http.StatusBadRequest, err)
	default:
		writeError(w, r, http.StatusInternalServerError, err)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, code int, err error) {
	srog.WarningCtx(r.Context(), "request failed: {Error} ({Status})", err.Error(), code)
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
