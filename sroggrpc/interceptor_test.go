package sroggrpc_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/sroggrpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func newLogger() (*srog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	l := srog.MustNew(srog.WithWriter(&buf, srog.AsJSON()), srog.WithTimestamp(false), srog.WithLevel(srog.DebugLevel))
	return l, &buf
}

func lastWith(t *testing.T, buf *bytes.Buffer, key string) map[string]any {
	t.Helper()
	var found map[string]any
	sc := bufio.NewScanner(strings.NewReader(buf.String()))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid json %q: %v", line, err)
		}
		if _, ok := m[key]; ok {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("no log line with key %q:\n%s", key, buf.String())
	}
	return found
}

func TestUnaryInterceptorSuccess(t *testing.T) {
	log, buf := newLogger()
	interceptor := sroggrpc.UnaryServerInterceptor(log)

	handlerCalled := false
	handler := func(ctx context.Context, req any) (any, error) {
		handlerCalled = true
		// The handler logs through the request-scoped logger from the context.
		srog.FromContext(ctx).Debug("handling")
		return "pong", nil
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-id", "abc-123"))
	resp, err := interceptor(ctx, "ping", &grpc.UnaryServerInfo{FullMethod: "/demo.Svc/Ping"}, handler)
	if err != nil || resp != "pong" {
		t.Fatalf("interceptor altered call: resp=%v err=%v", resp, err)
	}
	if !handlerCalled {
		t.Fatal("handler was not invoked")
	}

	m := lastWith(t, buf, "code")
	if m["level"] != "info" || m["code"] != "OK" {
		t.Errorf("success completion wrong: %v", m)
	}
	if m["RequestId"] != "abc-123" || m["method"] != "/demo.Svc/Ping" {
		t.Errorf("completion fields wrong: %v", m)
	}
}

func TestUnaryInterceptorCodeMapping(t *testing.T) {
	cases := []struct {
		code      codes.Code
		wantLevel string
	}{
		{codes.NotFound, "warn"},
		{codes.InvalidArgument, "warn"},
		{codes.Internal, "error"},
		{codes.Unavailable, "error"},
	}
	for _, tc := range cases {
		log, buf := newLogger()
		interceptor := sroggrpc.UnaryServerInterceptor(log)
		handler := func(ctx context.Context, req any) (any, error) {
			return nil, status.Error(tc.code, "boom")
		}
		_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/demo.Svc/Op"}, handler)
		if status.Code(err) != tc.code {
			t.Fatalf("error code not preserved: got %v", status.Code(err))
		}
		m := lastWith(t, buf, "code")
		if m["level"] != tc.wantLevel {
			t.Errorf("code %v: want level %q, got %v", tc.code, tc.wantLevel, m["level"])
		}
		if m["code"] != tc.code.String() {
			t.Errorf("code %v: field=%v", tc.code, m["code"])
		}
	}
}

func TestUnaryInterceptorGeneratesID(t *testing.T) {
	log, buf := newLogger()
	interceptor := sroggrpc.UnaryServerInterceptor(log)
	handler := func(ctx context.Context, req any) (any, error) { return nil, nil }

	_, _ = interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/demo.Svc/Op"}, handler)

	m := lastWith(t, buf, "code")
	id, ok := m["RequestId"].(string)
	if !ok || len(id) != 32 {
		t.Errorf("expected a generated 32-char request id, got %v", m["RequestId"])
	}
}
