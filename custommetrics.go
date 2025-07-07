// Package custommetrics a custom metrics plugin for Traefik.
package custommetrics

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Config the plugin configuration.
type Config struct {
	MetricHeaders []string `json:"metricHeaders,omitempty"`
	MetricName    string   `json:"metricName,omitempty"`
	MetricType    string   `json:"metricType,omitempty"` // "counter", "histogram", "gauge"
	MetricsPort   int      `json:"metricsPort,omitempty"` // Port for metrics endpoint
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		MetricHeaders: []string{},
		MetricName:    "plugin_custom_requests",
		MetricType:    "counter",
		MetricsPort:   8081,
	}
}

// CustomMetrics a custom metrics plugin.
type CustomMetrics struct {
	next          http.Handler
	metricHeaders []string
	metricName    string
	metricType    string
	metricsPort   int
	name          string
	
	// Prometheus metrics
	counter   prometheus.Counter
	histogram prometheus.Histogram
	gauge     prometheus.Gauge
	
	// Metrics registry and server management
	registry     *prometheus.Registry
	server       *http.Server
	serverStop   chan struct{}
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
		registry:      prometheus.NewRegistry(),
		serverStop:    make(chan struct{}),
		serverStopped: make(chan struct{}),
	}

	// Initialize metrics
	if err := plugin.initializeMetrics(); err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}
	
	// Start metrics server with port conflict detection
	if err := plugin.startMetricsServer(); err != nil {
		return nil, fmt.Errorf("failed to start metrics server: %w", err)
	}

	return plugin, nil
}

// Stop gracefully shuts down the metrics server
func (c *CustomMetrics) Stop() error {
	if c.server != nil {
		close(c.serverStop)
		<-c.serverStopped // Wait for server to stop
		return c.server.Close()
	}
	return nil
}

// initializeMetrics initializes Prometheus metrics
func (c *CustomMetrics) initializeMetrics() error {
	switch c.metricType {
	case "counter":
		c.counter = prometheus.NewCounter(prometheus.CounterOpts{
			Name: c.metricName,
			Help: "Custom counter metric based on HTTP headers",
		})
		c.registry.MustRegister(c.counter)
	case "histogram":
		c.histogram = prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: c.metricName,
			Help: "Custom histogram metric based on HTTP headers",
		})
		c.registry.MustRegister(c.histogram)
	case "gauge":
		c.gauge = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: c.metricName,
			Help: "Custom gauge metric based on HTTP headers",
		})
		c.registry.MustRegister(c.gauge)
	default:
		return fmt.Errorf("unsupported metric type: %s", c.metricType)
	}
	return nil
}

// startMetricsServer starts the metrics HTTP server with port conflict detection
func (c *CustomMetrics) startMetricsServer() error {
	addr := fmt.Sprintf(":%d", c.metricsPort)
	
	// Check if port is available (port 0 means random available port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d is already in use: %w", c.metricsPort, err)
	}
	
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{}))
	
	c.server = &http.Server{
		Addr:    addr,
		Handler: mux,
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

// collectMetrics collects metrics based on the configured headers (optimized)
func (c *CustomMetrics) collectMetrics(req *http.Request) {
	if len(c.metricHeaders) == 0 {
		return
	}

	// Fast path for counters - just check if any header exists
	if c.metricType == "counter" {
		for _, headerName := range c.metricHeaders {
			if req.Header.Get(headerName) != "" {
				c.counter.Inc()
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
	case "histogram":
		if c.histogram != nil {
			c.histogram.Observe(value)
		}
	case "gauge":
		if c.gauge != nil {
			c.gauge.Set(value)
		}
	}
}

func (c *CustomMetrics) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// Collect metrics based on configured headers
	c.collectMetrics(req)

	// Pass request to next handler
	c.next.ServeHTTP(rw, req)
}
