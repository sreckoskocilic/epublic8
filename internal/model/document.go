package model

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"epublic8/internal/errors"
	"epublic8/internal/metrics"
	"epublic8/internal/tracing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// defaultOCRConcurrency returns the default OCR concurrency limit.
// Defaults to GOMAXPROCS; override via NewDocumentProcessor.
func defaultOCRConcurrency() int {
	return max(1, runtime.GOMAXPROCS(0))
}

// visionOCR is the lazily-compiled macOS Vision framework OCR binary.
// It is initialised once via visionOCROnce on the first OCR request.
var (
	visionOCROnce sync.Once
	visionOCRBin  string // empty string means Vision OCR is unavailable
)

// initVisionOCR compiles ocr/vision_ocr.swift with swiftc (macOS only).
// On success visionOCRBin is set to the path of the compiled binary.
// Failures are silently ignored so Tesseract remains the fallback.
func initVisionOCR() {
	if runtime.GOOS != "darwin" {
		return
	}
	// Find swiftc.
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		return
	}
	// Search candidate root directories for ocr/vision_ocr.swift.
	// We check both the working directory and the directory of the running
	// executable (walking up in each case), so the search works for both
	// `go run` invocations (where the binary lives in a temp dir) and deployed
	// binaries sitting next to the project root.
	roots := []string{}
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(exe))
	}
	src := ""
	for _, root := range roots {
		for dir := root; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
			candidate := filepath.Join(dir, "ocr", "vision_ocr.swift")
			if _, err := os.Stat(candidate); err == nil {
				src = candidate
				break
			}
		}
		if src != "" {
			break
		}
	}
	if src == "" {
		return
	}
	// Compile into a temp binary that lives for the process lifetime.
	binFile, err := os.CreateTemp("", "vision_ocr_*")
	if err != nil {
		return
	}
	binPath := binFile.Name()
	binFile.Close()
	os.Remove(binPath) // swiftc will create it

	compileCtx, compileCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer compileCancel()
	cmd := exec.CommandContext(compileCtx, swiftc, "-O", src, "-o", binPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Log compilation failure at WARN level - Tesseract remains the fallback.
		errors.LogWarn("Vision OCR compilation failed: %s (using Tesseract fallback)", string(out))
		os.Remove(binPath)
		return
	}
	visionOCRBin = binPath
}

// LogToolAvailability logs which external tools (pdftotext, tesseract, etc.)
// are present on PATH. Call once at server startup for operational visibility.
func LogToolAvailability(logf func(string, ...any)) {
	tools := []string{"pdftotext", "pdffonts", "pdfinfo", "pdfimages", "pdftoppm", "tesseract"}
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			logf("tool unavailable: %s (some features may be degraded)", t)
		} else {
			logf("tool available: %s", t)
		}
	}
	visionOCROnce.Do(initVisionOCR)
	if visionOCRBin != "" {
		logf("tool available: vision_ocr (macOS native OCR)")
	} else {
		logf("tool unavailable: vision_ocr (Tesseract will be used for OCR)")
	}
}

// tesseractLangToVision maps Tesseract language codes to BCP-47 tags accepted
// by the macOS Vision framework. Croatian ("hr") is the closest supported
// language to Serbian Latin; it covers the same diacritic set.
var tesseractLangToVision = map[string]string{
	"srp_latn+hrv": "hr",
	"srp_latn":     "hr",
	"hrv":          "hr",
	"eng":          "en-US",
}

// extractImageTextVision runs the Vision OCR binary on imgPath and returns
// only the recognised text. Use runVisionOCR (in figures.go) when bounding
// boxes are also needed (e.g., figure detection).
func extractImageTextVision(ctx context.Context, imgPath, tesseractLang string) string {
	return runVisionOCR(ctx, imgPath, tesseractLang).Text
}

// footnoteMarkerRe matches lines that begin a footnote section. Three forms:
//
//	"1"       — standalone digit (pdftotext separates marker from text)
//	"8Cf."    — digit glued directly to capital letter (Tesseract OCR)
//	"14 Of"   — digit + space + capital letter (Tesseract OCR)
var footnoteMarkerRe = regexp.MustCompile(`^\d{1,3}(\s*$|\s*[A-Z])`)

// ProcessOptions controls how a document is processed.
type ProcessOptions struct {
	SmartOCR       bool // detect encoding issues / scan fonts; fall back to OCR
	ForceOCR       bool // always render pages via OCR, skip embedded text entirely
	StripHeaders   bool // remove running headers from paginated PDFs
	StripFootnotes bool // strip footnote sections from page bottoms
	StripCaptions   bool          // remove art image descriptions and sidebar notes (e.g. LIFELINE)
	IncludeClusters []int         // cluster IDs to include; nil = no cluster filtering
	ClusterDefs     []FontCluster // cluster definitions from analyze step
	TextOnly        bool          // skip embedded image extraction
	PageFrom       int  // first page to process (1-based, 0 = start)
	PageTo         int  // last page to process (1-based, 0 = end)
}

// DefaultProcessOptions returns sensible defaults: smart-OCR on, headers and
// footnotes stripped, images extracted, force-OCR off.
func DefaultProcessOptions() ProcessOptions {
	return ProcessOptions{
		SmartOCR:        true,
		StripHeaders:    true,
		StripFootnotes:  true,
		ForceOCR:        false,
		StripCaptions:   false,
		IncludeClusters: nil,
		ClusterDefs:     nil,
		TextOnly:        false,
		PageFrom:        0,
		PageTo:          0,
	}
}

type DocumentProcessor struct {
	// OCRConcurrencyLimit bounds the total concurrent OCR page workers across
	// all active conversions. If zero, defaults to GOMAXPROCS.
	OCRConcurrencyLimit int

	// OCRLanguages is the ordered list of language codes for OCR (Vision and Tesseract).
	// The first language in the list that is supported by the OCR system will be used.
	// If empty, defaults to ["srp_latn+hrv", "srp_latn", "eng"].
	OCRLanguages []string
}

