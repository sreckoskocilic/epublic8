package handler

import (
	"testing"
)

func TestNewDocumentHandler(t *testing.T) {
	tests := []struct {
		name             string
		concurrencyLimit int
		ocrLanguages     []string
	}{
		{
			name:             "with custom concurrency",
			concurrencyLimit: 4,
			ocrLanguages:     []string{"eng", "fra"},
		},
		{
			name:             "with zero concurrency uses default",
			concurrencyLimit: 0,
			ocrLanguages:     nil,
		},
		{
			name:             "with negative concurrency uses default",
			concurrencyLimit: -1,
			ocrLanguages:     nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewDocumentHandler(tc.concurrencyLimit, tc.ocrLanguages)
			if h == nil {
				t.Fatal("expected non-nil handler")
			}
			if h.Processor == nil {
				t.Error("expected Processor to be set")
			}
		})
	}
}

func TestHandlerClose(t *testing.T) {
	h := NewDocumentHandler(0, nil)
	if err := h.Close(); err != nil {
		t.Errorf("unexpected error on close: %v", err)
	}
}
