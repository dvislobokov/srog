// Command shared-convention demonstrates one correlation-id convention shared
// across services via the platformlog package. An HTTP "checkout" service and a
// gRPC "inventory" service both log the SAME CorrelationId — propagated across
// the service boundary — while their handlers only ever call srog.FromContext
// and never mention the field name.
//
//	go run .
//
// Output is JSON so the CorrelationId field is visible; every line for one
// request carries the same value ("corr-abc-123", supplied by the client).
package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/examples/shared-convention/platformlog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// inventory is the downstream gRPC service. Its handler logs via the injected
// logger — no knowledge of the correlation-id field.
type inventory struct {
	grpc_health_v1.UnimplementedHealthServer
}

func (inventory) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	srog.FromContext(ctx).Information("checking stock for {Service}", req.GetService())
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

func main() {
	log := srog.MustNew(srog.WithTimestamp(false)) // JSON to stdout
	defer log.Close()

	// --- inventory gRPC service (its own service name) ---
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err, "listen failed")
	}
	gsrv := grpc.NewServer(grpc.UnaryInterceptor(platformlog.GRPCUnary(log.Named("inventory-svc"))))
	grpc_health_v1.RegisterHealthServer(gsrv, inventory{})
	go func() { _ = gsrv.Serve(lis) }()
	defer gsrv.GracefulStop()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err, "dial failed")
	}
	defer conn.Close()
	inv := grpc_health_v1.NewHealthClient(conn)

	// --- checkout HTTP service (edge) ---
	mux := http.NewServeMux()
	mux.HandleFunc("GET /checkout", func(w http.ResponseWriter, r *http.Request) {
		// Handler reads the injected logger — never the field name.
		reqLog := srog.FromContext(r.Context())
		reqLog.Information("checkout requested for cart {Cart}", r.URL.Query().Get("cart"))

		// Call the downstream gRPC service, propagating the same correlation id.
		resp, err := inv.Check(platformlog.PropagateGRPC(r.Context()),
			&grpc_health_v1.HealthCheckRequest{Service: "inventory"})
		if err != nil {
			reqLog.Error(err, "inventory check failed")
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		reqLog.Information("inventory status {Status}", resp.GetStatus().String())
		_, _ = w.Write([]byte("ok\n"))
	})
	httpSrv := httptest.NewServer(platformlog.HTTPMiddleware(log.Named("checkout-svc"))(mux))
	defer httpSrv.Close()

	// --- client hits the edge with a correlation id ---
	req, _ := http.NewRequest(http.MethodGet, httpSrv.URL+"/checkout?cart=42", nil)
	req.Header.Set(platformlog.Header, "corr-abc-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err, "client call failed")
	}
	_ = resp.Body.Close()

	time.Sleep(100 * time.Millisecond) // let the interleaved log lines flush
}
