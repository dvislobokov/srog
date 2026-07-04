package httpapi

import "github.com/dvislobokov/srog/fullexample/internal/domain"

// Wire models kept separate from domain entities: the transport can evolve
// (field names, versions) without touching the domain.

type createOrderRequest struct {
	Customer    string `json:"customer"`
	AmountCents int64  `json:"amount_cents"`
}

type orderResponse struct {
	ID          string `json:"id"`
	Customer    string `json:"customer"`
	AmountCents int64  `json:"amount_cents"`
	Status      string `json:"status"`
}

func toResponse(o domain.Order) orderResponse {
	return orderResponse{
		ID:          o.ID,
		Customer:    o.Customer,
		AmountCents: o.AmountCents,
		Status:      string(o.Status),
	}
}
