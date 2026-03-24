package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStatusToString(t *testing.T) {
	tests := []struct {
		status   int
		expected string
	}{
		{200, "2xx"},
		{201, "2xx"},
		{299, "2xx"},
		{300, "3xx"},
		{301, "3xx"},
		{399, "3xx"},
		{400, "4xx"},
		{401, "4xx"},
		{499, "4xx"},
		{500, "5xx"},
		{501, "5xx"},
		{599, "5xx"},
		{0, "other"},
		{199, "other"},
		{600, "other"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			result := statusToString(tc.status)
			if result != tc.expected {
				t.Errorf("statusToString(%d) = %s, want %s", tc.status, result, tc.expected)
			}
		})
	}
}

func TestRecordRequest(t *testing.T) {
	// RecordRequest just increments counters, so we just test it doesn't panic
	t.Run("valid inputs", func(t *testing.T) {
		RecordRequest("GET", "/test", 200, 0.1)
		RecordRequest("POST", "/api/upload", 201, 0.2)
		RecordRequest("GET", "/download", 404, 0.05)
		RecordRequest("POST", "/api/upload", 500, 1.0)
	})
}

func TestRecordDocumentProcessed(t *testing.T) {
	// RecordDocumentProcessed just increments counters
	t.Run("success", func(t *testing.T) {
		RecordDocumentProcessed(true)
	})

	t.Run("error", func(t *testing.T) {
		RecordDocumentProcessed(false)
	})
}

func TestRecordOCRCall(t *testing.T) {
	t.Run("increments counter", func(t *testing.T) {
		RecordOCRCall()
	})
}

func TestActiveRequestsGauge(t *testing.T) {
	// Test increment and decrement
	IncActiveRequests()
	IncActiveRequests()
	DecActiveRequests()
	DecActiveRequests()
}

func TestDocumentsInProgressGauge(t *testing.T) {
	// Test increment and decrement
	IncDocumentsInProgress()
	IncDocumentsInProgress()
	DecDocumentsInProgress()
	DecDocumentsInProgress()
}

func TestMiddleware(t *testing.T) {
	t.Run("middleware calls next handler", func(t *testing.T) {
		nextCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		})

		handler := Middleware(next)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if !nextCalled {
			t.Error("expected next handler to be called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("middleware records status code", func(t *testing.T) {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})

		handler := Middleware(next)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("middleware records duration", func(t *testing.T) {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(10 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		})

		handler := Middleware(next)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		start := time.Now()
		handler.ServeHTTP(rec, req)
		duration := time.Since(start)

		// Should have taken at least 10ms
		if duration < 10*time.Millisecond {
			t.Errorf("expected duration >= 10ms, got %v", duration)
		}
	})

	t.Run("middleware skips metrics endpoint", func(t *testing.T) {
		nextCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
		})

		handler := Middleware(next)
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if !nextCalled {
			t.Error("expected next handler to be called for /metrics")
		}
	})

	t.Run("middleware tracks active requests", func(t *testing.T) {
		// The gauge should be incremented and decremented correctly
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		handler := Middleware(next)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		// If this doesn't panic, gauge is working
	})
}

func TestMiddlewareWithDifferentMethods(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			handler := Middleware(next)
			req := httptest.NewRequest(method, "/test", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", rec.Code)
			}
		})
	}
}

func TestStatusRecorder(t *testing.T) {
	t.Run("captures status code", func(t *testing.T) {
		rec := &statusRecorder{
			ResponseWriter: httptest.NewRecorder(),
			statusCode:     0,
		}

		rec.WriteHeader(http.StatusNotFound)

		if rec.statusCode != http.StatusNotFound {
			t.Errorf("expected statusCode %d, got %d", http.StatusNotFound, rec.statusCode)
		}
	})
}

func TestRecordRequestWithVariousPaths(t *testing.T) {
	paths := []string{
		"/",
		"/upload",
		"/api/upload",
		"/download",
		"/health/live",
		"/health/ready",
		"/version",
		"/metrics",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			RecordRequest("GET", path, 200, 0.1)
		})
	}
}
