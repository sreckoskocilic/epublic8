package handler

import (
	"context"
	"testing"

	epb "epublic8/pb"
)

// DocumentHandler tests for gRPC methods

func TestNewDocumentHandler(t *testing.T) {
	tests := []struct {
		name              string
		concurrencyLimit  int
		ocrLanguages      []string
		wantConcurrency   int
		wantLanguagesSize int
	}{
		{
			name:              "with custom concurrency",
			concurrencyLimit:  4,
			ocrLanguages:      []string{"eng", "fra"},
			wantConcurrency:   4,
			wantLanguagesSize: 2,
		},
		{
			name:              "with zero concurrency uses default",
			concurrencyLimit:  0,
			ocrLanguages:      nil,
			wantConcurrency:   0, // Will use default (GOMAXPROCS)
			wantLanguagesSize: 3, // Default languages
		},
		{
			name:              "with negative concurrency uses default",
			concurrencyLimit:  -1,
			ocrLanguages:      nil,
			wantConcurrency:   -1, // Will use default
			wantLanguagesSize: 3,
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

func TestExtractEntities(t *testing.T) {
	h := NewDocumentHandler(0, nil)

	tests := []struct {
		name         string
		text         string
		entityTypes  []string
		wantMinCount int
	}{
		{
			name:         "empty text returns empty",
			text:         "",
			entityTypes:  nil,
			wantMinCount: 0,
		},
		{
			name:         "extract emails",
			text:         "Contact us at test@example.com or info@company.org",
			entityTypes:  []string{"EMAIL"},
			wantMinCount: 2,
		},
		{
			name:         "extract persons",
			text:         "John Smith and Jane Doe attended the meeting",
			entityTypes:  []string{"PERSON"},
			wantMinCount: 2,
		},
		{
			name:         "extract dates",
			text:         "The event is on 12/25/2024 or January 15, 2025",
			entityTypes:  []string{"DATE"},
			wantMinCount: 2,
		},
		{
			name:         "extract phone numbers",
			text:         "Call us at (555) 123-4567 or +1-555-987-6543",
			entityTypes:  []string{"PHONE"},
			wantMinCount: 2,
		},
		{
			name:         "extract all types",
			text:         "John Smith works at Acme Corp. Contact john@acme.com or 555-1234. Meeting on 01/15/2025.",
			entityTypes:  nil, // All types
			wantMinCount: 4,
		},
		{
			name:         "no matches",
			text:         "This is just some random text with no entities.",
			entityTypes:  []string{"PERSON", "EMAIL", "PHONE"},
			wantMinCount: 0,
		},
		{
			name:         "extract locations",
			text:         "The meeting is in New York, NY or Paris, France",
			entityTypes:  []string{"LOCATION"},
			wantMinCount: 2,
		},
		{
			name:         "extract organizations",
			text:         "Apple Inc. and Google LLC are tech companies",
			entityTypes:  []string{"ORGANIZATION"},
			wantMinCount: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &epb.EntityRequest{
				DocumentId:  "test-doc",
				Text:        tc.text,
				EntityTypes: tc.entityTypes,
			}

			resp, err := h.ExtractEntities(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if resp.DocumentId != "test-doc" {
				t.Errorf("expected DocumentId test-doc, got %s", resp.DocumentId)
			}

			if len(resp.Entities) < tc.wantMinCount {
				t.Errorf("expected at least %d entities, got %d", tc.wantMinCount, len(resp.Entities))
			}
		})
	}
}

func TestExtractEntitiesWithSpecificTypes(t *testing.T) {
	h := NewDocumentHandler(0, nil)

	// Test with specific entity type filters
	req := &epb.EntityRequest{
		DocumentId:  "test-doc",
		Text:        "John lives in New York and works at Test Inc. Email: test@test.com",
		EntityTypes: []string{"PERSON", "EMAIL"},
	}

	resp, err := h.ExtractEntities(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only have PERSON and EMAIL, not LOCATION or ORGANIZATION
	for _, e := range resp.Entities {
		if e.Type != "PERSON" && e.Type != "EMAIL" {
			t.Errorf("unexpected entity type: %s", e.Type)
		}
	}
}

func TestExtractEntitiesConfidence(t *testing.T) {
	h := NewDocumentHandler(0, nil)

	req := &epb.EntityRequest{
		DocumentId:  "test-doc",
		Text:        "Contact test@example.com",
		EntityTypes: []string{"EMAIL"},
	}

	resp, err := h.ExtractEntities(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range resp.Entities {
		if e.Confidence == 0 {
			t.Error("expected non-zero confidence")
		}
	}
}

func TestExtractEntitiesOffsets(t *testing.T) {
	h := NewDocumentHandler(0, nil)

	req := &epb.EntityRequest{
		DocumentId:  "test-doc",
		Text:        "Contact test@example.com for help",
		EntityTypes: []string{"EMAIL"},
	}

	resp, err := h.ExtractEntities(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range resp.Entities {
		if e.StartOffset == "" || e.EndOffset == "" {
			t.Error("expected non-empty offsets")
		}
	}
}

func TestExtractEntitiesDeduplication(t *testing.T) {
	h := NewDocumentHandler(0, nil)

	// Text with repeated entity
	req := &epb.EntityRequest{
		DocumentId:  "test-doc",
		Text:        "John Smith and John Smith both attended",
		EntityTypes: []string{"PERSON"},
	}

	resp, err := h.ExtractEntities(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should deduplicate to single "John Smith"
	if len(resp.Entities) != 1 {
		t.Errorf("expected 1 entity after deduplication, got %d", len(resp.Entities))
	}
}

func TestHealthCheck(t *testing.T) {
	h := NewDocumentHandler(0, nil)

	req := &epb.HealthRequest{}
	resp, err := h.Health(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The processor should be loaded (not nil)
	if resp.Healthy == false {
		t.Error("expected healthy to be true")
	}

	// Active requests should be 0 initially
	if resp.ActiveRequests != 0 {
		t.Errorf("expected 0 active requests, got %d", resp.ActiveRequests)
	}

	// Status should be "ready"
	if resp.Status != "ready" {
		t.Errorf("expected status 'ready', got %s", resp.Status)
	}
}

// Note: ProcessDocument and StreamProcessDocument are harder to test without
// actual PDF content or mock setup, so they're covered by integration tests
// or tested through the HTTP handlers.

func TestProcessDocumentPlainText(t *testing.T) {
	h := NewDocumentHandler(0, nil)

	req := &epb.DocumentRequest{
		DocumentId: "test-doc-1",
		Content:    []byte("Hello world. This is a test document."),
		MimeType:   "text/plain",
	}

	resp, err := h.ProcessDocument(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.DocumentId != "test-doc-1" {
		t.Errorf("expected DocumentId 'test-doc-1', got %q", resp.DocumentId)
	}
	if resp.ExtractedText != "Hello world. This is a test document." {
		t.Errorf("unexpected extracted text: %q", resp.ExtractedText)
	}
	if resp.Stats.ProcessingTimeMs < 0 {
		t.Errorf("expected non-negative processing time, got %d", resp.Stats.ProcessingTimeMs)
	}
	if resp.Metadata.DetectedLanguage == "" {
		t.Error("expected non-empty detected language")
	}
}

func TestProcessDocumentMarkdown(t *testing.T) {
	h := NewDocumentHandler(0, nil)

	req := &epb.DocumentRequest{
		DocumentId: "test-md",
		Content:    []byte("# Title\n\nSome markdown content."),
		MimeType:   "text/markdown",
	}

	resp, err := h.ProcessDocument(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.ExtractedText != "# Title\n\nSome markdown content." {
		t.Errorf("unexpected extracted text: %q", resp.ExtractedText)
	}
}

func TestProcessDocumentWithOCROption(t *testing.T) {
	h := NewDocumentHandler(0, nil)

	req := &epb.DocumentRequest{
		DocumentId: "test-ocr",
		Content:    []byte("Test with OCR enabled"),
		MimeType:   "text/plain",
		Options:    &epb.ProcessOptions{EnableOcr: true},
	}

	resp, err := h.ProcessDocument(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.ExtractedText != "Test with OCR enabled" {
		t.Errorf("unexpected extracted text: %q", resp.ExtractedText)
	}
}

func TestHealthCheckWithActiveRequests(t *testing.T) {
	h := NewDocumentHandler(0, nil)

	// Simulate an active request
	h.incrementRequests()

	req := &epb.HealthRequest{}
	resp, err := h.Health(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.ActiveRequests != 1 {
		t.Errorf("expected 1 active request, got %d", resp.ActiveRequests)
	}

	h.decrementRequests()

	// Verify it decremented
	resp2, err := h.Health(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp2.ActiveRequests != 0 {
		t.Errorf("expected 0 active requests after decrement, got %d", resp2.ActiveRequests)
	}
}

func TestHandlerClose(t *testing.T) {
	h := NewDocumentHandler(0, nil)
	if err := h.Close(); err != nil {
		t.Errorf("unexpected error on close: %v", err)
	}
}