// PDFImage holds a single image extracted from a PDF.
type PDFImage struct {
	Name          string // filename used inside the EPUB (e.g. "img001.png")
	Data          []byte
	MimeType      string  // "image/jpeg" or "image/png"
	PageNum       int     // 1-based page number in the source PDF
	FigNum        int     // figure number (e.g. 82 for "Fig. 82"); 0 if not a figure
	Caption       string  // figure caption text; empty if not a figure
	WidthFraction float64 // fraction of page width (0..1]; 0 means unset (full width)
}

// extractPDFImages uses pdfimages to extract embedded images from pdfPath.
// Returns nil when pdfimages is not available, on any error, or when all
// images are smaller than 64×64 px (icons / bullets / noise).
func extractPDFImages(ctx context.Context, pdfPath string, opts ProcessOptions, logf func(string, ...any)) []PDFImage {
	// List images first to obtain page numbers and dimensions.
	listArgs := []string{"-list"}
	if opts.PageFrom > 0 {
		listArgs = append(listArgs, "-f", strconv.Itoa(opts.PageFrom))
	}
	if opts.PageTo > 0 {
		listArgs = append(listArgs, "-l", strconv.Itoa(opts.PageTo))
	}
	listArgs = append(listArgs, pdfPath)
	listOut, err := exec.CommandContext(ctx, "pdfimages", listArgs...).Output()
	if err != nil {
		logf("pdfimages -list failed: %v", err)
		return nil
	}
	lines := strings.Split(string(listOut), "\n")
	// Need header + separator + at least one data line.
	if len(lines) < 3 {
		return nil
	}
	type imgMeta struct{ pageNum, w, h, bpc int }
	var metas []imgMeta
	for _, line := range lines[2:] {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		page, e1 := strconv.Atoi(fields[0])
		w, e2 := strconv.Atoi(fields[3])
		h, e3 := strconv.Atoi(fields[4])
		bpc, e4 := strconv.Atoi(fields[7])
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			continue
		}
		metas = append(metas, imgMeta{page, w, h, bpc})
	}
	if len(metas) == 0 {
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "pdf_imgs_*")
	if err != nil {
		logf("extractPDFImages: failed to create temp dir: %v", err)
		return nil
	}
	defer os.RemoveAll(tmpDir)

	prefix := filepath.Join(tmpDir, "img")
	extractArgs := []string{"-png"}
	if opts.PageFrom > 0 {
		extractArgs = append(extractArgs, "-f", strconv.Itoa(opts.PageFrom))
	}
	if opts.PageTo > 0 {
		extractArgs = append(extractArgs, "-l", strconv.Itoa(opts.PageTo))
	}
	extractArgs = append(extractArgs, pdfPath, prefix)
	if err := exec.CommandContext(ctx, "pdfimages", extractArgs...).Run(); err != nil {
		logf("pdfimages -png failed: %v", err)
		return nil
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		logf("extractPDFImages: failed to read temp dir: %v", err)
		return nil
	}
	// Sort entries so their order matches the sequential index from pdfimages.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var images []PDFImage
	metaIdx := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		var mimeType string
		switch ext {
		case ".png":
			mimeType = "image/png"
		case ".jpg", ".jpeg":
			mimeType = "image/jpeg"
		default:
			metaIdx++
			continue // skip PPM/PBM that slipped through
		}
		// Skip tiny images (icons, bullets, noise) and 1-bit black-and-white
		// bitmaps. bpc=1 images in PDFs are almost always scan artifacts
		// (borders, formula outlines, stray marks) rather than real content.
		if metaIdx < len(metas) {
			m := metas[metaIdx]
			if m.w < 64 || m.h < 64 || m.bpc == 1 {
				metaIdx++
				continue
			}
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, entry.Name()))
		if err != nil {
			metaIdx++
			continue
		}
		pageNum := 1
		if metaIdx < len(metas) {
			pageNum = metas[metaIdx].pageNum
		}
		epubName := fmt.Sprintf("img%03d%s", len(images)+1, ext)
		images = append(images, PDFImage{
			Name:          epubName,
			Data:          data,
			MimeType:      mimeType,
			PageNum:       pageNum,
			FigNum:        0,
			Caption:       "",
			WidthFraction: 0,
		})
		metaIdx++
	}
	if len(images) > 0 {
		logf("Extracted %d images from PDF", len(images))
	}
	return images
}

// ProcessResult holds the output of document processing.
type ProcessResult struct {
	ExtractedText string
	PageCount     int
	Images        []PDFImage // populated by ProcessDocumentFromPath for PDFs; nil from ProcessDocument
	ProcessingMs  int64
	Language      string // BCP-47 language tag derived from OCR language config
}

// ocrLangToBCP47 converts a Tesseract language code to a BCP-47 tag.
func ocrLangToBCP47(lang string) string {
	// Combined codes like "srp_latn+hrv" → use first part.
	if idx := strings.Index(lang, "+"); idx >= 0 {
		lang = lang[:idx]
	}
	switch lang {
	case "srp_latn", "hrv":
		return "hr"
	case "srp":
		return "sr"
	case "eng":
		return "en"
	default:
		return "und"
	}
}

// defaultOCRLanguages returns the default OCR languages if none specified.
func defaultOCRLanguages() []string {
	return []string{"srp_latn+hrv", "srp_latn", "eng"}
}

func NewDocumentProcessor(ocrConcurrencyLimit int, ocrLanguages []string) *DocumentProcessor {
	// Vision OCR binary is compiled lazily via visionOCROnce in LogToolAvailability.
	limit := ocrConcurrencyLimit
	if limit <= 0 {
		limit = defaultOCRConcurrency()
	}
	languages := ocrLanguages
	if len(languages) == 0 {
		languages = defaultOCRLanguages()
	}
	return &DocumentProcessor{
		OCRConcurrencyLimit: limit,
		OCRLanguages:        languages,
	}
}

