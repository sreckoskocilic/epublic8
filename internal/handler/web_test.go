package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"epublic8/internal/config"
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
	// Line-break hyphens are removed: "auto-\nmatic" → "automatic"
	if !strings.Contains(got, "automatic") {
		t.Errorf("expected hyphen removed on line break, got: %s", got)
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
	h, err := NewWebHandler(NewDocumentHandler(2, nil), dir, config.SecurityConfig{}, config.MetricsConfig{}, 0)
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

// --- Path Traversal Security Tests ---

func TestHandleDownloadPathTraversalDotDot(t *testing.T) {
	h := newTestWebHandler(t)
	// Attempt path traversal with ".." sequence
	req := httptest.NewRequest(http.MethodGet, "/download?file=../../etc/passwd.epub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal attempt, got %d", rec.Code)
	}
}

func TestHandleDownloadPathTraversalDotDotInMiddle(t *testing.T) {
	h := newTestWebHandler(t)
	// Attempt path traversal with ".." in the middle
	req := httptest.NewRequest(http.MethodGet, "/download?file=foo/../../bar.epub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal attempt, got %d", rec.Code)
	}
}

func TestHandleDownloadPathTraversalLeadingDotDot(t *testing.T) {
	h := newTestWebHandler(t)
	// Attempt with leading ".."
	req := httptest.NewRequest(http.MethodGet, "/download?file=../secret.epub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal attempt, got %d", rec.Code)
	}
}

func TestHandleDownloadPathTraversalEncoded(t *testing.T) {
	h := newTestWebHandler(t)
	// Attempt with URL-encoded ".."
	req := httptest.NewRequest(http.MethodGet, "/download?file=%2e%2e/secret.epub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// Note: filepath.Base will decode this, so the ".." will be detected
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for encoded path traversal attempt, got %d", rec.Code)
	}
}

func TestHandleDownloadPathTraversalDoubleEncoded(t *testing.T) {
	h := newTestWebHandler(t)
	// After one URL decode, %252e%252e becomes %2e%2e/secret.epub.
	// filepath.Base strips the directory component, leaving "secret.epub".
	// No traversal is possible; the file simply doesn't exist → 404.
	req := httptest.NewRequest(http.MethodGet, "/download?file=%252e%252e/secret.epub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for double-encoded path (safe, file absent), got %d", rec.Code)
	}
}

func TestHandleDownloadPathTraversalAbsolutePath(t *testing.T) {
	h := newTestWebHandler(t)
	// Attempt with absolute path
	req := httptest.NewRequest(http.MethodGet, "/download?file=/etc/passwd.epub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for absolute path attempt, got %d", rec.Code)
	}
}

func TestHandleDownloadPathTraversalWithDirectory(t *testing.T) {
	h := newTestWebHandler(t)
	// Attempt with directory prefix
	req := httptest.NewRequest(http.MethodGet, "/download?file=uploads/../../../etc/passwd.epub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for directory traversal attempt, got %d", rec.Code)
	}
}

func TestHandleDownloadInvalidExtensionBlocked(t *testing.T) {
	h := newTestWebHandler(t)
	// Attempt to download non-epub file should be blocked
	req := httptest.NewRequest(http.MethodGet, "/download?file=config.yaml", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-epub extension, got %d", rec.Code)
	}
}

func TestHandleDownloadExtensionNotExecutable(t *testing.T) {
	h := newTestWebHandler(t)
	// Attempt with executable extension should be blocked
	req := httptest.NewRequest(http.MethodGet, "/download?file=malware.exe", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for executable extension, got %d", rec.Code)
	}
}

func TestHandleDownloadNullByteInjection(t *testing.T) {
	h := newTestWebHandler(t)
	// Attempt with null byte (should be sanitized by filepath.Base)
	req := httptest.NewRequest(http.MethodGet, "/download?file=test%00.epub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// Go's http package handles query parameter decoding, null byte becomes unicode replacement or is handled
	// filepath.Base should strip any path components
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Errorf("expected 400 or 404 for null byte attempt, got %d", rec.Code)
	}
}

func TestValidateDownloadPath(t *testing.T) {
	h := newTestWebHandler(t)

	// Valid path should succeed
	path, err := h.validateDownloadPath("validfile.epub")
	if err != nil {
		t.Errorf("valid path should not error: %v", err)
	}
	if !strings.HasSuffix(path, "validfile.epub") {
		t.Errorf("expected path to end with validfile.epub, got %s", path)
	}

	// Path with ".." should fail
	_, err = h.validateDownloadPath("../etc/passwd.epub")
	if err == nil {
		t.Error("path with '..' should error")
	}

	// Absolute path should fail
	_, err = h.validateDownloadPath("/etc/passwd.epub")
	if err == nil {
		t.Error("absolute path should error")
	}

	// Non-epub extension should fail
	_, err = h.validateDownloadPath("file.txt")
	if err == nil {
		t.Error("non-epub extension should error")
	}
}

// --- Health checks ---

func TestHandleLivenessReturnsOK(t *testing.T) {
	h := newTestWebHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("expected text/plain, got %q", ct)
	}
	if rec.Body.String() != "OK" {
		t.Errorf("expected 'OK', got %q", rec.Body.String())
	}
}

func TestHandleReadinessReturnsReady(t *testing.T) {
	h := newTestWebHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json, got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "ready") {
		t.Errorf("expected 'ready' in response, got %q", rec.Body.String())
	}
}

func TestHandleVersionReturnsBuildInfo(t *testing.T) {
	h := newTestWebHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json, got %q", ct)
	}
	// Check that the response contains expected keys
	body := rec.Body.String()
	if !strings.Contains(body, "version") {
		t.Errorf("expected 'version' in response, got %q", body)
	}
	if !strings.Contains(body, "goVersion") {
		t.Errorf("expected 'goVersion' in response, got %q", body)
	}
}
