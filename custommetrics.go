// Package custommetrics a custom metrics plugin for Traefik.
package custommetrics

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	MetricTypeCounter   = "counter"
	MetricTypeHistogram = "histogram"
	MetricTypeGauge     = "gauge"
)

// Config the plugin configuration.
type Config struct {
	MetricHeaders []string `json:"metricHeaders,omitempty"`
	MetricName    string   `json:"metricName,omitempty"`
	MetricType    string   `json:"metricType,omitempty"`  // "counter", "histogram", "gauge"
	MetricsPort   int      `json:"metricsPort,omitempty"` // Port for metrics endpoint
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		MetricHeaders: []string{},
		MetricName:    "plugin_custom_requests",
		MetricType:    MetricTypeCounter,
		MetricsPort:   8081,
	}
}

// Metric represents a simple metric with value and labels.
type Metric struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Value  float64           `json:"value"`
	Labels map[string]string `json:"labels,omitempty"`
}

// MetricsStore holds all collected metrics.
type MetricsStore struct {
	mu      sync.RWMutex
	metrics map[string]*Metric
}

// CustomMetrics a custom metrics plugin.
type CustomMetrics struct {
	next          http.Handler
	metricHeaders []string
	metricName    string
	metricType    string
	metricsPort   int
	name          string

	// Simple metrics storage
	store         *MetricsStore
	server        *http.Server
	serverStop    chan struct{}
	serverStopped chan struct{}
}

// New created a new CustomMetrics plugin.
func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	if len(config.MetricHeaders) == 0 {
		return nil, fmt.Errorf("metricHeaders cannot be empty")
	}

	plugin := &CustomMetrics{
		metricHeaders: config.MetricHeaders,
		metricName:    config.MetricName,
		metricType:    config.MetricType,
		metricsPort:   config.MetricsPort,
		next:          next,
		name:          name,
		store: &MetricsStore{
			metrics: make(map[string]*Metric),
		},
		serverStop:    make(chan struct{}),
		serverStopped: make(chan struct{}),
	}

	// Initialize metric in store
	plugin.store.metrics[config.MetricName] = &Metric{
		Name:   config.MetricName,
		Type:   config.MetricType,
		Value:  0,
		Labels: make(map[string]string),
	}

	// Start metrics server with port conflict detection
	if err := plugin.startMetricsServer(); err != nil {
		return nil, fmt.Errorf("failed to start metrics server: %w", err)
	}

	return plugin, nil
}

// Stop gracefully shuts down the metrics server.
func (c *CustomMetrics) Stop() error {
	if c.server != nil {
		close(c.serverStop)
		<-c.serverStopped // Wait for server to stop
		return c.server.Close()
	}
	return nil
}

// renderPrometheusFormat renders metrics in Prometheus text format.
func (c *CustomMetrics) renderPrometheusFormat() string {
	c.store.mu.RLock()
	defer c.store.mu.RUnlock()

	var output string
	for _, metric := range c.store.metrics {
		// Add HELP and TYPE comments
		output += fmt.Sprintf("# HELP %s Custom metric based on HTTP headers\n", metric.Name)
		output += fmt.Sprintf("# TYPE %s %s\n", metric.Name, metric.Type)

		// Add metric value
		output += fmt.Sprintf("%s %.0f\n", metric.Name, metric.Value)
	}
	return output
}

// startMetricsServer starts the metrics HTTP server with port conflict detection.
func (c *CustomMetrics) startMetricsServer() error {
	addr := fmt.Sprintf(":%d", c.metricsPort)

	// Check if port is available (port 0 means random available port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d is already in use: %w", c.metricsPort, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, c.renderPrometheusFormat())
	})

	c.server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start server in background with graceful shutdown
	go func() {
		defer close(c.serverStopped)

		if err := c.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			// Log error but don't crash the plugin
			fmt.Printf("Metrics server error: %v\n", err)
		}
	}()

	return nil
}

// collectMetrics collects metrics based on the configured headers (optimized).
func (c *CustomMetrics) collectMetrics(req *http.Request) {
	if len(c.metricHeaders) == 0 {
		return
	}

	c.store.mu.Lock()
	defer c.store.mu.Unlock()

	metric := c.store.metrics[c.metricName]
	if metric == nil {
		return
	}

	// Fast path for counters - just check if any header exists
	if c.metricType == MetricTypeCounter {
		for _, headerName := range c.metricHeaders {
			if req.Header.Get(headerName) != "" {
				metric.Value++
				return // Only increment once per request
			}
		}
		return
	}

	// For histograms and gauges, parse the first numeric header found
	var value float64 = 1 // Default value
	for _, headerName := range c.metricHeaders {
		headerValue := req.Header.Get(headerName)
		if headerValue != "" {
			if parsedValue, err := strconv.ParseFloat(headerValue, 64); err == nil {
				value = parsedValue
				break // Use first valid numeric value
			}
		}
	}

	// Record metrics
	switch c.metricType {
	case MetricTypeHistogram, MetricTypeGauge:
		metric.Value = value
	}
}

func (c *CustomMetrics) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// Collect metrics based on configured headers
	c.collectMetrics(req)

	// Pass request to next handler
	c.next.ServeHTTP(rw, req)
}