func (p *DocumentProcessor) ProcessDocument(ctx context.Context, content []byte, mimeType string, opts ProcessOptions, logf func(string, ...any)) (*ProcessResult, error) {
	ctx, span := tracing.StartSpan(ctx, "ProcessDocument",
		trace.WithAttributes(
			attribute.String("document.mime_type", mimeType),
			attribute.Int("document.size_bytes", len(content)),
		),
	)
	defer span.End()

	start := time.Now()
	lang := "und"
	if len(p.OCRLanguages) > 0 {
		lang = ocrLangToBCP47(p.OCRLanguages[0])
	}
	result := &ProcessResult{
		ExtractedText: p.extractText(ctx, content, mimeType, opts, logf),
		PageCount:     1,
		Language:      lang,
		ProcessingMs:  time.Since(start).Milliseconds(),
		Images:        nil,
	}
	span.SetAttributes(
		attribute.Int("result.text_length", len(result.ExtractedText)),
		attribute.Int64("result.processing_ms", result.ProcessingMs),
	)
	return result, nil
}

func (p *DocumentProcessor) extractText(ctx context.Context, content []byte, mimeType string, opts ProcessOptions, logf func(string, ...any)) string {
	switch mimeType {
	case "application/pdf":
		return p.extractPDFText(ctx, content, opts, logf)
	case "image/png", "image/jpeg", "image/tiff":
		return p.extractImageText(ctx, content, logf)
	case "text/plain", "text/markdown", "text/html", "application/octet-stream":
		return string(content)
	default:
		logf("extractText: unrecognised MIME type %q, treating as plain text", mimeType)
		return string(content)
	}
}

// writeTempFile writes content to a new temp file with the given name pattern
// and returns its path. The caller must os.Remove the path when done.
func writeTempFile(pattern string, content []byte, logf func(string, ...any)) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.Write(content); err != nil {
		if cerr := f.Close(); cerr != nil {
			logf("warning: failed to close temp file after write error: %v", cerr)
		}
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		logf("warning: failed to close temp file: %v", err)
	}
	return path, nil
}

// runTesseractOCR tries each language in langs against imgPath using Tesseract.
// Returns the first non-empty result and the language used, or ("", "") on failure.
func runTesseractOCR(ctx context.Context, imgPath string, langs []string, logf func(string, ...any)) (text, usedLang string) {
	ocrBase := imgPath + "_tess"
	for _, lang := range langs {
		cmd := exec.CommandContext(ctx, "tesseract", imgPath, ocrBase, "-l", lang)
		// Pin Tesseract/OpenMP to one thread per worker; concurrency is already
		// bounded by the OCR semaphore, so per-process threads would oversubscribe.
		cmd.Env = append(os.Environ(), "OMP_NUM_THREADS=1")
		if cmd.Run() != nil {
			continue
		}
		data, err := os.ReadFile(ocrBase + ".txt")
		if rerr := os.Remove(ocrBase + ".txt"); rerr != nil && !os.IsNotExist(rerr) {
			if logf != nil {
				logf("warning: failed to remove OCR tmp file: %v", rerr)
			}
		}
		if err != nil {
			if logf != nil {
				logf("warning: failed to read OCR output: %v", err)
			}
			continue
		}
		if strings.TrimSpace(string(data)) != "" {
			return string(data), lang
		}
	}
	return "", ""
}

func (p *DocumentProcessor) extractPDFText(ctx context.Context, content []byte, opts ProcessOptions, logf func(string, ...any)) string {
	path, err := writeTempFile("pdf_*.pdf", content, logf)
	if err != nil {
		errors.LogWarn("extractPDFText: failed to write temp file: %v", err)
		return "[PDF Error: failed to write temporary file]"
	}
	defer os.Remove(path)
	text, _, _ := p.extractPDFTextFromPath(ctx, path, opts, logf)
	return text
}

// extractImageText OCRs a raw image (PNG/JPEG/TIFF) by writing it to a temp
// file and running Vision OCR (macOS) or Tesseract as fallback.
func (p *DocumentProcessor) extractImageText(ctx context.Context, content []byte, logf func(string, ...any)) string {
	path, err := writeTempFile("img_ocr_*", content, logf)
	if err != nil {
		errors.LogWarn("extractImageText: failed to write temp file: %v", err)
		return "[Image OCR Error: failed to write temporary file]"
	}
	defer os.Remove(path)

	for _, lang := range p.OCRLanguages {
		if t := extractImageTextVision(ctx, path, lang); t != "" {
			logf("Image OCR engine=vision:%s chars=%d", lang, len(t))
			return t
		}
	}

	// Tesseract fallback.
	if t, lang := runTesseractOCR(ctx, path, p.OCRLanguages, logf); t != "" {
		logf("Image OCR engine=tesseract:%s chars=%d", lang, len(t))
		return t
	}
	return "[Image OCR: no text recognised]"
}

// scanFontRe matches the auto-generated font-subset names that macOS Quartz and
// Adobe Acrobat emit when baking OCR output into a searchable scan PDF.
// Format: six uppercase letters + "+" + arbitrary suffix (e.g. "AAAAAB+Fd431742").
var scanFontRe = regexp.MustCompile(`^[A-Z]{6}\+`)

// parsePDFFonts runs pdffonts on pdfPath once and returns:
//   - hasUnmapped: any font uses Custom encoding with no ToUnicode map (pdftotext will garble)
//   - likelyScan:  all named fonts use auto-generated subset names (scan-to-PDF re-OCR candidate)
func parsePDFFonts(ctx context.Context, pdfPath string) (hasUnmapped, likelyScan bool) {
	out, err := exec.CommandContext(ctx, "pdffonts", pdfPath).Output()
	if err != nil {
		return false, false
	}
	lines := strings.Split(string(out), "\n")
	// Need at least: header line, separator line, one data line.
	if len(lines) < 3 {
		return false, false
	}
	// Derive column start positions from the separator line (groups of dashes).
	// Columns: name(0) type(1) encoding(2) emb(3) sub(4) uni(5) object(6)
	sep := lines[1]
	var colStarts []int
	inDash := false
	for i, c := range sep {
		if c == '-' && !inDash {
			colStarts = append(colStarts, i)
			inDash = true
		} else if c != '-' {
			inDash = false
		}
	}
	if len(colStarts) < 2 {
		return false, false
	}
	nameEnd := colStarts[1]
	canCheckEnc := len(colStarts) >= 7
	var encStart, encEnd, uniStart int
	if canCheckEnc {
		encStart, encEnd, uniStart = colStarts[2], colStarts[3], colStarts[5]
	}
	total, scan := 0, 0
	for _, line := range lines[2:] {
		if len(line) < nameEnd {
			continue
		}
		name := strings.TrimSpace(line[:nameEnd])
		if name == "" {
			continue
		}
		total++
		if scanFontRe.MatchString(name) {
			scan++
		}
		if canCheckEnc && !hasUnmapped && len(line) > uniStart {
			enc := strings.TrimSpace(line[encStart:min(encEnd, len(line))])
			uni := strings.TrimSpace(line[uniStart:min(uniStart+3, len(line))])
			if enc == "Custom" && uni == "no" {
				hasUnmapped = true
			}
		}
	}
	likelyScan = total > 0 && scan == total
	// Single font of any kind = likely OCR layer (real books use multiple fonts).
	if !likelyScan && total == 1 {
		likelyScan = true
	}
	return hasUnmapped, likelyScan
}

