package handler

import (
	"archive/zip"
	"context"
	crand "crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"epublic8/internal/config"
	"epublic8/internal/errors"
	"epublic8/internal/handler/middleware"
	"epublic8/internal/model"
	"epublic8/internal/tracing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Build info variables set at build time via -ldflags.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type WebHandler struct {
	documentHandler *DocumentHandler
	epubGenerator   *model.EPUBGenerator
	uploadDir       string
	persistentDir   bool
	templates       *template.Template
	securityCfg     config.SecurityConfig
	metricsCfg      config.MetricsConfig
}

//go:embed templates
var templatesFS embed.FS

// loadTemplates loads HTML templates from the embedded filesystem.
func loadTemplates() (*template.Template, error) {
	tmpl := template.New("upload")

	// Read and parse the upload template
	// When embedding a directory with //go:embed templates, files are accessed directly
	uploadTmpl, err := templatesFS.ReadFile("templates/upload.html")
	if err != nil {
		return nil, fmt.Errorf("failed to read upload template: %w", err)
	}

	tmpl, err = tmpl.Parse(string(uploadTmpl))
	if err != nil {
		return nil, fmt.Errorf("failed to parse upload template: %w", err)
	}

	return tmpl, nil
}

func NewWebHandler(docHandler *DocumentHandler, outputDir string, securityCfg config.SecurityConfig, metricsCfg config.MetricsConfig, chapterWords int) (*WebHandler, error) {
	persistent := outputDir != ""
	if !persistent {
		var err error
		outputDir, err = os.MkdirTemp("", "uploads")
		if err != nil {
			return nil, fmt.Errorf("failed to create upload dir: %v", err)
		}
	} else if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output dir %q: %v", outputDir, err)
	}

	tmpl, err := loadTemplates()
	if err != nil {
		return nil, fmt.Errorf("failed to load templates: %v", err)
	}

	epubGen := model.NewEPUBGenerator(chapterWords)

	return &WebHandler{
		documentHandler: docHandler,
		epubGenerator:   epubGen,
		uploadDir:       outputDir,
		templates:       tmpl,
		persistentDir:   persistent,
		securityCfg:     securityCfg,
		metricsCfg:      metricsCfg,
	}, nil
}

func (h *WebHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check if authentication is required for this endpoint
	var protectedPaths = map[string]bool{
		"/api/upload": true,
		"/download":   true,
	}

	path := r.URL.Path
	isProtected := protectedPaths[path]

	// Apply auth middleware if this is a protected endpoint
	if isProtected && h.securityCfg.BasicAuth != "" {
		// Create a basic auth wrapper for this specific handler
		authHandler := middleware.BasicAuth(&h.securityCfg, http.HandlerFunc(h.servePath))
		authHandler.ServeHTTP(w, r)
		return
	}

	// No auth needed - serve directly
	h.servePath(w, r)
}

