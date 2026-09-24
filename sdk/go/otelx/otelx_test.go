package otelx_test

import (
	"context"
	"testing"

	"platfarm/sdk/go/otelx"
)

func Test_Init_returns_noop_when_endpoint_unset(t *testing.T) {
	// Given
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	ctx := context.Background()

	// When
	shutdown, err := otelx.Init(ctx, "svc-demo")

	// Then
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown")
	}
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