// extractPDFTextFromPath returns the text, any embedded images, and the page
// count from pdfPath. Page count is 0 on the pdftotext path (caller must call
// pdfPageCount separately); it is set when OCR is used.
func (p *DocumentProcessor) extractPDFTextFromPath(ctx context.Context, pdfPath string, opts ProcessOptions, logf func(string, ...any)) (string, []PDFImage, int) {
	runOCR := func() (string, []PDFImage, int) {
		if ocr, figs, n := p.extractPDFTextOCR(ctx, pdfPath, opts, logf); ocr != "" {
			logf("OCR complete (%d chars)", len(ocr))
			return p.processFootnotes(ocr, opts), figs, n
		}
		logf("OCR produced no output")
		return "", nil, 0
	}

	if opts.ForceOCR {
		logf("Force OCR enabled, skipping embedded text")
		return runOCR()
	}

	if len(opts.IncludeClusters) > 0 && len(opts.ClusterDefs) > 0 {
		logf("Cluster filtering enabled, using OCR for bounding-box data")
		return runOCR()
	}

	if opts.SmartOCR {
		hasUnmapped, likelyScan := parsePDFFonts(ctx, pdfPath)
		if hasUnmapped {
			logf("Custom-encoded fonts detected, skipping pdftotext")
			return runOCR()
		}
		if likelyScan {
			logf("Scan-based PDF detected (embedded OCR fonts), re-rendering pages for fresh OCR")
			if text, figs, n := runOCR(); text != "" {
				return text, figs, n
			}
			logf("OCR produced no output, falling back to pdftotext")
		}
	}

	outFile, err := os.CreateTemp("", "pdftext_*.txt")
	if err != nil {
		return fmt.Sprintf("[PDF Error: %v]", err), nil, 0
	}
	outputFile := outFile.Name()
	outFile.Close()
	defer os.Remove(outputFile)

	pdftextArgs := []string{}
	if opts.PageFrom > 0 {
		pdftextArgs = append(pdftextArgs, "-f", strconv.Itoa(opts.PageFrom))
	}
	if opts.PageTo > 0 {
		pdftextArgs = append(pdftextArgs, "-l", strconv.Itoa(opts.PageTo))
	}
	pdftextArgs = append(pdftextArgs, pdfPath, outputFile)
	cmd := exec.CommandContext(ctx, "pdftotext", pdftextArgs...)
	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("[PDF Error: %v]", err), nil, 0
	}

	text, err := os.ReadFile(outputFile)
	if err != nil {
		return fmt.Sprintf("[PDF Error: %v]", err), nil, 0
	}

	extracted := string(text)
	needOCR := strings.TrimSpace(extracted) == "" || p.hasGarbledCEEncoding(extracted)
	if needOCR {
		if strings.TrimSpace(extracted) == "" {
			logf("pdftotext returned empty text (image-only PDF?), falling back to OCR")
		} else {
			logf("Garbled encoding detected, falling back to OCR")
		}
		if text, figs, n := runOCR(); text != "" {
			return text, figs, n
		}
		logf("OCR produced no output, using pdftotext result")
		return p.processFootnotes(extracted, opts), nil, 0
	}

	// Native-text PDF: extract embedded images unless text-only mode.
	var images []PDFImage
	if !opts.TextOnly {
		images = extractPDFImages(ctx, pdfPath, opts, logf)
	}
	return p.processFootnotes(extracted, opts), images, 0
}

// ProcessDocumentFromPath processes a file already on disk.
// For PDFs this avoids writing content to a second temp file.
// smartOCR enables upfront font-encoding detection via pdffonts to skip
// directly to OCR when Custom-encoded fonts are found.
func (p *DocumentProcessor) ProcessDocumentFromPath(ctx context.Context, filePath, mimeType string, opts ProcessOptions, logf func(string, ...any)) (*ProcessResult, error) {
	ctx, span := tracing.StartSpan(ctx, "ProcessDocumentFromPath",
		trace.WithAttributes(
			attribute.String("document.mime_type", mimeType),
			attribute.String("document.path", filePath),
			attribute.Bool("ocr.smart", opts.SmartOCR),
			attribute.Bool("ocr.force", opts.ForceOCR),
		),
	)
	defer span.End()

	start := time.Now()
	var extractedText string
	var images []PDFImage
	pageCount := 1
	if mimeType == "application/pdf" {
		var ocrPageCount int
		extractedText, images, ocrPageCount = p.extractPDFTextFromPath(ctx, filePath, opts, logf)
		if ocrPageCount > 0 {
			pageCount = ocrPageCount
		} else if n := pdfPageCount(ctx, filePath); n > 0 {
			pageCount = n
		}
	} else {
		content, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read file: %w", err)
		}
		extractedText = p.extractText(ctx, content, mimeType, opts, logf)
	}
	lang := "und"
	if len(p.OCRLanguages) > 0 {
		lang = ocrLangToBCP47(p.OCRLanguages[0])
	}
	result := &ProcessResult{
		ExtractedText: extractedText,
		PageCount:     pageCount,
		Images:        images,
		Language:      lang,
		ProcessingMs:  time.Since(start).Milliseconds(),
	}
	span.SetAttributes(
		attribute.Int("result.page_count", pageCount),
		attribute.Int("result.text_length", len(extractedText)),
		attribute.Int("result.image_count", len(images)),
		attribute.Int64("result.processing_ms", result.ProcessingMs),
	)
	return result, nil
}

