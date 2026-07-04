package domain

import "context"

// OrderRepository is a port: the domain declares the persistence contract, and
// an outer adapter (memrepo, or a future Postgres repo) implements it. This is
// dependency inversion — the database depends on the domain, never the reverse.
type OrderRepository interface {
	Save(ctx context.Context, o Order) error
	FindByID(ctx context.Context, id string) (Order, error)
}
