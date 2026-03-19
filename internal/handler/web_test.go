package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestTextToHTMLBlankLineParagraphBreak(t *testing.T) {
	got := textToHTML("First paragraph.\n\nSecond paragraph.")
	if strings.Count(got, "<p>") != 2 {
		t.Errorf("expected 2 paragraphs, got: %s", got)
	}
}

func TestTextToHTMLWrappedLinesJoined(t *testing.T) {
	// Two lines that do not end a sentence → joined with space
	got := textToHTML("first line\ncontinued here.")
	if strings.Contains(got, "\n") {
		t.Errorf("wrapped lines should be joined: %s", got)
	}
	if !strings.Contains(got, "first line continued here.") {
		t.Errorf("expected joined text, got: %s", got)
	}
}

func TestTextToHTMLSentenceEndStartsNewParagraph(t *testing.T) {
	// Line ending with "." followed by line starting with uppercase → new paragraph
	got := textToHTML("End of sentence.\nNew sentence starts here.")
	if strings.Count(got, "<p>") != 2 {
		t.Errorf("expected 2 paragraphs on sentence boundary, got: %s", got)
	}
}

func TestTextToHTMLSoftHyphenJoined(t *testing.T) {
	got := textToHTML("word\u00ad\ncontinued")
	if strings.Contains(got, "\u00ad") {
		t.Errorf("soft hyphen should be removed: %s", got)
	}
	if !strings.Contains(got, "wordcontinued") {
		t.Errorf("expected joined word, got: %s", got)
	}
}

func TestTextToHTMLHyphenLineBreakJoined(t *testing.T) {
	got := textToHTML("auto-\nmatic")
	if !strings.Contains(got, "auto-matic") {
		t.Errorf("expected hyphenated word rejoined, got: %s", got)
	}
}

func TestTextToHTMLEscapesHTML(t *testing.T) {
	got := textToHTML("a & b <tag>")
	if strings.Contains(got, "<tag>") {
		t.Errorf("HTML should be escaped, got: %s", got)
	}
	if !strings.Contains(got, "&amp;") {
		t.Errorf("& should be escaped, got: %s", got)
	}
}

func TestTextToHTMLEmptyInput(t *testing.T) {
	got := textToHTML("")
	if got != "" {
		t.Errorf("expected empty output for empty input, got: %s", got)
	}
}

func TestTextToHTMLCyrillicCapitalStartsNewParagraph(t *testing.T) {
	// Serbian Cyrillic uppercase should trigger paraStart
	got := textToHTML("Kraj rečenice.\nАли нова почиње.")
	if strings.Count(got, "<p>") != 2 {
		t.Errorf("expected 2 paragraphs with Cyrillic capital, got: %s", got)
	}
}

// --- isUpperStart ---

func TestIsUpperStart(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
	}{
		{'A', true},
		{'Z', true},
		{'a', false},
		{'z', false},
		{'А', true},  // Cyrillic uppercase
		{'я', false}, // Cyrillic lowercase
		{'Č', true},  // Latin extended uppercase
		{'"', true},
		{'„', true},
		{'—', true},
		{'-', true},
		{'1', false},
	}
	for _, tc := range cases {
		if got := isUpperStart(tc.r); got != tc.want {
			t.Errorf("isUpperStart(%q) = %v, want %v", tc.r, got, tc.want)
		}
	}
}

// --- splitAtSentenceBoundaries ---

func TestSplitAtSentenceBoundariesShortPassthrough(t *testing.T) {
	para := "Short text. Only two sentences."
	got := splitAtSentenceBoundaries(para, 4)
	if len(got) != 1 || got[0] != para {
		t.Errorf("short paragraph should pass through unchanged, got %v", got)
	}
}

func TestSplitAtSentenceBoundariesLongSplits(t *testing.T) {
	// Build a paragraph > 350 chars with many sentences.
	sentence := "This is a long sentence that ends here. "
	para := strings.Repeat(sentence, 15) // ~600 chars, 15 sentences
	got := splitAtSentenceBoundaries(para, 4)
	if len(got) < 2 {
		t.Errorf("expected multiple splits for long paragraph with many sentences, got %d", len(got))
	}
}

// --- randomHex ---

func TestRandomHexLength(t *testing.T) {
	for _, n := range []int{4, 8, 16} {
		got, err := randomHex(n)
		if err != nil {
			t.Fatalf("randomHex(%d): unexpected error: %v", n, err)
		}
		if len(got) != 2*n {
			t.Errorf("randomHex(%d): expected len %d, got %d", n, 2*n, len(got))
		}
	}
}

func TestRandomHexIsHex(t *testing.T) {
	got, err := randomHex(8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, c := range got {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("randomHex returned non-hex character %q in %q", c, got)
		}
	}
}

// --- HTTP routing and handlers ---

func newTestWebHandler(t *testing.T) *WebHandler {
	t.Helper()
	dir := t.TempDir()
	h, err := NewWebHandler(NewDocumentHandler(), dir)
	if err != nil {
		t.Fatalf("NewWebHandler: %v", err)
	}
	return h
}

func TestServeHTTPUnknownPathReturns404(t *testing.T) {
	h := newTestWebHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/not-a-real-path", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestServeHTTPUploadPageReturnsHTML(t *testing.T) {
	h := newTestWebHandler(t)
	for _, path := range []string{"/", "/upload"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: expected text/html content-type, got %q", path, ct)
		}
	}
}

func TestHandleUploadGetMethodNotAllowed(t *testing.T) {
	h := newTestWebHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/upload", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHandleDownloadMissingQueryParam(t *testing.T) {
	h := newTestWebHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing file param, got %d", rec.Code)
	}
}

func TestHandleDownloadInvalidExtension(t *testing.T) {
	h := newTestWebHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/download?file=book.pdf", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-.epub extension, got %d", rec.Code)
	}
}

func TestHandleDownloadFileNotFound(t *testing.T) {
	h := newTestWebHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/download?file=missing.epub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing epub, got %d", rec.Code)
	}
}

func TestHandleDownloadServesFile(t *testing.T) {
	h := newTestWebHandler(t)

	// Write a dummy epub file into the handler's upload dir.
	epubPath := h.uploadDir + "/test.epub"
	if err := os.WriteFile(epubPath, []byte("PK fake epub"), 0600); err != nil {
		t.Fatalf("write test epub: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/download?file=test.epub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for existing epub, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/epub+zip" {
		t.Errorf("expected application/epub+zip, got %q", ct)
	}
}