// hasGarbledCEEncoding detects when a PDF font maps Central European characters
// to ASCII positions, causing pdftotext to produce garbled output. Different
// fonts use different mappings — common patterns seen in Bosnian/Croatian/Serbian PDFs:
//   - !="#$ replacing diacritics (immediate signal)
//   - digits 2, 7, 9 or / appearing inside words (e.g. "izme9u"=između, "dolaze2i"=dolazeći)
//
// It samples four 500-rune windows spread across the document (start, 25%,
// 50%, 75%) so that garbling that begins after a clean English preface is
// still detected. Total scan budget is ~2000 runes.
func (p *DocumentProcessor) hasGarbledCEEncoding(text string) bool {
	runes := []rune(text)
	n := len(runes)
	if n == 0 {
		return false
	}
	isLetter := func(c rune) bool {
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	const windowSize = 500
	offsets := [4]int{0, n / 4, n / 2, 3 * n / 4}
	suspiciousCount := 0
	var prevEnd int
	for _, start := range offsets {
		if start < prevEnd {
			start = prevEnd // skip already-scanned region
		}
		if start >= n {
			break
		}
		end := min(start+windowSize, n)
		prevEnd = end
		window := runes[start:end]
		for i := 1; i < len(window)-1; i++ {
			r := window[i]
			if !isLetter(window[i-1]) || !isLetter(window[i+1]) {
				continue
			}
			switch r {
			case '!', '"', '#', '$':
				return true // immediate strong signal
			case '2', '7', '9', '/':
				suspiciousCount++
				if suspiciousCount >= 2 {
					return true
				}
			}
		}
	}
	return false
}

// pdfPageCount returns the number of pages in the PDF using pdfinfo.
func pdfPageCount(ctx context.Context, pdfPath string) int {
	out, err := exec.CommandContext(ctx, "pdfinfo", pdfPath).Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Pages:") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
			if err == nil {
				return n
			}
		}
	}
	return 0
}

// AnalyzePDF samples evenly-spaced pages from a PDF, runs Vision OCR, and
// clusters the observations by font size. Returns a ClusterResult for the
// frontend to display. Requires Vision OCR (macOS).
func (p *DocumentProcessor) AnalyzePDF(ctx context.Context, pdfPath string, pageFrom, pageTo int, logf func(string, ...any)) (*ClusterResult, error) {
	visionOCROnce.Do(initVisionOCR)
	if visionOCRBin == "" {
		return nil, fmt.Errorf("vision OCR not available (macOS only)")
	}

	pageCount := pdfPageCount(ctx, pdfPath)
	if pageCount == 0 {
		return nil, fmt.Errorf("could not determine page count")
	}

	rangeStart := 1
	rangeEnd := pageCount
	if pageFrom > 0 && pageFrom <= pageCount {
		rangeStart = pageFrom
	}
	if pageTo > 0 && pageTo <= pageCount {
		rangeEnd = pageTo
	}
	rangeLen := rangeEnd - rangeStart + 1
	if rangeLen <= 0 {
		return nil, fmt.Errorf("invalid page range %d–%d", pageFrom, pageTo)
	}

	const maxSamplePages = 20
	samplePages := pickSamplePages(rangeLen, maxSamplePages)
	for i := range samplePages {
		samplePages[i] += rangeStart - 1
	}
	logf("Analyzing %d sample pages from range %d–%d (total %d)", len(samplePages), rangeStart, rangeEnd, pageCount)

	tmpDir, err := os.MkdirTemp("", "pdf_analyze_*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	langs := p.OCRLanguages
	if len(langs) == 0 {
		langs = []string{"eng"}
	}

	type analyzePageResult struct {
		obs     []visionObs
		pageNum int
	}
	results := make([]analyzePageResult, len(samplePages))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))

	for idx, pg := range samplePages {
		wg.Add(1)
		go func(i, pageNum int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			imgPrefix := filepath.Join(tmpDir, fmt.Sprintf("p%d", pageNum))
			cmd := exec.CommandContext(ctx, "pdftoppm", "-r", "150", "-png",
				"-f", strconv.Itoa(pageNum), "-l", strconv.Itoa(pageNum), "-singlefile",
				pdfPath, imgPrefix)
			if out, err := cmd.CombinedOutput(); err != nil {
				logf("Analyze: render page %d failed: %s", pageNum, string(out))
				return
			}
			imgPath := imgPrefix + ".png"
			defer os.Remove(imgPath)
			if _, err := os.Stat(imgPath); err != nil {
				return
			}

			for _, lang := range langs {
				vr := runVisionOCR(ctx, imgPath, lang)
				if len(vr.Obs) > 0 {
					results[i] = analyzePageResult{obs: vr.Obs, pageNum: pageNum}
					logf("Analyze: page %d → %d observations", pageNum, len(vr.Obs))
					return
				}
			}
		}(idx, pg)
	}
	wg.Wait()

	var allObs [][]visionObs
	var obsPageNums []int
	for _, r := range results {
		if len(r.obs) > 0 {
			allObs = append(allObs, r.obs)
			obsPageNums = append(obsPageNums, r.pageNum)
		}
	}

	if len(allObs) == 0 {
		return nil, fmt.Errorf("no observations from sampled pages")
	}

	var totalObs int
	var minH, maxH float64
	for _, pageObs := range allObs {
		for _, o := range pageObs {
			if o.H <= 0 {
				continue
			}
			totalObs++
			if minH == 0 || o.H < minH {
				minH = o.H
			}
			if o.H > maxH {
				maxH = o.H
			}
		}
	}
	logf("Analyze: %d total obs, H range [%.4f – %.4f], ratio %.1fx", totalObs, minH, maxH, maxH/minH)

	result := ClusterObservations(allObs, obsPageNums)
	result.PageCount = pageCount
	logf("Analyze: %d clusters detected", len(result.Clusters))
	for _, c := range result.Clusters {
		logf("  Cluster %d (%s): %d lines, avgH=%.4f [%.4f–%.4f]", c.ID, c.RelativeSize, c.LineCount, c.AvgH, c.MinH, c.MaxH)
	}
	return &result, nil
}

