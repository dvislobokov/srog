// Package memrepo is an in-memory OrderRepository — an outbound (driven) adapter
// implementing the domain port. Replacing it with Postgres would touch only this
// package and one line in the composition root; the domain and use cases are
// unaffected.
package memrepo

import (
	"context"
	"sync"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/fullexample/internal/domain"
)

// Repo is a concurrency-safe in-memory store.
type Repo struct {
	mu     sync.RWMutex
	orders map[string]domain.Order
}

func New() *Repo {
	return &Repo{orders: make(map[string]domain.Order)}
}

func (r *Repo) Save(ctx context.Context, o domain.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.orders[o.ID] = o
	srog.DebugCtx(ctx, "persisted order {OrderId}", o.ID)
	return nil
}

func (r *Repo) FindByID(ctx context.Context, id string) (domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.orders[id]
	if !ok {
		return domain.Order{}, domain.ErrNotFound
	}
	srog.DebugCtx(ctx, "loaded order {OrderId}", id)
	return o, nil
}

// Compile-time assertion that Repo satisfies the domain port.
var _ domain.OrderRepository = (*Repo)(nil)
