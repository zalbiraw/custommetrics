package custommetrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetricsOnly(t *testing.T) {
	cfg := CreateConfig()
	cfg.MetricHeaders = []string{"X-User-ID"}
	cfg.MetricName = "test_counter"
	cfg.MetricType = "counter"
	cfg.MetricsPort = 8084

	ctx := context.Background()
	next := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})

	handler, err := New(ctx, next, cfg, "test-plugin")
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-User-ID", "user123")

	handler.ServeHTTP(recorder, req)

	if recorder.Code != 200 {
		t.Errorf("expected status 200, got %d", recorder.Code)
	}
}

func TestMetrics(t *testing.T) {
	cfg := CreateConfig()
	cfg.MetricHeaders = []string{"X-User-ID", "X-Request-Size"}
	cfg.MetricName = "test_counter"
	cfg.MetricType = "counter"
	cfg.MetricsPort = 8082

	ctx := context.Background()
	next := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})

	handler, err := New(ctx, next, cfg, "metrics-plugin")
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Add test headers
	req.Header.Set("X-User-ID", "user123")
	req.Header.Set("X-Request-Size", "1024")

	handler.ServeHTTP(recorder, req)

	// Test passes if no errors occur during metric collection
	if recorder.Code != 200 {
		t.Errorf("expected status 200, got %d", recorder.Code)
	}
}

func TestCombinedRequestResponseHeaders(t *testing.T) {
	cfg := CreateConfig()
	cfg.MetricHeaders = []string{"X-User-ID", "X-Response-ID"}
	cfg.MetricName = "combined_test_counter"
	cfg.MetricType = "counter"
	cfg.MetricsPort = 8083

	ctx := context.Background()
	next := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		// Set response header
		rw.Header().Set("X-Response-ID", "resp123")
		rw.WriteHeader(http.StatusOK)
	})

	handler, err := New(ctx, next, cfg, "combined-test-plugin")
	if err != nil {
		t.Fatal(err)
	}

	// Test 1: Request header only
	recorder := httptest.NewRecorder()
	req1, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost", nil)
	if err != nil {
		t.Fatal(err)
	}
	req1.Header.Set("X-User-ID", "user123")
	handler.ServeHTTP(recorder, req1)

	// Test 2: Response header only
	recorder2 := httptest.NewRecorder()
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler.ServeHTTP(recorder2, req2)

	// Test 3: Both headers
	recorder3 := httptest.NewRecorder()
	req3, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost", nil)
	if err != nil {
		t.Fatal(err)
	}
	req3.Header.Set("X-User-ID", "user456")
	handler.ServeHTTP(recorder3, req3)

	if recorder.Code != 200 || recorder2.Code != 200 || recorder3.Code != 200 {
		t.Errorf("expected status 200, got %d, %d, %d", recorder.Code, recorder2.Code, recorder3.Code)
	}

	// Check that metrics are being created with different labels
	plugin := handler.(*CustomMetrics)
	plugin.store.mu.RLock()
	metricsCount := len(plugin.store.metrics)
	plugin.store.mu.RUnlock()

	if metricsCount == 0 {
		t.Error("expected metrics to be created")
	}
	
	t.Logf("Created %d different metric series", metricsCount)

	// Print the Prometheus format to see the labels
	prometheusOutput := plugin.renderPrometheusFormat()
	t.Logf("Prometheus output:\n%s", prometheusOutput)
}

func BenchmarkCustomMetrics(b *testing.B) {
	cfg := CreateConfig()
	cfg.MetricHeaders = []string{"X-User-ID"}
	cfg.MetricName = "benchmark_counter"
	cfg.MetricType = "counter"
	cfg.MetricsPort = 0 // Use random available port

	ctx := context.Background()
	next := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})

	handler, err := New(ctx, next, cfg, "benchmark-plugin")
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Create a new request for each iteration
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost", nil)
			if err != nil {
				b.Fatal(err)
			}
			req.Header.Set("X-User-ID", "user123")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
		}
	})
}