// pickSamplePages returns up to n evenly-spaced page numbers from [1, total].
func pickSamplePages(total, n int) []int {
	if total <= n {
		pages := make([]int, total)
		for i := range pages {
			pages[i] = i + 1
		}
		return pages
	}
	pages := make([]int, n)
	for i := range n {
		pages[i] = 1 + i*(total-1)/(n-1)
	}
	// Deduplicate (can happen with small totals).
	seen := map[int]bool{}
	var unique []int
	for _, p := range pages {
		if !seen[p] {
			seen[p] = true
			unique = append(unique, p)
		}
	}
	return unique
}

// extractPDFTextOCR renders each PDF page to an image, runs OCR, and detects
// figures via Vision observation bounding boxes.
// Returns the full text, any cropped figure images, and the page count.
func (p *DocumentProcessor) extractPDFTextOCR(ctx context.Context, pdfPath string, opts ProcessOptions, logf func(string, ...any)) (string, []PDFImage, int) {
	pageCount := pdfPageCount(ctx, pdfPath)
	if pageCount == 0 {
		return "", nil, 0
	}

	tmpDir, err := os.MkdirTemp("", "pdf_ocr_*")
	if err != nil {
		errors.LogError(err, "extractPDFTextOCR: failed to create temp dir")
		return "", nil, 0
	}
	defer os.RemoveAll(tmpDir)

	// Render all pages in a single pdftoppm call (batch mode) instead of
	// one process per page. This reduces process spawn overhead significantly
	// for large PDFs.
	firstPage := 1
	lastPage := pageCount
	if opts.PageFrom > 0 {
		firstPage = min(opts.PageFrom, pageCount)
	}
	if opts.PageTo > 0 && opts.PageTo <= pageCount {
		lastPage = opts.PageTo
	}
	if firstPage > lastPage {
		logf("Invalid page range %d–%d (PDF has %d pages)", firstPage, lastPage, pageCount)
		return "", nil, 0
	}
	actualPages := lastPage - firstPage + 1
	renderDPI := "300"
	if len(opts.IncludeClusters) > 0 {
		renderDPI = "150"
	}
	logf("Rendering %d pages (%d–%d) at %s dpi…", actualPages, firstPage, lastPage, renderDPI)
	batchCmd := exec.CommandContext(ctx, "pdftoppm", "-r", renderDPI, "-png",
		"-f", strconv.Itoa(firstPage), "-l", strconv.Itoa(lastPage),
		pdfPath, filepath.Join(tmpDir, "page"))
	if out, err := batchCmd.CombinedOutput(); err != nil {
		errors.LogError(err, "extractPDFTextOCR: batch pdftoppm failed: %s", string(out))
		return "", nil, 0
	}

	// Create a semaphore for OCR concurrency limiting.
	ocrSem := make(chan struct{}, p.OCRConcurrencyLimit)

	type pageResult struct {
		text string
		figs []PDFImage
	}
	results := make([]pageResult, actualPages)
	var wg sync.WaitGroup

	// pdftoppm zero-pads page numbers based on total page count:
	// 1-9 pages → 1 digit, 10-99 → 2 digits, 100-999 → 3 digits, etc.
	padWidth := len(strconv.Itoa(pageCount))

	for i := range actualPages {
		wg.Add(1)
		go func(page int) {
			defer wg.Done()
			pdfPage := firstPage + page
			select {
			case ocrSem <- struct{}{}:
			case <-ctx.Done():
				logf("OCR page %d/%d: cancelled (%v)", pdfPage, lastPage, ctx.Err())
				return
			}
			defer func() { <-ocrSem }()

			imgPath := filepath.Join(tmpDir, fmt.Sprintf("page-%0*d.png", padWidth, pdfPage))
			if _, statErr := os.Stat(imgPath); statErr != nil {
				logf("OCR page %d/%d: rendered image not found", pdfPage, lastPage)
				return
			}

			usedLang := "none"
			var pageText string
			var pageFigs []PDFImage

			ocrStart := time.Now()

			// Try Vision first: returns text + bounding boxes for figure detection.
			for _, lang := range p.OCRLanguages {
				vr := runVisionOCR(ctx, imgPath, lang)
				if vr.Text == "" {
					continue
				}
				usedLang = "vision:" + lang
				pageText = vr.Text
				if len(opts.IncludeClusters) > 0 && len(opts.ClusterDefs) > 0 {
					pageText, _ = FilterObsByCluster(pageText, vr.Obs, opts.IncludeClusters, opts.ClusterDefs)
				} else if opts.StripCaptions {
					pageText = filterSmallFontText(pageText, vr.Obs)
				}
				pageFigs = cropFigures(imgPath, vr.Obs, pdfPage)
				if len(pageFigs) > 0 {
					logf("OCR page %d/%d engine=%s figures=%d", pdfPage, lastPage, usedLang, len(pageFigs))
				} else {
					logf("OCR page %d/%d engine=%s", pdfPage, lastPage, usedLang)
				}
				break
			}

			// Tesseract fallback (no figure detection).
			if pageText == "" {
				if t, lang := runTesseractOCR(ctx, imgPath, p.OCRLanguages, nil); t != "" {
					usedLang = "tesseract:" + lang
					pageText = t
				}
				logf("OCR page %d/%d engine=%s", pdfPage, lastPage, usedLang)
			}

			metrics.RecordOCRCall()
			metrics.RecordOCRProcessing(time.Since(ocrStart).Seconds())
			results[page] = pageResult{text: pageText, figs: pageFigs}
		}(i)
	}
	wg.Wait()

	var allText strings.Builder
	var allFigs []PDFImage
	for _, r := range results {
		allText.WriteString(r.text)
		allText.WriteString("\f") // page separator — enables processFootnotes per-page
		allFigs = append(allFigs, r.figs...)
	}
	return allText.String(), allFigs, actualPages
}

