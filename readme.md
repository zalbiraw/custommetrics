# Custom Metrics Plugin

Collects Prometheus metrics based on HTTP headers.

## Configuration

```json
{
  "metricHeaders": ["X-User-ID"],
  "metricName": "custom_requests",
  "metricType": "counter",
  "metricsPort": 8081
}
```

- `metricHeaders`: HTTP headers to monitor
- `metricName`: Metric name  
- `metricType`: "counter", "histogram", or "gauge"
- `metricsPort`: Metrics endpoint port

Metrics endpoint: `http://localhost:8081/metrics`
