package app_test

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/fullexample/internal/app"
	"github.com/dvislobokov/srog/fullexample/internal/domain"

	"go.opentelemetry.io/otel/trace/noop"
)

// TestMain silences the default logger so use-case logs do not clutter test
// output — the point of the clean-architecture split is that this test needs no
// HTTP server, no tracer backend, and no real database.
func TestMain(m *testing.M) {
	srog.SetDefault(srog.MustNew(srog.WithWriter(io.Discard)))
	os.Exit(m.Run())
}

type stubRepo struct {
	saved   map[string]domain.Order
	saveErr error
}

func newStub() *stubRepo { return &stubRepo{saved: map[string]domain.Order{}} }

func (s *stubRepo) Save(_ context.Context, o domain.Order) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved[o.ID] = o
	return nil
}

func (s *stubRepo) FindByID(_ context.Context, id string) (domain.Order, error) {
	o, ok := s.saved[id]
	if !ok {
		return domain.Order{}, domain.ErrNotFound
	}
	return o, nil
}

func newService(repo domain.OrderRepository) *app.OrderService {
	return app.NewOrderService(repo, noop.NewTracerProvider().Tracer("test"))
}

func TestCreateOrder(t *testing.T) {
	repo := newStub()
	svc := newService(repo)

	got, err := svc.CreateOrder(context.Background(), app.CreateOrderInput{Customer: "neo", AmountCents: 1999})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if got.Customer != "neo" || got.Status != domain.StatusPending || got.AmountCents != 1999 {
		t.Fatalf("unexpected order: %+v", got)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("order was not persisted")
	}
}

func TestCreateOrderValidation(t *testing.T) {
	svc := newService(newStub())
	if _, err := svc.CreateOrder(context.Background(), app.CreateOrderInput{Customer: "", AmountCents: 10}); err == nil {
		t.Fatal("expected an error for empty customer")
	}
	if _, err := svc.CreateOrder(context.Background(), app.CreateOrderInput{Customer: "neo", AmountCents: 0}); err == nil {
		t.Fatal("expected an error for non-positive amount")
	}
}

func TestGetOrderNotFound(t *testing.T) {
	svc := newService(newStub())
	if _, err := svc.GetOrder(context.Background(), "missing"); err != domain.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