// servePath handles routing to the appropriate handler based on URL path.
// This is separated from ServeHTTP to allow auth middleware to wrap it.
func (h *WebHandler) servePath(w http.ResponseWriter, r *http.Request) {
	// Handle metrics endpoint separately - no auth required, uses promhttp
	if h.metricsCfg.Enabled && r.URL.Path == h.metricsCfg.Path {
		promhttp.Handler().ServeHTTP(w, r)
		return
	}

	switch r.URL.Path {
	case "/", "/upload":
		h.serveUploadPage(w, r)
	case "/api/upload":
		h.handleUpload(w, r)
	case "/download":
		h.handleDownload(w, r)
	case "/health/live":
		h.handleLiveness(w, r)
	case "/health/ready":
		h.handleReadiness(w, r)
	case "/version":
		h.handleVersion(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleLiveness returns a simple OK response indicating the service is running.
func (h *WebHandler) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // simple response, ignore write error
	w.Write([]byte("OK"))
}

// handleReadiness checks if the service is ready to accept requests.
func (h *WebHandler) handleReadiness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check if upload directory is accessible
	if err := h.checkUploadDir(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		//nolint:errcheck // JSON response, ignore write error
		json.NewEncoder(w).Encode(map[string]string{
			"status": "not ready",
			"error":  err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // JSON response, ignore write error
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ready",
	})
}

// checkUploadDir verifies the upload directory is accessible for writes.
func (h *WebHandler) checkUploadDir() error {
	// Try to create a temp file to verify write access
	testFile, err := os.CreateTemp(h.uploadDir, "healthcheck_*")
	if err != nil {
		return fmt.Errorf("upload dir not writable: %v", err)
	}
	name := testFile.Name()
	testFile.Close()
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("failed to cleanup test file: %v", err)
	}
	return nil
}

// handleVersion returns build information.
func (h *WebHandler) handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	info := map[string]interface{}{
		"version":   Version,
		"commit":    Commit,
		"buildTime": BuildTime,
		"goVersion": runtime.Version(),
	}

	//nolint:errcheck // JSON response, ignore write error
	json.NewEncoder(w).Encode(info)
}

func (h *WebHandler) serveUploadPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	if err := h.templates.Execute(w, nil); err != nil {
		errors.LogWarn("template execute error: %v", err)
		// Don't return error to client - template errors are rare and the page
		// may have partially rendered. Just log and continue.
	}
}

