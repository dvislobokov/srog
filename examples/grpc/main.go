// Command grpc shows the sroggrpc server interceptor: it assigns a RequestId,
// injects a request-scoped logger into the call context, and logs each call's
// outcome by gRPC status. The handler pulls that logger back out with
// srog.FromContext — it is never passed as an argument. To stay self-contained
// the example serves grpc's built-in Health service (no protoc needed).
//
//	go run .
package main

import (
	"context"
	"net"
	"time"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/sroggrpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

// healthServer implements grpc's Health service and logs from the handler using
// the interceptor-injected, request-scoped logger.
type healthServer struct {
	grpc_health_v1.UnimplementedHealthServer
}

func (healthServer) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	srog.FromContext(ctx).Information("health check for {Service}", req.GetService())
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

func main() {
	log := srog.MustNew(srog.WithConsole(srog.MinLevel(srog.DebugLevel)))
	defer log.Close()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err, "listen failed")
	}

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(sroggrpc.UnaryServerInterceptor(log)),
	)
	grpc_health_v1.RegisterHealthServer(srv, healthServer{})

	go func() {
		log.Information("gRPC server listening on {Addr}", lis.Addr().String())
		if err := srv.Serve(lis); err != nil {
			log.Error(err, "serve stopped")
		}
	}()
	defer srv.GracefulStop()

	// --- client call ---
	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err, "dial failed")
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Supply a request id from the client; the interceptor reuses it.
	ctx = metadata.AppendToOutgoingContext(ctx, "x-request-id", "req-demo-1")

	resp, err := grpc_health_v1.NewHealthClient(conn).
		Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: "billing"})
	if err != nil {
		log.Error(err, "health check failed")
		return
	}
	log.Information("client received status {Status}", resp.GetStatus().String())

	time.Sleep(100 * time.Millisecond) // let the server-side log line print
}
