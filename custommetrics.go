// Package custommetrics a custom metrics plugin for Traefik.
package custommetrics

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Metric type constants.
const (
	MetricTypeCounter   = "counter"   // MetricTypeCounter represents a counter metric.
	MetricTypeHistogram = "histogram" // MetricTypeHistogram represents a histogram metric.
	MetricTypeGauge     = "gauge"     // MetricTypeGauge represents a gauge metric.
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

// responseWriter wraps http.ResponseWriter to capture response headers.
type responseWriter struct {
	http.ResponseWriter
	headerWritten bool
}

// WriteHeader writes the status code and ensures headers are written only once.
func (rw *responseWriter) WriteHeader(statusCode int) {
	if !rw.headerWritten {
		rw.headerWritten = true
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

// Write writes data to the response and ensures headers are written.
func (rw *responseWriter) Write(data []byte) (int, error) {
	if !rw.headerWritten {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(data)
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

	// Metrics will be created dynamically as requests come in

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
	helpAdded := false

	for _, metric := range c.store.metrics {
		// Add HELP and TYPE comments only once per metric name
		if !helpAdded {
			output += fmt.Sprintf("# HELP %s Custom metric based on HTTP headers\n", metric.Name)
			output += fmt.Sprintf("# TYPE %s %s\n", metric.Name, metric.Type)
			helpAdded = true
		}

		// Format metric with labels
		metricLine := metric.Name
		if len(metric.Labels) > 0 {
			labelPairs := make([]string, 0, len(metric.Labels))
			for k, v := range metric.Labels {
				labelPairs = append(labelPairs, fmt.Sprintf("%s=\"%s\"", k, v))
			}
			metricLine += fmt.Sprintf("{%s}", strings.Join(labelPairs, ","))
		}

		output += fmt.Sprintf("%s %.0f\n", metricLine, metric.Value)
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

// getNumericValueFromHeaders extracts the first numeric value from headers, checking request first then response.
func (c *CustomMetrics) getNumericValueFromHeaders(req *http.Request, responseHeaders http.Header) float64 {
	// Check request headers first
	for _, headerName := range c.metricHeaders {
		if headerValue := req.Header.Get(headerName); headerValue != "" {
			if parsedValue, err := strconv.ParseFloat(headerValue, 64); err == nil {
				return parsedValue
			}
		}
	}

	// Check response headers if no numeric value found in request
	for _, headerName := range c.metricHeaders {
		if headerValue := responseHeaders.Get(headerName); headerValue != "" {
			if parsedValue, err := strconv.ParseFloat(headerValue, 64); err == nil {
				return parsedValue
			}
		}
	}

	return 1 // Default value
}

// createMetricKey creates a unique key for a metric with labels.
func (c *CustomMetrics) createMetricKey(metricName string, labels map[string]string) string {
	key := metricName
	for k, v := range labels {
		key += fmt.Sprintf("_%s_%s", k, v)
	}
	return key
}

// collectMetrics collects metrics for every request, using header values as labels.
func (c *CustomMetrics) collectMetrics(req *http.Request, responseHeaders http.Header) {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()

	// Collect header values as labels
	labels := make(map[string]string)
	for _, headerName := range c.metricHeaders {
		// Check request headers first
		if value := req.Header.Get(headerName); value != "" {
			labels[headerName] = value
		} else if value := responseHeaders.Get(headerName); value != "" {
			// Check response headers if not found in request
			labels[headerName] = value
		} else {
			// Use empty string for missing headers
			labels[headerName] = ""
		}
	}

	// Create a unique metric key based on labels
	metricKey := c.metricName
	if len(labels) > 0 {
		metricKey = c.createMetricKey(c.metricName, labels)
	}

	// Get or create metric with labels
	metric := c.store.metrics[metricKey]
	if metric == nil {
		metric = &Metric{
			Name:   c.metricName,
			Type:   c.metricType,
			Value:  0,
			Labels: labels,
		}
		c.store.metrics[metricKey] = metric
	}

	// Update metric value
	switch c.metricType {
	case MetricTypeCounter:
		metric.Value++ // Count every request
	case MetricTypeHistogram, MetricTypeGauge:
		metric.Value = c.getNumericValueFromHeaders(req, responseHeaders)
	}
}

// ServeHTTP processes HTTP requests and collects metrics based on both request and response headers.
func (c *CustomMetrics) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// Wrap the response writer to capture response headers
	wrappedRW := &responseWriter{ResponseWriter: rw}

	// Pass request to next handler with wrapped response writer
	c.next.ServeHTTP(wrappedRW, req)

	// Collect metrics based on configured headers from both request and response
	c.collectMetrics(req, wrappedRW.Header())
}