func (h *WebHandler) handleUpload(w http.ResponseWriter, r *http.Request) {
	// Start tracing span for the upload request
	ctx, span := tracing.StartSpan(r.Context(), "handleUpload",
		trace.WithAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.url", r.URL.String()),
		),
	)
	defer span.End()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 200<<20) // 200 MB limit

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	reqID, _ := randomHex(4) // correlation ID for log correlation; ignore error, empty string is fine

	// Add correlation ID to tracing context
	ctx = tracing.ContextWithCorrelationID(ctx, reqID)

	jsonStr := func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	// sseMu serializes writes to w: OCR goroutines call logf concurrently and
	// http.ResponseWriter is not goroutine-safe.
	var sseMu sync.Mutex
	sendEvent := func(typ, payload string) {
		// Escape newlines so SSE frame boundaries (\n\n) are never broken.
		payload = strings.ReplaceAll(payload, "\n", " ")
		sseMu.Lock()
		defer sseMu.Unlock()
		fmt.Fprintf(w, "data: {\"type\":%s,\"message\":%s}\n\n", jsonStr(typ), jsonStr(payload))
		flusher.Flush()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		sendEvent("error", "No file uploaded")
		return
	}
	defer file.Close()

	// Stream upload to disk to avoid holding two copies in memory.
	tmpFile, err := os.CreateTemp("", "upload_*")
	if err != nil {
		sendEvent("error", "failed to create temporary file")
		return
	}
	uploadPath := tmpFile.Name()
	// Safety net: clean up the temp file if we return before the goroutine
	// takes ownership. Set goroutineTookOwnership=true to cancel the defer.
	goroutineTookOwnership := false
	defer func() {
		if !goroutineTookOwnership {
			os.Remove(uploadPath)
		}
	}()
	if _, err := io.Copy(tmpFile, file); err != nil {
		tmpFile.Close()
		sendEvent("error", "Failed to read file")
		return
	}
	tmpFile.Close()

	fi, _ := os.Stat(uploadPath)
	var fileSizeKB float64
	if fi != nil {
		fileSizeKB = float64(fi.Size()) / 1024
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		ext := strings.ToLower(filepath.Ext(header.Filename))
		mimeType = map[string]string{
			".pdf":  "application/pdf",
			".txt":  "text/plain",
			".md":   "text/markdown",
			".html": "text/html",
		}[ext]
	}

	// Process with a 10-minute timeout.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	logf := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		log.Printf("[upload %s] %s", reqID, msg)
		select {
		case <-ctx.Done():
			return // connection closed or timed out, stop writing to response
		default:
			sendEvent("log", msg)
		}
	}

	logf("File: %q (%s, %.1f KB)", header.Filename, mimeType, fileSizeKB)

	type procResult struct {
		doc *model.ProcessResult
		err error
	}
	resCh := make(chan procResult, 1)
	opts := model.ProcessOptions{
		SmartOCR:       r.FormValue("smart_ocr") == "true",
		ForceOCR:       r.FormValue("force_ocr") == "true",
		StripHeaders:   r.FormValue("strip_headers") != "false",
		StripFootnotes: r.FormValue("strip_footnotes") != "false",
		TextOnly:       r.FormValue("text_only") == "true",
	}

	goroutineTookOwnership = true
	go func() {
		defer os.Remove(uploadPath)
		doc, err := h.documentHandler.Processor.ProcessDocumentFromPath(ctx, uploadPath, mimeType, opts, logf)
		resCh <- procResult{doc, err}
	}()

	var docResult *model.ProcessResult
	select {
	case <-ctx.Done():
		sendEvent("error", "conversion timed out after 10 minutes")
		return
	case res := <-resCh:
		if res.err != nil {
			logf("Processing error: %v", res.err)
			sendEvent("error", res.err.Error())
			return
		}
		docResult = res.doc
	}

	logf("Extracted %d chars in %dms", len(docResult.ExtractedText), docResult.ProcessingMs)

	title := strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	epubResult, err := h.epubGenerator.GenerateFromText(docResult.ExtractedText, docResult.Images, docResult.PageCount, title, "Document Author")
	if err != nil {
		logf("EPUB generation error: %v", err)
		sendEvent("error", err.Error())
		return
	}
	logf("EPUB: %d chapters", len(epubResult.Chapters))

	randSuffix, err := randomHex(8)
	if err != nil {
		logf("Failed to generate filename: %v", err)
		sendEvent("error", "server error generating filename")
		return
	}
	filename := fmt.Sprintf("%s_%s.epub", sanitizeFilename(title), randSuffix)
	epubPath := filepath.Join(h.uploadDir, filename)
	if err := h.generateEPUBFile(epubResult, epubPath); err != nil {
		logf("EPUB file error: %v", err)
		os.Remove(epubPath)
		sendEvent("error", err.Error())
		return
	}

	fi, err = os.Stat(epubPath)
	if err != nil {
		logf("Failed to stat epub: %v", err)
		sendEvent("error", "Failed to save file")
		return
	}
	epubKB := float64(fi.Size()) / 1024
	logf("Done -> %s (%.1f KB)", filename, epubKB)
	type doneEvent struct {
		Type         string  `json:"type"`
		DownloadURL  string  `json:"download_url"`
		Filename     string  `json:"filename"`
		Chapters     int     `json:"chapters"`
		Chars        int     `json:"chars"`
		EpubKB       float64 `json:"epub_kb"`
		ProcessingMs int64   `json:"processing_ms"`
	}
	doneJSON, err := json.Marshal(doneEvent{
		Type:         "done",
		DownloadURL:  "/download?file=" + url.QueryEscape(filename),
		Filename:     filename,
		Chapters:     len(epubResult.Chapters),
		Chars:        len(docResult.ExtractedText),
		EpubKB:       epubKB,
		ProcessingMs: docResult.ProcessingMs,
	})
	if err != nil {
		logf("JSON marshal error: %v", err)
		sendEvent("error", "internal server error")
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", doneJSON)
	flusher.Flush()
}

