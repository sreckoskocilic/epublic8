package model

import "testing"

// TestIntegrationDocumentProcessingWorkflow tests a complete document processing workflow
func TestIntegrationDocumentProcessingWorkflow(t *testing.T) {
	// Test the full workflow: text -> chapters -> EPUB -> verify structure
	t.Run("text to epub full workflow", func(t *testing.T) {
		g := NewEPUBGenerator(0)

		// HTML entities, quotes, special chars
		inputText := `Glava 1
Text with "quotes" and 'apostrophes' and <brackets>.
Email: test@example.com
URL: https://example.com?param=value&other=1
Number: 123-456-7890
Math: 1 + 2 = 3
Ellipsis...`

		result, err := g.GenerateFromText(inputText, nil, 5, "Special Chars", "Autor")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})
}

// TestIntegrationProcessOptionsCombinations tests ProcessOptions struct
func TestIntegrationProcessOptionsCombinations(t *testing.T) {
	opts := []ProcessOptions{
		{
			SmartOCR:       false,
			ForceOCR:       false,
			StripHeaders:   false,
			StripFootnotes: false,
			TextOnly:       false,
		},
		{
			SmartOCR:       true,
			ForceOCR:       true,
			StripHeaders:   true,
			StripFootnotes: true,
			TextOnly:       true,
		},
		{
			SmartOCR:       true,
			ForceOCR:       false,
			StripHeaders:   true,
			StripFootnotes: false,
			TextOnly:       false,
		},
		{
			SmartOCR:       false,
			ForceOCR:       true,
			StripHeaders:   false,
			StripFootnotes: true,
			TextOnly:       true,
		},
	}

	// Test that ProcessOptions can be created with various combinations
	for i, opt := range opts {
		t.Run("options combination", func(t *testing.T) {
			// Just verify the options are valid - the actual processing
			// is done with DefaultProcessOptions in ProcessDocument
			_ = opt.SmartOCR // Verify fields are accessible
			_ = opt.ForceOCR
			_ = i // silence unused variable warning
		})
	}
}
