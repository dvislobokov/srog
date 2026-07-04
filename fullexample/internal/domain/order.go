// Package domain holds the enterprise entities and the ports (interfaces) that
// outer layers implement. It is the center of the architecture: it imports no
// other layer and no infrastructure (no logging, tracing, HTTP, or database) —
// all dependencies point inward, toward this package.
package domain

import "errors"

// Status is an order's lifecycle state.
type Status string

const (
	StatusPending Status = "pending"
	StatusPaid    Status = "paid"
)

// Order is the core entity.
type Order struct {
	ID          string
	Customer    string
	AmountCents int64
	Status      Status
}

// Domain errors. Adapters map these to transport-specific results (e.g. HTTP
// status codes) without the domain knowing anything about transports.
var (
	ErrNotFound        = errors.New("order not found")
	ErrInvalidCustomer = errors.New("customer must not be empty")
	ErrInvalidAmount   = errors.New("amount must be positive")
)

// NewOrder validates the business invariants and constructs a pending order.
// Keeping validation here (not in handlers) means every entry point enforces it.
func NewOrder(id, customer string, amountCents int64) (Order, error) {
	if customer == "" {
		return Order{}, ErrInvalidCustomer
	}
	if amountCents <= 0 {
		return Order{}, ErrInvalidAmount
	}
	return Order{ID: id, Customer: customer, AmountCents: amountCents, Status: StatusPending}, nil
}