// sanitizeFilename strips directory components from name and replaces any
// character that is not alphanumeric, a hyphen, or a dot with an underscore.
// This prevents path traversal when the result is used in filepath.Join.
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	if s := b.String(); s != "" && s != "." {
		return s
	}
	return "document"
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (h *WebHandler) handleDownload(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("file")
	if filename == "" {
		http.Error(w, "Missing filename", http.StatusBadRequest)
		return
	}

	// Validate and sanitize the filename
	validatedPath, err := h.validateDownloadPath(filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f, err := os.Open(validatedPath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	safeName := filepath.Base(validatedPath)
	w.Header().Set("Content-Type", "application/epub+zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", safeName))
	http.ServeContent(w, r, safeName, fi.ModTime(), f)
}

// validateDownloadPath performs comprehensive path traversal protection.
// It returns the validated absolute path or an error if the path is invalid.
func (h *WebHandler) validateDownloadPath(filename string) (string, error) {
	// Step 0: Check for path traversal attempts and absolute paths in the original filename
	// This catches both raw ".." and URL-encoded "%2e%2e" (which becomes ".." after HTTP decoding)
	// Also rejects absolute paths which could bypass Base() checks
	if strings.Contains(filename, "..") || strings.Contains(filename, "%2e%2e") || filepath.IsAbs(filename) {
		return "", fmt.Errorf("invalid file")
	}

	// Step 1: Strip directory components using filepath.Base
	safeName := filepath.Base(filename)

	// Step 2: Explicit check for ".." sequences (defense in depth)
	// This catches attempts like "../etc/passwd" or "foo/../../bar"
	if strings.Contains(safeName, "..") {
		return "", fmt.Errorf("invalid file")
	}

	// Step 3: Verify extension is .epub only
	if filepath.Ext(safeName) != ".epub" {
		return "", fmt.Errorf("invalid file")
	}

	// Step 4: Build the full path
	filePath := filepath.Join(h.uploadDir, safeName)

	// Step 5: Canonicalize the path to resolve any symlinks or relative components
	// filepath.Clean normalizes the path (removes redundant separators, etc.)
	cleanPath := filepath.Clean(filePath)

	// Step 6: Canonicalize the base directory for comparison
	cleanBase := filepath.Clean(h.uploadDir)

	// Step 7: Verify the resolved path is within the allowed directory
	// Use explicit prefix check with trailing separator to prevent partial matches
	// e.g., "/uploads/evil" should not match prefix "/uploads/ev"
	if !strings.HasPrefix(cleanPath+string(filepath.Separator), cleanBase+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid file")
	}

	// Step 8: Additional check - ensure the final path doesn't contain ".." after cleaning
	if strings.Contains(cleanPath, "..") {
		return "", fmt.Errorf("invalid file")
	}

	return cleanPath, nil
}

// epubZip wraps zip.Writer and propagates the first write error so callers
// only need to check once at the end via zw.Close().
type epubZip struct {
	zw  *zip.Writer
	err error
}

func (z *epubZip) create(name string) io.Writer {
	if z.err != nil {
		return io.Discard
	}
	w, err := z.zw.Create(name)
	if err != nil {
		z.err = err
		return io.Discard
	}
	return w
}

func (z *epubZip) createHeader(h *zip.FileHeader) io.Writer {
	if z.err != nil {
		return io.Discard
	}
	w, err := z.zw.CreateHeader(h)
	if err != nil {
		z.err = err
		return io.Discard
	}
	return w
}

func (z *epubZip) write(name string, data []byte) {
	if z.err != nil {
		return
	}
	w, err := z.zw.Create(name)
	if err != nil {
		z.err = err
		return
	}
	if _, err := w.Write(data); err != nil {
		z.err = err
	}
}

func (z *epubZip) close() error {
	if z.err != nil {
		return z.err
	}
	return z.zw.Close()
}

func (h *WebHandler) generateEPUBFile(epubResult *model.EPUBResult, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	z := &epubZip{zw: zip.NewWriter(f), err: nil}

	// mimetype must be the first entry and stored uncompressed per EPUB spec.
	fmt.Fprintf(z.createHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store}), "application/epub+zip")

	fmt.Fprintf(z.create("META-INF/container.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`)

	chapters := epubResult.Chapters

	// Collect all images across all chapters for the OPF manifest.
	var allImages []model.PDFImage
	for _, ch := range chapters {
		allImages = append(allImages, ch.Images...)
	}

	// Build manifest items, spine itemrefs, and NCX navPoints for each chapter.
	var manifest, spine, navMap strings.Builder
	manifest.WriteString("    <item id=\"ncx\" href=\"toc.ncx\" media-type=\"application/x-dtbncx+xml\"/>\n")
	for i, ch := range chapters {
		id := fmt.Sprintf("chapter%d", i+1)
		href := fmt.Sprintf("chapter%d.xhtml", i+1)
		fmt.Fprintf(&manifest, "    <item id=\"%s\" href=\"%s\" media-type=\"application/xhtml+xml\"/>\n", id, href)
		fmt.Fprintf(&spine, "    <itemref idref=\"%s\"/>\n", id)
		fmt.Fprintf(&navMap,
			"    <navPoint id=\"navpoint-%d\" playOrder=\"%d\">\n      <navLabel><text>%s</text></navLabel>\n      <content src=\"%s\"/>\n    </navPoint>\n",
			i+1, i+1, template.HTMLEscapeString(ch.Title), href)
	}
	// Add image items to manifest.
	for _, img := range allImages {
		fmt.Fprintf(&manifest, "    <item id=\"%s\" href=\"images/%s\" media-type=\"%s\"/>\n",
			strings.TrimSuffix(img.Name, filepath.Ext(img.Name)), img.Name, img.MimeType)
	}

	fmt.Fprintf(z.create("OEBPS/content.opf"), `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>%s</dc:title>
    <dc:creator>%s</dc:creator>
    <dc:language>%s</dc:language>
    <dc:identifier id="bookid">%s</dc:identifier>
  </metadata>
  <manifest>
%s  </manifest>
  <spine toc="ncx">
%s  </spine>
</package>`,
		template.HTMLEscapeString(epubResult.Title),
		template.HTMLEscapeString(epubResult.Author),
		epubResult.Language,
		template.HTMLEscapeString(epubResult.Title),
		manifest.String(), spine.String())

	fmt.Fprintf(z.create("OEBPS/toc.ncx"), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE ncx PUBLIC "-//NISO//DTD ncx 2005-1//EN" "http://www.daisy.org/z3986/2005/ncx-2005-1.dtd">
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head><meta name="dtb:uid" content="bookid"/></head>
  <navMap>
%s  </navMap>
</ncx>`, navMap.String())

	// Write image files into OEBPS/images/.
	for _, img := range allImages {
		z.write("OEBPS/images/"+img.Name, img.Data)
	}

	// Write each chapter as a separate XHTML document.
	for i, ch := range chapters {
		// Build figNum → image map for inline insertion.
		type figEntry struct {
			name          string
			widthFraction float64
		}
		figMap := make(map[int]figEntry)
		for _, img := range ch.Images {
			if img.FigNum > 0 {
				figMap[img.FigNum] = figEntry{img.Name, img.WidthFraction}
			}
		}

		bodyHTML := textToHTML(ch.Content)

		imgStyle := func(wf float64) string {
			if wf > 0 && wf < 0.99 {
				return fmt.Sprintf(` style="width:%.0f%%"`, wf*100)
			}
			return ""
		}

		// Insert figure images inline: collect all insertion points first (one
		// scan per figure), then apply in reverse position order so earlier
		// offsets are not shifted by subsequent insertions.
		type figInsertion struct {
			pos   int
			block string
		}
		var insertions []figInsertion
		for figNum, fe := range figMap {
			imgBlock := fmt.Sprintf(
				`<div class="fig"><img src="images/%s"%s alt="Fig. %d"/></div>`,
				template.HTMLEscapeString(fe.name), imgStyle(fe.widthFraction), figNum)

			// Candidates in priority order: caption-start paragraph first,
			// then any paragraph that mentions the figure number.
			candidates := []string{
				fmt.Sprintf("<p>Fig. %d", figNum),
				fmt.Sprintf("<p>Fig %d", figNum),
				fmt.Sprintf("<p>fig. %d", figNum),
				fmt.Sprintf("Fig. %d", figNum),
				fmt.Sprintf("Fig %d", figNum),
			}
			for _, needle := range candidates {
				idx := strings.Index(bodyHTML, needle)
				if idx < 0 {
					continue
				}
				var pStart int
				if strings.HasPrefix(needle, "<p>") {
					pStart = idx
				} else {
					pStart = strings.LastIndex(bodyHTML[:idx], "<p>")
					if pStart < 0 {
						pStart = idx
					}
				}
				insertions = append(insertions, figInsertion{pStart, imgBlock})
				break
			}
		}
		// Apply insertions in a single pass (ascending offset order) to avoid
		// O(n²) string allocation from repeated slicing.
		sort.Slice(insertions, func(i, j int) bool {
			return insertions[i].pos < insertions[j].pos
		})
		var spliced strings.Builder
		spliced.Grow(len(bodyHTML))
		prev := 0
		for _, ins := range insertions {
			spliced.WriteString(bodyHTML[prev:ins.pos])
			spliced.WriteString(ins.block)
			prev = ins.pos
		}
		spliced.WriteString(bodyHTML[prev:])
		bodyHTML = spliced.String()

		// Append any images that couldn't be placed inline (no matching caption
		// found in text) as a fallback gallery at chapter end.
		var fallback strings.Builder
		for _, img := range ch.Images {
			style := imgStyle(img.WidthFraction)
			if img.FigNum == 0 {
				fmt.Fprintf(&fallback, `<div class="fig"><img src="images/%s"%s alt=""/></div>`,
					template.HTMLEscapeString(img.Name), style)
			} else if !strings.Contains(bodyHTML, "images/"+img.Name) {
				fmt.Fprintf(&fallback, `<div class="fig"><img src="images/%s"%s alt="Fig. %d"/></div>`,
					template.HTMLEscapeString(img.Name), style, img.FigNum)
			}
		}

		fmt.Fprintf(z.create(fmt.Sprintf("OEBPS/chapter%d.xhtml", i+1)),
			`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.1//EN" "http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>%s</title><style type="text/css">div.fig{text-align:center;margin:1.5em 0;}img{max-width:100%%;height:auto;}</style></head>
<body>
<h2>%s</h2>
%s%s
</body>
</html>`, template.HTMLEscapeString(ch.Title), template.HTMLEscapeString(ch.Title),
			bodyHTML, fallback.String())
	}

	return z.close()
}

// textToHTML converts plain text from pdftotext/OCR into XHTML paragraphs.
// It handles:
//   - Soft-hyphenated line breaks (­\n or -\n at line end) → joined without space
//   - Sentence-ending lines (ending with . ? ! " ") followed by a new paragraph-start
//     (capital/quote/„) → split into separate <p> elements
//   - All other single newlines → space (rejoin wrapped lines)
//   - Explicit double-newline gaps → paragraph break
func textToHTML(text string) string {
	// 1. Join soft-hyphenated breaks: "wor­\nld" → "world", "wor-\nld" → "world"
	text = strings.ReplaceAll(text, "\u00ad\n", "") // soft hyphen
	text = strings.ReplaceAll(text, "-\n", "-")

	// 2. Split into lines and detect paragraph boundaries.
	lines := strings.Split(text, "\n")
	var paragraphs []string
	var current strings.Builder

	sentenceEnd := func(s string) bool {
		if s == "" {
			return false
		}
		runes := []rune(s)
		last := runes[len(runes)-1]
		return last == '.' || last == '?' || last == '!' || last == '"' || last == '\'' || last == '\u201d' || last == '»'
	}
	paraStart := func(s string) bool {
		if s == "" {
			return false
		}
		r := []rune(s)
		first := r[0]
		return first == '„' || first == '"' || first == '—' || first == '-' ||
			(first >= 'A' && first <= 'Z') ||
			(first >= 'А' && first <= 'Я') || // Cyrillic uppercase
			(first >= 0x00C0 && first <= 0x024F) // Latin extended uppercase (Č Š Ž Ć Đ…)
	}

	flush := func() {
		if s := strings.TrimSpace(current.String()); s != "" {
			paragraphs = append(paragraphs, s) // unescaped; escape happens at render time
		}
		current.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Blank line → always a paragraph break
		if trimmed == "" {
			flush()
			continue
		}

		if current.Len() > 0 {
			// Decide: continue current paragraph or start a new one?
			prev := strings.TrimSpace(current.String())
			if sentenceEnd(prev) && paraStart(trimmed) {
				flush()
			} else {
				current.WriteString(" ")
			}
		}
		current.WriteString(trimmed)
	}
	flush()

	// For OCR output there are no blank lines between paragraphs, so the
	// heuristic above can miss sentence boundaries that fall mid-line. Split
	// any paragraph that accumulated too many sentences into smaller chunks.
	paragraphs = splitLongParagraphs(paragraphs, 4)

	if len(paragraphs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range paragraphs {
		b.WriteString("<p>")
		b.WriteString(template.HTMLEscapeString(p))
		b.WriteString("</p>")
	}
	return b.String()
}

// splitLongParagraphs splits any paragraph that contains more than maxSentences
// sentence-ending punctuation marks into multiple paragraphs. Splitting occurs
// at ". "/? "/! " boundaries followed by an uppercase letter, grouping every
// maxSentences sentences into one paragraph.
func splitLongParagraphs(paragraphs []string, maxSentences int) []string {
	out := make([]string, 0, len(paragraphs))
	for _, para := range paragraphs {
		out = append(out, splitAtSentenceBoundaries(para, maxSentences)...)
	}
	return out
}

// minParaLenToSplit is the minimum byte length a paragraph must exceed before
// sentence-boundary splitting is attempted. Short paragraphs are fine as-is.
const minParaLenToSplit = 350

func splitAtSentenceBoundaries(para string, maxSentences int) []string {
	if len(para) < minParaLenToSplit {
		return []string{para}
	}
	runes := []rune(para)
	n := len(runes)
	var result []string
	start := 0
	sentences := 0
	for i := 0; i < n; i++ {
		r := runes[i]
		if r == '.' || r == '?' || r == '!' {
			sentences++
			// Split after maxSentences sentences when followed by " [UpperLetter]".
			if sentences >= maxSentences && i+2 < n && runes[i+1] == ' ' && isUpperStart(runes[i+2]) {
				result = append(result, strings.TrimSpace(string(runes[start:i+1])))
				start = i + 2 // skip the space
				sentences = 0
			}
		}
	}
	if tail := strings.TrimSpace(string(runes[start:])); tail != "" {
		result = append(result, tail)
	}
	if len(result) == 0 {
		return []string{para}
	}
	return result
}

// isUpperStart returns true for characters that can open a new sentence after
// a paragraph split: uppercase ASCII/Latin-extended/Cyrillic letters, quotes,
// and dash characters.
func isUpperStart(r rune) bool {
	return (r >= 'A' && r <= 'Z') ||
		(r >= 'А' && r <= 'Я') || // Cyrillic uppercase
		(r >= 0x00C0 && r <= 0x024F) || // Latin extended (Č Š Ž Ć Đ…)
		r == '"' || r == '„' || r == '—' || r == '-'
}

func (h *WebHandler) Close() error {
	if !h.persistentDir {
		os.RemoveAll(h.uploadDir)
	}
	if h.epubGenerator != nil {
		h.epubGenerator.Close()
	}
	return nil
}
