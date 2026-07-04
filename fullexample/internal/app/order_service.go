// Package app holds the application use cases. It orchestrates domain entities
// and ports, and treats observability as a cross-cutting concern carried on the
// context: it logs through the package-level srog.*Ctx helpers (which resolve the
// request-scoped logger from the context — enriched by the middleware and
// srogotel with CorrelationId + trace_id/span_id) and starts spans on an injected
// tracer. There is no logger field and no logger parameter; it knows nothing
// about HTTP or the concrete database.
package app

import (
	"context"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/fullexample/internal/domain"

	"go.opentelemetry.io/otel/trace"
)

// CreateOrderInput is the use-case request model (decoupled from any DTO/HTTP).
type CreateOrderInput struct {
	Customer    string
	AmountCents int64
}

// OrderService implements the order use cases. Its only collaborators are the
// repository port and a tracer — both injected, so it is trivially unit-testable.
type OrderService struct {
	repo   domain.OrderRepository
	tracer trace.Tracer
	newID  func() string
}

// NewOrderService wires the service. newID defaults to srog.NewID.
func NewOrderService(repo domain.OrderRepository, tracer trace.Tracer) *OrderService {
	return &OrderService{repo: repo, tracer: tracer, newID: srog.NewID}
}

// CreateOrder validates and persists a new order.
func (s *OrderService) CreateOrder(ctx context.Context, in CreateOrderInput) (domain.Order, error) {
	ctx, span := s.tracer.Start(ctx, "OrderService.CreateOrder")
	defer span.End()

	// Log through the package directly — the ctx carries the request-scoped
	// logger, so these lines pick up CorrelationId + trace_id/span_id.
	order, err := domain.NewOrder(s.newID(), in.Customer, in.AmountCents)
	if err != nil {
		srog.WarningCtx(ctx, "rejected invalid order: {Reason}", err.Error())
		return domain.Order{}, err
	}
	if err := s.repo.Save(ctx, order); err != nil {
		srog.ErrorCtx(ctx, err, "failed to persist order {OrderId}", order.ID)
		return domain.Order{}, err
	}
	srog.InfoCtx(ctx, "created order {OrderId} for {Customer} ({Amount} cents)",
		order.ID, order.Customer, order.AmountCents)
	return order, nil
}

// GetOrder loads an order by id.
func (s *OrderService) GetOrder(ctx context.Context, id string) (domain.Order, error) {
	ctx, span := s.tracer.Start(ctx, "OrderService.GetOrder")
	defer span.End()

	order, err := s.repo.FindByID(ctx, id)
	if err != nil {
		srog.WarningCtx(ctx, "order {OrderId} lookup failed: {Reason}", id, err.Error())
		return domain.Order{}, err
	}
	srog.DebugCtx(ctx, "fetched order {OrderId}", id)
	return order, nil
}