func (p *DocumentProcessor) processFootnotes(text string, opts ProcessOptions) string {
	// Process each page independently so footnote markers on one page
	// don't incorrectly consume content from subsequent pages.
	// Without form feeds we have no page boundaries and cannot safely
	// distinguish footnotes from body text.
	if !strings.Contains(text, "\f") {
		return text
	}
	pages := strings.Split(text, "\f")

	// Detect repeated headers/footers across pages before stripping.
	var headers, footers map[string]bool
	if opts.StripHeaders && len(pages) > 2 {
		headers, footers = detectRepeatingHeadersFooters(pages[1:])
	}

	processed := make([]string, 0, len(pages))
	for i, page := range pages {
		if i > 0 && opts.StripHeaders {
			page = stripDetectedHeaders(page, headers, footers)
		}
		if opts.StripFootnotes {
			page = p.processPageFootnotes(page)
		}
		if opts.StripCaptions {
			page = stripLifelineNotes(page)
		}
		processed = append(processed, page)
	}
	return strings.Join(processed, "\n")
}

// pageNumOnlyRe matches a block that is just a page number.
var pageNumOnlyRe = regexp.MustCompile(`^\d+$`)

// detectRepeatingHeadersFooters scans the top and bottom of each page to find
// text that repeats across many pages. Repeated text at the top = running header,
// at the bottom = running footer. No assumptions about case or formatting.
func detectRepeatingHeadersFooters(pages []string) (headers, footers map[string]bool) {
	const maxTopBlocks = 5
	const maxBottomBlocks = 3
	const minPages = 3

	topCounts := map[string]int{}
	bottomCounts := map[string]int{}

	for _, page := range pages {
		blocks := nonEmptyBlocks(page)
		if len(blocks) == 0 {
			continue
		}

		topLimit := maxTopBlocks
		if topLimit > len(blocks) {
			topLimit = len(blocks)
		}
		seen := map[string]bool{}
		for _, b := range blocks[:topLimit] {
			norm := strings.TrimSpace(b)
			if norm == "" || pageNumOnlyRe.MatchString(norm) {
				continue
			}
			if !seen[norm] {
				topCounts[norm]++
				seen[norm] = true
			}
		}

		bottomLimit := maxBottomBlocks
		if bottomLimit > len(blocks) {
			bottomLimit = len(blocks)
		}
		seen = map[string]bool{}
		for _, b := range blocks[len(blocks)-bottomLimit:] {
			norm := strings.TrimSpace(b)
			if norm == "" || pageNumOnlyRe.MatchString(norm) {
				continue
			}
			if !seen[norm] {
				bottomCounts[norm]++
				seen[norm] = true
			}
		}
	}

	threshold := len(pages) * 30 / 100
	if threshold < minPages {
		threshold = minPages
	}

	headers = map[string]bool{}
	for text, count := range topCounts {
		if count >= threshold {
			headers[text] = true
		}
	}
	footers = map[string]bool{}
	for text, count := range bottomCounts {
		if count >= threshold {
			footers[text] = true
		}
	}
	return headers, footers
}

// nonEmptyBlocks splits a page into blank-line-separated blocks (pdftotext style)
// or single-newline lines (OCR style) and returns non-empty entries.
func nonEmptyBlocks(page string) []string {
	var blocks []string
	if strings.Contains(page, "\n\n") {
		for _, b := range strings.Split(page, "\n\n") {
			if strings.TrimSpace(b) != "" {
				blocks = append(blocks, strings.TrimSpace(b))
			}
		}
	} else {
		for _, l := range strings.Split(page, "\n") {
			if strings.TrimSpace(l) != "" {
				blocks = append(blocks, strings.TrimSpace(l))
			}
		}
	}
	return blocks
}

// stripDetectedHeaders removes known repeated headers/footers and bare page
// numbers from a page.
func stripDetectedHeaders(page string, headers, footers map[string]bool) string {
	blocks := nonEmptyBlocks(page)
	if len(blocks) == 0 {
		return page
	}

	// Determine separator style.
	sep := "\n"
	if strings.Contains(page, "\n\n") {
		sep = "\n\n"
	}

	// Strip from top: page numbers and detected headers.
	start := 0
	for start < len(blocks) {
		b := blocks[start]
		if pageNumOnlyRe.MatchString(b) || headers[b] {
			start++
			continue
		}
		break
	}

	// Strip from bottom: page numbers and detected footers.
	end := len(blocks)
	for end > start {
		b := blocks[end-1]
		if pageNumOnlyRe.MatchString(b) || footers[b] {
			end--
			continue
		}
		break
	}

	if start == 0 && end == len(blocks) {
		return page
	}
	return strings.Join(blocks[start:end], sep)
}


