// Package metrics provides Prometheus metrics for the epublic8 service.
// It tracks HTTP request duration, request counts by status, documents processed,
// and OCR usage.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTPRequestsTotal is a counter for total HTTP requests by method, path, and status.
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "http_requests_total",
			Help:        "Total number of HTTP requests",
			Namespace:   "",
			Subsystem:   "",
			ConstLabels: nil,
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestDuration is a histogram for HTTP request duration in seconds.
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:                            "http_request_duration_seconds",
			Help:                            "HTTP request duration in seconds",
			Buckets:                         []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
			Namespace:                       "",
			Subsystem:                       "",
			ConstLabels:                     nil,
			NativeHistogramBucketFactor:     0,
			NativeHistogramZeroThreshold:    0,
			NativeHistogramMaxBucketNumber:  0,
			NativeHistogramMinResetDuration: 0,
			NativeHistogramMaxZeroThreshold: 0,
			NativeHistogramMaxExemplars:     0,
			NativeHistogramExemplarTTL:      0,
		},
		[]string{"method", "path"},
	)

	// DocumentsProcessedTotal is a counter for total documents processed.
	DocumentsProcessedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "documents_processed_total",
			Help:        "Total number of documents processed",
			Namespace:   "",
			Subsystem:   "",
			ConstLabels: nil,
		},
		[]string{"status"}, // "success" or "error"
	)

	// OCRCallsTotal is a counter for total OCR API calls.
	OCRCallsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name:        "ocr_calls_total",
			Help:        "Total number of OCR API calls",
			Namespace:   "",
			Subsystem:   "",
			ConstLabels: nil,
		},
	)

	// ActiveRequests is a gauge for currently active requests.
	ActiveRequests = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name:        "http_active_requests",
			Help:        "Number of currently active HTTP requests",
			Namespace:   "",
			Subsystem:   "",
			ConstLabels: nil,
		},
	)

	// DocumentsInProgress is a gauge for documents currently being processed.
	DocumentsInProgress = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name:        "documents_in_progress",
			Help:        "Number of documents currently being processed",
			Namespace:   "",
			Subsystem:   "",
			ConstLabels: nil,
		},
	)

	// OCRProcessingDuration is a histogram for OCR processing time in seconds.
	OCRProcessingDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:                            "ocr_processing_duration_seconds",
			Help:                            "OCR processing duration in seconds",
			Buckets:                         []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
			Namespace:                       "",
			Subsystem:                       "",
			ConstLabels:                     nil,
			NativeHistogramBucketFactor:     0,
			NativeHistogramZeroThreshold:    0,
			NativeHistogramMaxBucketNumber:  0,
			NativeHistogramMinResetDuration: 0,
			NativeHistogramMaxZeroThreshold: 0,
			NativeHistogramMaxExemplars:     0,
			NativeHistogramExemplarTTL:      0,
		},
	)
)

// RecordRequest increments the request counter and records duration.
// Call this at the end of request handling.
func RecordRequest(method, path string, status int, duration float64) {
	HTTPRequestsTotal.WithLabelValues(method, path, statusToString(status)).Inc()
	HTTPRequestDuration.WithLabelValues(method, path).Observe(duration)
}

// RecordDocumentProcessed increments the document processed counter.
func RecordDocumentProcessed(success bool) {
	status := "success"
	if !success {
		status = "error"
	}
	DocumentsProcessedTotal.WithLabelValues(status).Inc()
}

// RecordOCRCall increments the OCR call counter.
func RecordOCRCall() {
	OCRCallsTotal.Inc()
}

// RecordOCRProcessing records OCR processing duration in seconds.
func RecordOCRProcessing(duration float64) {
	OCRProcessingDuration.Observe(duration)
}

// IncActiveRequests increments the active requests gauge.
func IncActiveRequests() {
	ActiveRequests.Inc()
}

// DecActiveRequests decrements the active requests gauge.
func DecActiveRequests() {
	ActiveRequests.Dec()
}

// IncDocumentsInProgress increments the documents in progress gauge.
func IncDocumentsInProgress() {
	DocumentsInProgress.Inc()
}

// DecDocumentsInProgress decrements the documents in progress gauge.
func DecDocumentsInProgress() {
	DocumentsInProgress.Dec()
}

// statusToString converts HTTP status code to string for labels.
func statusToString(status int) string {
	// Group status codes into categories for better cardinality
	switch {
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 300 && status < 400:
		return "3xx"
	case status >= 400 && status < 500:
		return "4xx"
	case status >= 500 && status < 600:
		return "5xx"
	default:
		return "other"
	}
}

// Middleware returns an HTTP middleware that records metrics for each request.
// The metricsPath parameter specifies the URL path to skip (to avoid infinite recursion).
func Middleware(metricsPath string, next http.Handler) http.Handler {
	if metricsPath == "" {
		metricsPath = "/metrics"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip metrics endpoint to avoid infinite recursion
		if r.URL.Path == metricsPath {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()

		IncActiveRequests()
		defer DecActiveRequests()

		// Use a response writer wrapper to capture status code
		wrapped := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start).Seconds()

		// Record metrics
		RecordRequest(r.Method, r.URL.Path, wrapped.statusCode, duration)
	})
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code.
func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush delegates to the underlying ResponseWriter if it supports http.Flusher.
// This is required for SSE streaming to work through the metrics middleware.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
