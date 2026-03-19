package model

import (
	"fmt"
	"log"
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
)

// globalOCRSem bounds the total concurrent OCR page workers across all active
// conversions. Defaults to GOMAXPROCS; override with OCR_CONCURRENCY env var.
var globalOCRSem = make(chan struct{}, ocrConcurrency())

func ocrConcurrency() int {
	if s := os.Getenv("OCR_CONCURRENCY"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
		log.Printf("warning: invalid value for OCR_CONCURRENCY, using GOMAXPROCS")
	}
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

	cmd := exec.Command(swiftc, "-O", src, "-o", binPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = out // compilation errors are silently dropped
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
func extractImageTextVision(imgPath, tesseractLang string) string {
	return runVisionOCR(imgPath, tesseractLang).Text
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
	TextOnly       bool // skip embedded image extraction
}

// DefaultProcessOptions returns sensible defaults: smart-OCR on, headers and
// footnotes stripped, images extracted, force-OCR off.
func DefaultProcessOptions() ProcessOptions {
	return ProcessOptions{
		SmartOCR:       true,
		ForceOCR:       false,
		StripHeaders:   true,
		StripFootnotes: true,
		TextOnly:       false,
	}
}

type DocumentProcessor struct{}

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
func extractPDFImages(pdfPath string, logf func(string, ...any)) []PDFImage {
	// List images first to obtain page numbers and dimensions.
	listOut, err := exec.Command("pdfimages", "-list", pdfPath).Output()
	if err != nil {
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
		return nil
	}
	defer os.RemoveAll(tmpDir)

	prefix := filepath.Join(tmpDir, "img")
	if err := exec.Command("pdfimages", "-png", pdfPath, prefix).Run(); err != nil {
		return nil
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
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

type ProcessResult struct {
	ExtractedText string
	PageCount     int
	Images        []PDFImage
	ProcessingMs  int64
}

func NewDocumentProcessor() *DocumentProcessor {
	// Compile the Vision OCR binary eagerly so the first real request doesn't
	// pay a 2-10s swiftc compilation penalty.
	go visionOCROnce.Do(initVisionOCR)
	return &DocumentProcessor{}
}

func (p *DocumentProcessor) ProcessDocument(content []byte, mimeType string, logf func(string, ...any)) (*ProcessResult, error) {
	start := time.Now()
	return &ProcessResult{
		ExtractedText: p.extractText(content, mimeType, logf),
		PageCount:     1,
		Images:        nil,
		ProcessingMs:  time.Since(start).Milliseconds(),
	}, nil
}

func (p *DocumentProcessor) extractText(content []byte, mimeType string, logf func(string, ...any)) string {
	switch mimeType {
	case "application/pdf":
		return p.extractPDFText(content, logf)
	case "image/png", "image/jpeg", "image/tiff":
		return p.extractImageText(content, logf)
	case "text/plain", "text/markdown", "text/html", "application/octet-stream":
		return string(content)
	default:
		logf("extractText: unrecognised MIME type %q, treating as plain text", mimeType)
		return string(content)
	}
}

func (p *DocumentProcessor) extractPDFText(content []byte, logf func(string, ...any)) string {
	tmpFile, err := os.CreateTemp("", "pdf_*.pdf")
	if err != nil {
		return fmt.Sprintf("[PDF Error: %v]", err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.Write(content); err != nil {
		if cerr := tmpFile.Close(); cerr != nil {
			logf("warning: failed to close tmp pdf file after write error: %v", cerr)
		}
		return fmt.Sprintf("[PDF Error: %v]", err)
	}
	if err := tmpFile.Close(); err != nil {
		logf("warning: failed to close tmp pdf file: %v", err)
	}
	text, _ := p.extractPDFTextFromPath(tmpFile.Name(), DefaultProcessOptions(), logf)
	return text
}

// extractImageText OCRs a raw image (PNG/JPEG/TIFF) by writing it to a temp
// file and running Vision OCR (macOS) or Tesseract as fallback.
func (p *DocumentProcessor) extractImageText(content []byte, logf func(string, ...any)) string {
	tmpFile, err := os.CreateTemp("", "img_ocr_*")
	if err != nil {
		return fmt.Sprintf("[Image OCR Error: %v]", err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.Write(content); err != nil {
		if cerr := tmpFile.Close(); cerr != nil {
			logf("warning: failed to close tmp image file after write error: %v", cerr)
		}
		return fmt.Sprintf("[Image OCR Error: %v]", err)
	}
	if err := tmpFile.Close(); err != nil {
		logf("warning: failed to close tmp image file: %v", err)
	}

	langs := []string{"srp_latn+hrv", "srp_latn", "eng"}
	for _, lang := range langs {
		if t := extractImageTextVision(tmpFile.Name(), lang); t != "" {
			logf("Image OCR engine=vision:%s chars=%d", lang, len(t))
			return t
		}
	}

	// Tesseract fallback.
	ocrBase := tmpFile.Name() + "_tess"
	for _, lang := range langs {
		c := exec.Command("tesseract", tmpFile.Name(), ocrBase, "-l", lang)
		if c.Run() != nil {
			continue
		}
		data, err := os.ReadFile(ocrBase + ".txt")
		if rerr := os.Remove(ocrBase + ".txt"); rerr != nil && !os.IsNotExist(rerr) {
			logf("warning: failed to remove OCR tmp file: %v", rerr)
		}
		if err != nil {
			logf("warning: failed to read OCR output: %v", err)
			continue
		}
		if strings.TrimSpace(string(data)) != "" {
			logf("Image OCR engine=tesseract:%s chars=%d", lang, len(data))
			return string(data)
		}
	}
	return "[Image OCR: no text recognised]"
}

// pdfHasUnmappedFonts runs pdffonts and returns true if any font uses Custom
// encoding with no ToUnicode mapping. This is a reliable upfront indicator that
// pdftotext will produce garbled output, allowing us to skip straight to OCR.
func pdfHasUnmappedFonts(pdfPath string) bool {
	out, err := exec.Command("pdffonts", pdfPath).Output()
	if err != nil {
		return false
	}
	lines := strings.Split(string(out), "\n")
	// Need at least: header line, separator line, one data line.
	if len(lines) < 3 {
		return false
	}
	// Derive column start positions from the separator line (groups of dashes).
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
	// Columns: name(0) type(1) encoding(2) emb(3) sub(4) uni(5) object(6)
	if len(colStarts) < 7 {
		return false
	}
	encStart, encEnd := colStarts[2], colStarts[3]
	uniStart := colStarts[5]
	for _, line := range lines[2:] {
		if len(line) <= uniStart {
			continue
		}
		enc := strings.TrimSpace(line[encStart:min(encEnd, len(line))])
		uni := strings.TrimSpace(line[uniStart:min(uniStart+3, len(line))])
		if enc == "Custom" && uni == "no" {
			return true
		}
	}
	return false
}

// scanFontRe matches the auto-generated font-subset names that macOS Quartz and
// Adobe Acrobat emit when baking OCR output into a searchable scan PDF.
// Format: six uppercase letters + "+" + arbitrary suffix (e.g. "AAAAAB+Fd431742").
var scanFontRe = regexp.MustCompile(`^[A-Z]{6}\+`)

// pdfLikelyScan returns true when all named fonts in the PDF use the
// auto-generated subset naming pattern produced by scan-to-PDF workflows.
// These PDFs have ToUnicode maps (so pdftotext doesn't error out) but the
// embedded text is the output of a prior OCR pass and may contain errors;
// re-rendering the page images with our own OCR gives an independent result.
func pdfLikelyScan(pdfPath string) bool {
	out, err := exec.Command("pdffonts", pdfPath).Output()
	if err != nil {
		return false
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) < 3 {
		return false
	}
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
		return false
	}
	nameEnd := colStarts[1]
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
	}
	return total > 0 && scan == total
}

// extractPDFTextFromPath returns the text and any embedded images from pdfPath.
func (p *DocumentProcessor) extractPDFTextFromPath(pdfPath string, opts ProcessOptions, logf func(string, ...any)) (string, []PDFImage) {
	runOCR := func() (string, []PDFImage) {
		if ocr, figs := p.extractPDFTextOCR(pdfPath, logf); ocr != "" {
			logf("OCR complete (%d chars)", len(ocr))
			return p.processFootnotes(ocr, opts), figs
		}
		logf("OCR produced no output")
		return "", nil
	}

	if opts.ForceOCR {
		logf("Force OCR enabled, skipping embedded text")
		return runOCR()
	}

	if opts.SmartOCR && pdfHasUnmappedFonts(pdfPath) {
		logf("Custom-encoded fonts detected, skipping pdftotext")
		return runOCR()
	}

	if opts.SmartOCR && pdfLikelyScan(pdfPath) {
		logf("Scan-based PDF detected (embedded OCR fonts), re-rendering pages for fresh OCR")
		if text, figs := runOCR(); text != "" {
			return text, figs
		}
		logf("OCR produced no output, falling back to pdftotext")
	}

	outFile, err := os.CreateTemp("", "pdftext_*.txt")
	if err != nil {
		return fmt.Sprintf("[PDF Error: %v]", err), nil
	}
	outputFile := outFile.Name()
	outFile.Close()
	defer os.Remove(outputFile)

	cmd := exec.Command("pdftotext", pdfPath, outputFile)
	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("[PDF Error: %v]", err), nil
	}

	text, err := os.ReadFile(outputFile)
	if err != nil {
		return fmt.Sprintf("[PDF Error: %v]", err), nil
	}

	extracted := string(text)
	needOCR := strings.TrimSpace(extracted) == "" || p.hasGarbledCEEncoding(extracted)
	if needOCR {
		if strings.TrimSpace(extracted) == "" {
			logf("pdftotext returned empty text (image-only PDF?), falling back to OCR")
		} else {
			logf("Garbled encoding detected, falling back to OCR")
		}
		if text, figs := runOCR(); text != "" {
			return text, figs
		}
		logf("OCR produced no output, using pdftotext result")
		return p.processFootnotes(extracted, opts), nil
	}

	// Native-text PDF: extract embedded images unless text-only mode.
	var images []PDFImage
	if !opts.TextOnly {
		images = extractPDFImages(pdfPath, logf)
	}
	return p.processFootnotes(extracted, opts), images
}

// ProcessDocumentFromPath processes a file already on disk.
// For PDFs this avoids writing content to a second temp file.
// smartOCR enables upfront font-encoding detection via pdffonts to skip
// directly to OCR when Custom-encoded fonts are found.
func (p *DocumentProcessor) ProcessDocumentFromPath(filePath, mimeType string, opts ProcessOptions, logf func(string, ...any)) (*ProcessResult, error) {
	start := time.Now()
	var extractedText string
	var images []PDFImage
	pageCount := 1
	if mimeType == "application/pdf" {
		extractedText, images = p.extractPDFTextFromPath(filePath, opts, logf)
		if n := pdfPageCount(filePath); n > 0 {
			pageCount = n
		}
	} else {
		content, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read file: %w", err)
		}
		extractedText = p.extractText(content, mimeType, logf)
	}
	return &ProcessResult{
		ExtractedText: extractedText,
		PageCount:     pageCount,
		Images:        images,
		ProcessingMs:  time.Since(start).Milliseconds(),
	}, nil
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
func pdfPageCount(pdfPath string) int {
	out, err := exec.Command("pdfinfo", pdfPath).Output()
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

// extractPDFTextOCR renders each PDF page to an image, runs OCR, and detects
// figures via Vision observation bounding boxes.
// Returns the full text and any cropped figure images.
func (p *DocumentProcessor) extractPDFTextOCR(pdfPath string, logf func(string, ...any)) (string, []PDFImage) {
	pageCount := pdfPageCount(pdfPath)
	if pageCount == 0 {
		return "", nil
	}

	tmpDir, err := os.MkdirTemp("", "pdf_ocr_*")
	if err != nil {
		return "", nil
	}
	defer os.RemoveAll(tmpDir)

	type pageResult struct {
		text string
		figs []PDFImage
	}
	results := make([]pageResult, pageCount)
	var wg sync.WaitGroup

	for i := range pageCount {
		wg.Add(1)
		go func(page int) {
			defer wg.Done()
			globalOCRSem <- struct{}{}
			defer func() { <-globalOCRSem }()

			imgBase := fmt.Sprintf("%s/p%d", tmpDir, page)
			cmd := exec.Command("pdftoppm", "-r", "300", "-png", "-f", strconv.Itoa(page+1), "-l", strconv.Itoa(page+1), "-singlefile", pdfPath, imgBase)
			if cmd.Run() != nil {
				return
			}
			imgPath := imgBase + ".png"
			defer os.Remove(imgPath)

			langs := []string{"srp_latn+hrv", "srp_latn", "eng"}
			usedLang := "none"
			var pageText string
			var pageFigs []PDFImage

			// Try Vision first: returns text + bounding boxes for figure detection.
			for _, lang := range langs {
				vr := runVisionOCR(imgPath, lang)
				if vr.Text == "" {
					continue
				}
				usedLang = "vision:" + lang
				pageText = vr.Text
				pageFigs = cropFigures(imgPath, vr.Obs, page+1)
				if len(pageFigs) > 0 {
					logf("OCR page %d/%d engine=%s figures=%d", page+1, pageCount, usedLang, len(pageFigs))
				} else {
					logf("OCR page %d/%d engine=%s", page+1, pageCount, usedLang)
				}
				break
			}

			// Tesseract fallback (no figure detection).
			if pageText == "" {
				ocrBase := fmt.Sprintf("%s/p%d_ocr", tmpDir, page)
				for _, lang := range langs {
					c := exec.Command("tesseract", imgPath, ocrBase, "-l", lang)
					if c.Run() != nil {
						continue
					}
					data, err := os.ReadFile(ocrBase + ".txt")
					os.Remove(ocrBase + ".txt")
					if err == nil && strings.TrimSpace(string(data)) != "" {
						usedLang = "tesseract:" + lang
						pageText = string(data)
						break
					}
				}
				logf("OCR page %d/%d engine=%s", page+1, pageCount, usedLang)
			}

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
	return allText.String(), allFigs
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
	processed := make([]string, 0, len(pages))
	for i, page := range pages {
		if i > 0 && opts.StripHeaders {
			page = stripRunningHeader(page)
		}
		if opts.StripFootnotes {
			page = p.processPageFootnotes(page)
		}
		processed = append(processed, page)
	}
	return strings.Join(processed, "\n")
}

// pageNumOnlyRe matches a block that is just a page number.
var pageNumOnlyRe = regexp.MustCompile(`^\d+$`)

// stripRunningHeader removes the running header (page number + repeated title
// words) from the start of a page. It handles two formats:
//
//   - pdftotext pages: blank-line-separated blocks; looks for a standalone
//     page number within the first few blocks as a signal, then strips leading
//     short all-caps blocks.
//
//   - OCR pages (Vision/Tesseract): single-newline-separated lines; strips
//     leading lines that are short, mostly uppercase, and contain a digit
//     (the page number is typically embedded in the header line, e.g.
//     "2 MYTH AND REALITY" or "THE STRUCTURE OF MYTHS 3").
func stripRunningHeader(page string) string {
	blocks := strings.Split(page, "\n\n")

	// Scan the first few blocks for a bare page number.
	const lookAhead = 8
	limit := lookAhead
	if limit > len(blocks) {
		limit = len(blocks)
	}
	hasPageNum := false
	for _, b := range blocks[:limit] {
		if pageNumOnlyRe.MatchString(strings.TrimSpace(b)) {
			hasPageNum = true
			break
		}
	}
	if hasPageNum {
		// pdftotext format: consume leading blocks that are either the page
		// number or short all-uppercase header words (book/chapter title fragments).
		i := 0
		for i < len(blocks) {
			trimmed := strings.TrimSpace(blocks[i])
			if trimmed == "" {
				i++
				continue
			}
			if pageNumOnlyRe.MatchString(trimmed) {
				i++
				continue
			}
			// Short block whose letters are overwhelmingly uppercase → header word.
			words := strings.Fields(trimmed)
			if len(words) <= 4 && headerUppercase(trimmed) {
				i++
				continue
			}
			break
		}
		return strings.Join(blocks[i:], "\n\n")
	}

	// OCR format: no blank-line separators. Check the first few single-newline
	// lines for running-header patterns: short + mostly uppercase + contains a
	// digit (the page number is on the same line as the title, e.g.
	// "2 MYTH AND REALITY" or "THE STRUCTURE OF MYTHS 3").
	lines := strings.Split(page, "\n")
	const maxOCRHeaderLines = 4
	i := 0
	for i < len(lines) && i < maxOCRHeaderLines {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			i++
			continue
		}
		words := strings.Fields(trimmed)
		if len(words) <= 6 && headerUppercase(trimmed) && stringContainsDigit(trimmed) {
			i++
			continue
		}
		// Also strip a bare page-number line at the very top.
		if pageNumOnlyRe.MatchString(trimmed) {
			i++
			continue
		}
		break
	}
	if i == 0 {
		return page
	}
	return strings.Join(lines[i:], "\n")
}

// stringContainsDigit returns true if s contains at least one ASCII digit.
func stringContainsDigit(s string) bool {
	for _, c := range s {
		if c >= '0' && c <= '9' {
			return true
		}
	}
	return false
}

// headerUppercase reports whether at least 80% of the ASCII letters in s are
// uppercase — the threshold that distinguishes "MYTH" from "mythos came in…".
func headerUppercase(s string) bool {
	var letters, upper int
	for _, c := range s {
		if c >= 'a' && c <= 'z' {
			letters++
		} else if c >= 'A' && c <= 'Z' {
			letters++
			upper++
		}
	}
	return letters > 0 && float64(upper)/float64(letters) >= 0.8
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
	return nil
}

func (p *DocumentProcessor) IsLoaded() bool {
	return true
}

type EPUBGenerator struct {
	MaxWordsPerChapter int // configurable via EPUB_CHAPTER_WORDS env var
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

func NewEPUBGenerator() *EPUBGenerator {
	maxWords := 1500
	if s := os.Getenv("EPUB_CHAPTER_WORDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			maxWords = n
		} else {
			log.Printf("warning: invalid value for EPUB_CHAPTER_WORDS, using default %d", maxWords)
		}
	}
	return &EPUBGenerator{MaxWordsPerChapter: maxWords}
}

// GenerateFromText splits text into chapters and distributes images across them
// proportionally by page number. totalPages is the page count of the source PDF
// (used to map image.PageNum to a chapter index); pass 0 when no images.
func (g *EPUBGenerator) GenerateFromText(text string, images []PDFImage, totalPages int, title, author string) (*EPUBResult, error) {
	start := time.Now()

	chapters := g.splitIntoChapters(text)
	if len(images) > 0 && len(chapters) > 0 {
		assignImagesToChapters(chapters, images, totalPages)
	}

	return &EPUBResult{
		Title:        title,
		Author:       author,
		Language:     "en",
		Chapters:     chapters,
		ProcessingMs: time.Since(start).Milliseconds(),
	}, nil
}

// assignImagesToChapters distributes images across chapters proportionally.
// Each image is placed in the chapter that corresponds to its source page.
func assignImagesToChapters(chapters []EPUBChapter, images []PDFImage, totalPages int) {
	n := len(chapters)
	if totalPages <= 0 {
		log.Printf("assignImagesToChapters: totalPages=%d, all %d images will be assigned to first chapter", totalPages, len(images))
		totalPages = 1
	}
	for i, img := range images {
		// Map page number (1-based) to chapter index (0-based).
		idx := (img.PageNum - 1) * n / totalPages
		if idx < 0 {
			idx = 0
		} else if idx >= n {
			idx = n - 1
		}
		chapters[idx].Images = append(chapters[idx].Images, images[i])
	}
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

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if chapterHeadingRe.MatchString(trimmed) {
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
	chapterCount := 1
	wordCount := 0
	maxWordsPerChapter := g.MaxWordsPerChapter
	if maxWordsPerChapter <= 0 {
		maxWordsPerChapter = 1500
	}

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		current.WriteString(para)
		current.WriteString("\n\n")
		wordCount += len(strings.Fields(para))
		if wordCount >= maxWordsPerChapter {
			chapters = append(chapters, EPUBChapter{
				Title:   fmt.Sprintf("%d", chapterCount),
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
			Title:   fmt.Sprintf("%d", chapterCount),
			Content: strings.TrimSpace(current.String()),
			Images:  nil,
		})
	}
	return chapters
}

func (g *EPUBGenerator) Close() error {
	return nil
}