func (p *DocumentProcessor) processPageFootnotes(text string) string {
	lines := strings.Split(text, "\n")
	total := len(lines)

	// Only scan the bottom 30% of the page for footnote markers.
	footnoteScanStart := (total * 7) / 10
	if footnoteScanStart < 5 {
		return text // page too short to have detectable footnotes
	}

	for i, line := range lines {
		if i < footnoteScanStart {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && footnoteMarkerRe.MatchString(trimmed) {
			// Cut here: return only the body text above the footnote section.
			return strings.Join(lines[:i], "\n")
		}
	}
	return text
}

func (p *DocumentProcessor) Close() error {
	// visionOCRBin lives in os.TempDir() and is cleaned by the OS on reboot.
	// Removing it here races with in-flight OCR goroutines during shutdown.
	return nil
}

// IsLoaded returns true when the required processing tools are available.
func (p *DocumentProcessor) IsLoaded() bool {
	_, pdfErr := exec.LookPath("pdftotext")
	_, tessErr := exec.LookPath("tesseract")
	return pdfErr == nil || tessErr == nil
}

type EPUBGenerator struct {
	MaxWordsPerChapter int // maximum words per chapter; defaults to 1500
}

type EPUBChapter struct {
	Title   string
	Content string
	Images  []PDFImage // images belonging to this chapter, in page order
}

type EPUBResult struct {
	Title        string
	Author       string
	Language     string
	Chapters     []EPUBChapter
	ProcessingMs int64
}

// NewEPUBGenerator creates a new EPUBGenerator with the given max words per chapter.
// If maxWordsPerChapter is <= 0, the default (1500) is used.
func NewEPUBGenerator(maxWordsPerChapter int) *EPUBGenerator {
	if maxWordsPerChapter <= 0 {
		maxWordsPerChapter = 1500
	}
	return &EPUBGenerator{MaxWordsPerChapter: maxWordsPerChapter}
}

// GenerateFromText splits text into chapters and distributes images across them
// proportionally by page number. totalPages is the page count of the source PDF
// (used to map image.PageNum to a chapter index); pass 0 when no images.
// language is the BCP-47 tag for the EPUB metadata (e.g. "hr", "en", "sr").
func (g *EPUBGenerator) GenerateFromText(text string, images []PDFImage, totalPages int, title, author, language string) (*EPUBResult, error) {
	start := time.Now()

	chapters := g.splitIntoChapters(text)
	if len(images) > 0 && len(chapters) > 0 {
		assignImagesToChapters(chapters, images, totalPages)
	}

	if language == "" {
		language = "en"
	}

	return &EPUBResult{
		Title:        title,
		Author:       author,
		Language:     language,
		Chapters:     chapters,
		ProcessingMs: time.Since(start).Milliseconds(),
	}, nil
}

// assignImagesToChapters distributes images across chapters proportionally.
// Each image is placed in the chapter that corresponds to its source page.
func assignImagesToChapters(chapters []EPUBChapter, images []PDFImage, totalPages int) {
	n := len(chapters)
	if totalPages <= 0 {
		errors.LogWarn("assignImagesToChapters: totalPages=%d, all %d images will be assigned to first chapter", totalPages, len(images))
		totalPages = 1
	}
	for i, img := range images {
		// Map page number (1-based) to chapter index (0-based).
		idx := int((int64(img.PageNum) - 1) * int64(n) / int64(totalPages))
		if idx < 0 {
			idx = 0
		} else if idx >= n {
			idx = n - 1
		}
		chapters[idx].Images = append(chapters[idx].Images, images[i])
	}
}

// isHeadingCandidate performs a cheap pre-check to filter out lines that
// clearly cannot be chapter headings before running the full regex.
func isHeadingCandidate(s string) bool {
	if len(s) == 0 {
		return false
	}
	r := []rune(s)
	first := r[0]
	// Headings start with: uppercase ASCII, uppercase Cyrillic, uppercase Latin extended,
	// or a digit (for "Chapter 1" patterns).
	return (first >= 'A' && first <= 'Z') ||
		(first >= 'А' && first <= 'Я') ||
		(first >= 0x00C0 && first <= 0x024F) ||
		(first >= '0' && first <= '9')
}

// chapterHeadingRe matches chapter heading patterns:
//   - Keyword (Glava/Poglavlje/Deo/etc.) followed strictly by Arabic digits or uppercase
//     Roman numerals — ensures "Glava mu je..." (normal sentences) don't match.
//   - Standalone uppercase Roman numerals on their own line.
//   - Known all-caps section words (UVOD, PREDGOVOR, DIO PRVI, etc.).
var chapterHeadingRe = regexp.MustCompile(
	`^(?i:glava|poglavlje|deo|dio|chapter|part)\s+(?:\d+|[IVXLCDM]{1,8})(?:\s+.{0,40})?$` +
		`|^[IVXLCDM]{1,8}\.?\s*$` +
		`|^(?:UVOD|PREDGOVOR|POGOVOR|EPILOG|PROLOG|INTRODUCTION|CONCLUSION|APPENDIX` +
		`|DIO PRVI|DIO DRUGI|DIO TRE[ĆC]I|PRVI DEO|DRUGI DEO|TRE[ĆC]I DEO)\s*$`,
)

func (g *EPUBGenerator) splitIntoChapters(text string) []EPUBChapter {
	// First try to split on real chapter headings.
	if chapters := g.splitOnHeadings(text); len(chapters) > 1 {
		return chapters
	}
	// Fall back to word-count based splitting.
	return g.splitByWordCount(text)
}

func (g *EPUBGenerator) splitOnHeadings(text string) []EPUBChapter {
	var chapters []EPUBChapter
	var currentTitle string
	var currentContent strings.Builder

	// Pre-allocate content builder with a reasonable estimate.
	currentContent.Grow(len(text) / 4)

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		// Cheap pre-check: skip lines that are too long or too short to be headings,
		// or that don't start with an uppercase letter or digit.
		if len(trimmed) > 0 && len(trimmed) <= 80 && isHeadingCandidate(trimmed) && chapterHeadingRe.MatchString(trimmed) {
			if currentContent.Len() > 0 {
				title := currentTitle
				if title == "" {
					title = "Početak"
				}
				chapters = append(chapters, EPUBChapter{
					Title:   title,
					Content: strings.TrimSpace(currentContent.String()),
					Images:  nil,
				})
				currentContent.Reset()
			}
			currentTitle = trimmed
		} else {
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
		}
	}

	if currentContent.Len() > 0 {
		title := currentTitle
		if title == "" {
			title = "Sadržaj"
		}
		chapters = append(chapters, EPUBChapter{
			Title:   title,
			Content: strings.TrimSpace(currentContent.String()),
			Images:  nil,
		})
	}

	return chapters
}

func (g *EPUBGenerator) splitByWordCount(text string) []EPUBChapter {
	var chapters []EPUBChapter
	paragraphs := strings.Split(text, "\n\n")
	var current strings.Builder
	current.Grow(g.MaxWordsPerChapter * 6) // ~6 bytes per word average
	chapterCount := 1
	wordCount := 0

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		current.WriteString(para)
		current.WriteString("\n\n")
		wordCount += len(strings.Fields(para))
		if wordCount >= g.MaxWordsPerChapter {
			chapters = append(chapters, EPUBChapter{
				Title:   strconv.Itoa(chapterCount),
				Content: strings.TrimSpace(current.String()),
				Images:  nil,
			})
			chapterCount++
			current.Reset()
			wordCount = 0
		}
	}
	if current.Len() > 0 {
		chapters = append(chapters, EPUBChapter{
			Title:   strconv.Itoa(chapterCount),
			Content: strings.TrimSpace(current.String()),
			Images:  nil,
		})
	}
	return chapters
}
