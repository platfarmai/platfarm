package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"platfarm/sdk/go/metrics"
)

func Test_Middleware_exposes_service_label_on_counter(t *testing.T) {
	// Given
	const service = "svc-demo"
	mux := http.NewServeMux()
	mux.Handle("/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	mux.Handle("/metrics", metrics.Handler())
	srv := httptest.NewServer(metrics.Middleware(service, mux))
	t.Cleanup(srv.Close)

	// When
	for range 2 {
		resp, err := http.Get(srv.URL + "/ping")
		if err != nil {
			t.Fatalf("ping: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("ping status = %d", resp.StatusCode)
		}
	}
	metricsResp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	body, err := io.ReadAll(metricsResp.Body)
	_ = metricsResp.Body.Close()
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	text := string(body)

	// Then
	if !strings.Contains(text, `http_requests_total{code="200",method="GET",service="svc-demo"}`) &&
		!strings.Contains(text, `service="svc-demo"`) {
		t.Fatalf("metrics text missing service label; got:\n%s", text)
	}
	if !strings.Contains(text, "http_requests_total") {
		t.Fatalf("metrics text missing http_requests_total; got:\n%s", text)
	}
}
