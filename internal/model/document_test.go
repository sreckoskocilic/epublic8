package model

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func newProcessor() *DocumentProcessor {
	return NewDocumentProcessor(0, nil)
}

func newGenerator() *EPUBGenerator {
	return NewEPUBGenerator(0)
}

// --- hasGarbledCEEncoding ---

func TestHasGarbledCEEncoding(t *testing.T) {
	p := newProcessor()

	cases := []struct {
		name string
		text string
		want bool
	}{
		{"clean latin", "Ovo je normalan tekst bez problema.", false},
		{"immediate signal !", `izme!u dva`, true},
		{"immediate signal \"", `dolaze"i`, true},
		{"digit 9 in word twice", "izme9u re9i", true},
		{"digit 9 in word once", "izme9u jedne", false},
		{"digit 2 in word twice", "dolaze2i i2du", true},
		{"digit at word boundary", "page 9 done", false},
		{"empty string", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.hasGarbledCEEncoding(tc.text); got != tc.want {
				t.Errorf("hasGarbledCEEncoding(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// --- processPageFootnotes ---

func TestProcessPageFootnotesNoFootnotes(t *testing.T) {
	p := newProcessor()
	text := "First line.\nSecond line.\nThird line."
	got := p.processPageFootnotes(text)
	if got != text {
		t.Errorf("expected unchanged text, got %q", got)
	}
}

func TestProcessPageFootnotesStripsAtBottom(t *testing.T) {
	p := newProcessor()

	// Build a page where footnote marker "1" appears in the bottom 30%.
	var lines []string
	for i := 0; i < 18; i++ {
		lines = append(lines, "Body text on line.")
	}
	lines = append(lines, "Referenced text1 here.")
	lines = append(lines, "Another body line.")
	// Footnote marker + content at bottom (>70% through the page)
	lines = append(lines, "1")
	lines = append(lines, "This is footnote one")
	text := strings.Join(lines, "\n")

	got := p.processPageFootnotes(text)
	if strings.Contains(got, "This is footnote one") {
		t.Errorf("expected footnote section to be stripped, but it was present in output")
	}
	if !strings.Contains(got, "Body text on line.") {
		t.Errorf("expected body text to be preserved, got:\n%s", got)
	}
}

func TestProcessPageFootnotesIgnoresMidPageNumbers(t *testing.T) {
	p := newProcessor()

	// A page number "5" appearing after only 3 lines should NOT trigger footnote mode.
	text := "Line one.\nLine two.\nLine three.\n5\nLine four.\nLine five."
	got := p.processPageFootnotes(text)
	if !strings.Contains(got, "Line four.") {
		t.Errorf("mid-page number should not cut off content; got:\n%s", got)
	}
}

// --- splitOnHeadings ---

func TestSplitOnHeadingsRecognisesKeywords(t *testing.T) {
	g := newGenerator()

	text := "Glava 1\nFirst chapter content here.\nGlava 2\nSecond chapter content here."
	chapters := g.splitOnHeadings(text)
	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d", len(chapters))
	}
	if chapters[0].Title != "Glava 1" {
		t.Errorf("unexpected title %q", chapters[0].Title)
	}
	if !strings.Contains(chapters[0].Content, "First chapter") {
		t.Errorf("unexpected content %q", chapters[0].Content)
	}
}

func TestSplitOnHeadingsRomanNumerals(t *testing.T) {
	g := newGenerator()

	text := "I.\nOpening content.\nII.\nClosing content."
	chapters := g.splitOnHeadings(text)
	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d: %v", len(chapters), chapters)
	}
}

func TestSplitOnHeadingsKnownSections(t *testing.T) {
	g := newGenerator()

	text := "UVOD\nIntroductory text.\nPOGOVOR\nAfterword text."
	chapters := g.splitOnHeadings(text)
	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d", len(chapters))
	}
}

func TestSplitOnHeadingsNoHeadingsReturnsEmpty(t *testing.T) {
	g := newGenerator()

	text := "Just some plain text with no headings at all."
	chapters := g.splitOnHeadings(text)
	// Single chunk with default title — still 1 chapter
	if len(chapters) != 1 {
		t.Fatalf("expected 1 chapter, got %d", len(chapters))
	}
}

// --- splitByWordCount ---

func TestSplitByWordCountChunksLongText(t *testing.T) {
	g := newGenerator()

	// Build text with ~3000 words → should produce at least 2 chapters
	para := strings.Repeat("word ", 200) // 200 words per paragraph
	var paras []string
	for i := 0; i < 16; i++ {
		paras = append(paras, para)
	}
	text := strings.Join(paras, "\n\n")

	chapters := g.splitByWordCount(text)
	if len(chapters) < 2 {
		t.Errorf("expected multiple chapters for long text, got %d", len(chapters))
	}
}

func TestSplitByWordCountShortTextSingleChapter(t *testing.T) {
	g := newGenerator()

	text := "Short text.\n\nNot many words here."
	chapters := g.splitByWordCount(text)
	if len(chapters) != 1 {
		t.Errorf("expected 1 chapter for short text, got %d", len(chapters))
	}
}

// --- splitIntoChapters prefers headings ---

func TestSplitIntoChaptersUsesHeadingsWhenPresent(t *testing.T) {
	g := newGenerator()

	text := "Chapter I\nContent of first.\nChapter II\nContent of second."
	chapters := g.splitIntoChapters(text)
	if len(chapters) < 2 {
		t.Errorf("expected heading-based split, got %d chapters", len(chapters))
	}
}

// --- DefaultProcessOptions ---

func TestDefaultProcessOptions(t *testing.T) {
	opts := DefaultProcessOptions()
	if !opts.SmartOCR {
		t.Error("expected SmartOCR=true")
	}
	if !opts.StripHeaders {
		t.Error("expected StripHeaders=true")
	}
	if !opts.StripFootnotes {
		t.Error("expected StripFootnotes=true")
	}
	if opts.ForceOCR {
		t.Error("expected ForceOCR=false")
	}
	if opts.TextOnly {
		t.Error("expected TextOnly=false")
	}
}

// --- stringContainsDigit ---

func TestStringContainsDigit(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"abc", false},
		{"abc1", true},
		{"1", true},
		{"", false},
		{"page 5", true},
		{"TITLE", false},
	}
	for _, tc := range cases {
		if got := stringContainsDigit(tc.s); got != tc.want {
			t.Errorf("stringContainsDigit(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// --- headerUppercase ---

func TestHeaderUppercase(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"MYTH", true},
		{"myth", false},
		{"MYTh", false}, // 75% uppercase — below threshold
		{"MYTH AND REALITY", true},
		{"Myth and reality", false},
		{"2 MYTH AND REALITY", true}, // digits ignored, letters are all-caps
		{"", false},
	}
	for _, tc := range cases {
		if got := headerUppercase(tc.s); got != tc.want {
			t.Errorf("headerUppercase(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// --- stripRunningHeader ---

func TestStripRunningHeaderPDFFormat(t *testing.T) {
	// pdftotext format: blank-line blocks, standalone page number present
	page := "3\n\nMYTH AND REALITY\n\nActual body text starts here.\nContinued body."
	got := stripRunningHeader(page)
	if strings.Contains(got, "MYTH AND REALITY") {
		t.Errorf("expected header stripped, got:\n%s", got)
	}
	if !strings.Contains(got, "Actual body text") {
		t.Errorf("expected body preserved, got:\n%s", got)
	}
}

func TestStripRunningHeaderOCRFormat(t *testing.T) {
	// OCR format: single-newline lines, header embeds page number
	page := "2 MYTH AND REALITY\nActual body text starts here.\nMore body."
	got := stripRunningHeader(page)
	if strings.Contains(got, "MYTH AND REALITY") {
		t.Errorf("expected OCR header stripped, got:\n%s", got)
	}
	if !strings.Contains(got, "Actual body text") {
		t.Errorf("expected body preserved, got:\n%s", got)
	}
}

func TestStripRunningHeaderNoHeader(t *testing.T) {
	page := "Normal body text.\nSecond line.\nThird line."
	got := stripRunningHeader(page)
	if got != page {
		t.Errorf("expected unchanged text, got:\n%s", got)
	}
}

func TestStripRunningHeaderBarePageNumber(t *testing.T) {
	// Bare page number at top of OCR page
	page := "42\nBody content follows here.\nMore content."
	got := stripRunningHeader(page)
	if strings.Contains(got, "42") {
		t.Errorf("expected bare page number stripped, got:\n%s", got)
	}
	if !strings.Contains(got, "Body content") {
		t.Errorf("expected body preserved, got:\n%s", got)
	}
}

// --- processFootnotes ---

func TestProcessFootnotesNoFormFeed(t *testing.T) {
	p := newProcessor()
	text := "Body text.\nMore body.\nFootnote marker."
	opts := ProcessOptions{
		SmartOCR: false, ForceOCR: false,
		StripHeaders: true, StripFootnotes: true, TextOnly: false,
	}
	got := p.processFootnotes(text, opts)
	if got != text {
		t.Errorf("expected unchanged text without form feeds, got:\n%s", got)
	}
}

func TestProcessFootnotesStripsFootnotesAcrossPages(t *testing.T) {
	p := newProcessor()
	opts := ProcessOptions{
		SmartOCR: false, ForceOCR: false,
		StripHeaders: false, StripFootnotes: true, TextOnly: false,
	}

	// Build two pages separated by \f; second page has footnote at bottom.
	var p2lines []string
	for i := 0; i < 18; i++ {
		p2lines = append(p2lines, "Body text.")
	}
	p2lines = append(p2lines, "More body.")
	p2lines = append(p2lines, "Another line.")
	p2lines = append(p2lines, "1")
	p2lines = append(p2lines, "Footnote content here.")
	page2 := strings.Join(p2lines, "\n")

	text := "First page body.\n\f" + page2
	got := p.processFootnotes(text, opts)
	if strings.Contains(got, "Footnote content here.") {
		t.Errorf("expected footnote stripped from page, got:\n%s", got)
	}
	if !strings.Contains(got, "Body text.") {
		t.Errorf("expected body text preserved, got:\n%s", got)
	}
}

func TestProcessFootnotesStripHeadersSkipsFirstPage(t *testing.T) {
	p := newProcessor()
	opts := ProcessOptions{
		SmartOCR: false, ForceOCR: false,
		StripHeaders: true, StripFootnotes: false, TextOnly: false,
	}

	// First page header should NOT be stripped (i==0).
	// Second page header (OCR format) SHOULD be stripped.
	page1 := "HEADER ON FIRST PAGE\nFirst page body text."
	page2 := "42\nSecond page body text."
	text := page1 + "\f" + page2

	got := p.processFootnotes(text, opts)
	// First page header is preserved
	if !strings.Contains(got, "HEADER ON FIRST PAGE") {
		t.Errorf("expected first-page header preserved, got:\n%s", got)
	}
	// Second page header (bare page number) is stripped
	if strings.Contains(got, "42") {
		t.Errorf("expected second-page header stripped, got:\n%s", got)
	}
}

// --- assignImagesToChapters ---

func TestAssignImagesToChapters(t *testing.T) {
	chapters := []EPUBChapter{
		{Title: "Ch1", Content: "content1", Images: nil},
		{Title: "Ch2", Content: "content2", Images: nil},
		{Title: "Ch3", Content: "content3", Images: nil},
	}
	images := []PDFImage{
		{Name: "img1.png", Data: nil, MimeType: "image/png", PageNum: 1, FigNum: 0, Caption: "", WidthFraction: 0},
		{Name: "img2.png", Data: nil, MimeType: "image/png", PageNum: 5, FigNum: 0, Caption: "", WidthFraction: 0},
		{Name: "img3.png", Data: nil, MimeType: "image/png", PageNum: 9, FigNum: 0, Caption: "", WidthFraction: 0},
	}
	assignImagesToChapters(chapters, images, 10)

	total := 0
	for _, ch := range chapters {
		total += len(ch.Images)
	}
	if total != 3 {
		t.Errorf("expected all 3 images assigned, got %d", total)
	}
}

func TestAssignImagesToChaptersZeroTotalPages(t *testing.T) {
	chapters := []EPUBChapter{
		{Title: "Ch1", Content: "c", Images: nil},
	}
	images := []PDFImage{
		{Name: "img.png", Data: nil, MimeType: "image/png", PageNum: 1, FigNum: 0, Caption: "", WidthFraction: 0},
	}
	// totalPages=0 should not panic (guarded to 1 internally)
	assignImagesToChapters(chapters, images, 0)
	if len(chapters[0].Images) != 1 {
		t.Errorf("expected image assigned to only chapter, got %d", len(chapters[0].Images))
	}
}

// --- GenerateFromText ---

func TestGenerateFromTextBasic(t *testing.T) {
	g := newGenerator()
	result, err := g.GenerateFromText("Glava 1\nFirst chapter.\nGlava 2\nSecond chapter.", nil, 0, "Test", "Author", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Title != "Test" {
		t.Errorf("expected title 'Test', got %q", result.Title)
	}
	if result.Author != "Author" {
		t.Errorf("expected author 'Author', got %q", result.Author)
	}
	if len(result.Chapters) < 2 {
		t.Errorf("expected at least 2 chapters, got %d", len(result.Chapters))
	}
}

func TestGenerateFromTextEmpty(t *testing.T) {
	g := newGenerator()
	result, err := g.GenerateFromText("", nil, 0, "Empty", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestGenerateFromTextLanguage(t *testing.T) {
	g := newGenerator()

	t.Run("passes language through", func(t *testing.T) {
		result, err := g.GenerateFromText("Some text.", nil, 0, "Book", "Author", "hr")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Language != "hr" {
			t.Errorf("expected language 'hr', got %q", result.Language)
		}
	})

	t.Run("defaults to en when empty", func(t *testing.T) {
		result, err := g.GenerateFromText("Some text.", nil, 0, "Book", "Author", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Language != "en" {
			t.Errorf("expected language 'en' for empty string, got %q", result.Language)
		}
	})
}

// --- NewDocumentProcessor / ProcessDocument ---

func TestNewDocumentProcessorAndProcess(t *testing.T) {
	p := NewDocumentProcessor(0, nil)
	result, err := p.ProcessDocument(context.Background(), []byte("Hello world"), "text/plain", DefaultProcessOptions(), func(string, ...any) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.PageCount < 1 {
		t.Errorf("expected PageCount >= 1, got %d", result.PageCount)
	}
}

// --- splitIntoChapters falls back to word-count split ---

func TestSplitIntoChaptersFallsBackToWordCount(t *testing.T) {
	g := newGenerator()
	// Build a long text with no headings — should trigger word-count split.
	para := strings.Repeat("word ", 200)
	var paras []string
	for i := 0; i < 16; i++ {
		paras = append(paras, fmt.Sprintf("Paragraph %d: %s", i, para))
	}
	text := strings.Join(paras, "\n\n")
	chapters := g.splitIntoChapters(text)
	if len(chapters) < 2 {
		t.Errorf("expected word-count fallback to produce multiple chapters, got %d", len(chapters))
	}
}

// --- Additional edge case tests ---

func TestProcessOptionsDefault(t *testing.T) {
	opts := DefaultProcessOptions()
	if !opts.SmartOCR {
		t.Error("expected SmartOCR=true")
	}
	if !opts.StripHeaders {
		t.Error("expected StripHeaders=true")
	}
	if !opts.StripFootnotes {
		t.Error("expected StripFootnotes=true")
	}
	if opts.ForceOCR {
		t.Error("expected ForceOCR=false")
	}
	if opts.TextOnly {
		t.Error("expected TextOnly=false")
	}
}

func TestProcessOptionsZeroValue(t *testing.T) {
	opts := ProcessOptions{}
	if opts.SmartOCR {
		t.Error("expected SmartOCR=false for zero value")
	}
	if opts.StripHeaders {
		t.Error("expected StripHeaders=false for zero value")
	}
	if opts.StripFootnotes {
		t.Error("expected StripFootnotes=false for zero value")
	}
}

func TestProcessOptionsWithAllOptions(t *testing.T) {
	opts := ProcessOptions{
		SmartOCR:       true,
		ForceOCR:       true,
		StripHeaders:   false,
		StripFootnotes: false,
		TextOnly:       true,
	}

	if !opts.SmartOCR {
		t.Error("expected SmartOCR=true")
	}
	if !opts.ForceOCR {
		t.Error("expected ForceOCR=true")
	}
	if opts.StripHeaders {
		t.Error("expected StripHeaders=false")
	}
	if opts.StripFootnotes {
		t.Error("expected StripFootnotes=false")
	}
	if !opts.TextOnly {
		t.Error("expected TextOnly=true")
	}
}

func TestEPUBResultStruct(t *testing.T) {
	result := &EPUBResult{
		Title:    "Test Book",
		Author:   "Test Author",
		Language: "en",
		Chapters: []EPUBChapter{
			{Title: "Chapter 1", Content: "Content 1", Images: nil},
			{Title: "Chapter 2", Content: "Content 2", Images: nil},
		},
	}

	if result.Title != "Test Book" {
		t.Errorf("expected Title 'Test Book', got %q", result.Title)
	}
	if result.Author != "Test Author" {
		t.Errorf("expected Author 'Test Author', got %q", result.Author)
	}
	if result.Language != "en" {
		t.Errorf("expected Language 'en', got %q", result.Language)
	}
	if len(result.Chapters) != 2 {
		t.Errorf("expected 2 chapters, got %d", len(result.Chapters))
	}
}

func TestEPUBChapterStruct(t *testing.T) {
	chapter := EPUBChapter{
		Title:   "Test Chapter",
		Content: "Test content",
		Images: []PDFImage{
			{Name: "img1.png", Data: []byte("data"), MimeType: "image/png", PageNum: 1, FigNum: 1, Caption: "Figure 1", WidthFraction: 0.5},
		},
	}

	if chapter.Title != "Test Chapter" {
		t.Errorf("expected Title 'Test Chapter', got %q", chapter.Title)
	}
	if chapter.Content != "Test content" {
		t.Errorf("expected Content 'Test content', got %q", chapter.Content)
	}
	if len(chapter.Images) != 1 {
		t.Errorf("expected 1 image, got %d", len(chapter.Images))
	}
}

func TestPDFImageStruct(t *testing.T) {
	img := PDFImage{
		Name:          "test.png",
		Data:          []byte("image data"),
		MimeType:      "image/png",
		PageNum:       5,
		FigNum:        2,
		Caption:       "Test figure",
		WidthFraction: 0.75,
	}

	if img.Name != "test.png" {
		t.Errorf("expected Name 'test.png', got %q", img.Name)
	}
	if img.PageNum != 5 {
		t.Errorf("expected PageNum 5, got %d", img.PageNum)
	}
	if img.FigNum != 2 {
		t.Errorf("expected FigNum 2, got %d", img.FigNum)
	}
	if img.WidthFraction != 0.75 {
		t.Errorf("expected WidthFraction 0.75, got %f", img.WidthFraction)
	}
}

func TestProcessResultStruct(t *testing.T) {
	result := &ProcessResult{
		ExtractedText: "Extracted text content",
		PageCount:     10,
		ProcessingMs:  500,
		Images: []PDFImage{
			{Name: "fig1.png", Data: nil, MimeType: "image/png", PageNum: 3, FigNum: 1, Caption: "", WidthFraction: 0},
		},
	}

	if result.ExtractedText != "Extracted text content" {
		t.Errorf("expected ExtractedText, got %q", result.ExtractedText)
	}
	if result.PageCount != 10 {
		t.Errorf("expected PageCount 10, got %d", result.PageCount)
	}
	if result.ProcessingMs != 500 {
		t.Errorf("expected ProcessingMs 500, got %d", result.ProcessingMs)
	}
	if len(result.Images) != 1 {
		t.Errorf("expected 1 image, got %d", len(result.Images))
	}
}

func TestSplitOnHeadingsWithMixedContent(t *testing.T) {
	g := newGenerator()

	// Mix of headings and regular text
	text := "Some intro text.\nGlava 1\nChapter one content.\nSome text between.\nGlava 2\nChapter two content."
	chapters := g.splitOnHeadings(text)

	// Should find at least 2 chapters
	if len(chapters) < 2 {
		t.Errorf("expected at least 2 chapters, got %d", len(chapters))
	}
}

func TestSplitOnHeadingsWithWhitespace(t *testing.T) {
	g := newGenerator()

	// Headings with whitespace
	text := "   Glava 1   \nContent here.\n   Glava 2   \nMore content."
	chapters := g.splitOnHeadings(text)

	if len(chapters) < 2 {
		t.Errorf("expected at least 2 chapters, got %d", len(chapters))
	}
}

func TestSplitOnHeadingsRomanNumeralsMultiple(t *testing.T) {
	g := newGenerator()

	// Multiple roman numerals
	text := "I.\nFirst.\nII.\nSecond.\nIII.\nThird.\nIV.\nFourth."
	chapters := g.splitOnHeadings(text)

	if len(chapters) < 4 {
		t.Errorf("expected at least 4 chapters, got %d", len(chapters))
	}
}

func TestSplitByWordCountExactlyAtBoundary(t *testing.T) {
	g := newGenerator()

	// Build text that's exactly around the chapter word boundary
	words := make([]string, 1500) // Exactly at default boundary
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")

	chapters := g.splitByWordCount(text)

	// With exactly 1500 words, should be 1 chapter
	if len(chapters) != 1 {
		t.Errorf("expected 1 chapter with exactly boundary words, got %d", len(chapters))
	}
}

func TestSplitByWordCountOverBoundary(t *testing.T) {
	g := newGenerator()

	// Build text just over the chapter word boundary with paragraph separators.
	// splitByWordCount splits on "\n\n" boundaries, so each paragraph must be
	// separated by a double newline.
	var paras []string
	for i := 0; i < 16; i++ {
		paras = append(paras, strings.Repeat("word ", 100))
	}
	text := strings.Join(paras, "\n\n") // 1600 words across 16 paragraphs

	chapters := g.splitByWordCount(text)

	// Should split into 2 chapters (1500 word boundary)
	if len(chapters) < 2 {
		t.Errorf("expected at least 2 chapters, got %d", len(chapters))
	}
}

func TestAssignImagesToChaptersWithImagesOnChapterBoundaries(t *testing.T) {
	// Images exactly on chapter boundaries
	chapters := []EPUBChapter{
		{Title: "Ch1", Content: "content1", Images: nil},
		{Title: "Ch2", Content: "content2", Images: nil},
	}
	// Image on page 1 (Ch1), page 2 (between chapters), page 3 (Ch2)
	images := []PDFImage{
		{Name: "img1.png", Data: nil, MimeType: "image/png", PageNum: 1, FigNum: 0, Caption: "", WidthFraction: 0},
		{Name: "img2.png", Data: nil, MimeType: "image/png", PageNum: 2, FigNum: 0, Caption: "", WidthFraction: 0},
		{Name: "img3.png", Data: nil, MimeType: "image/png", PageNum: 3, FigNum: 0, Caption: "", WidthFraction: 0},
	}
	assignImagesToChapters(chapters, images, 4)

	// Check distribution
	total := 0
	for _, ch := range chapters {
		total += len(ch.Images)
	}
	if total != 3 {
		t.Errorf("expected all 3 images assigned, got %d", total)
	}
}

func TestAssignImagesToChaptersWithManyImages(t *testing.T) {
	// Many images
	chapters := []EPUBChapter{
		{Title: "Ch1", Content: "content1", Images: nil},
		{Title: "Ch2", Content: "content2", Images: nil},
	}
	images := make([]PDFImage, 100)
	for i := range images {
		images[i] = PDFImage{
			Name:          "img.png",
			Data:          nil,
			MimeType:      "image/png",
			PageNum:       i + 1,
			FigNum:        0,
			Caption:       "",
			WidthFraction: 0,
		}
	}
	assignImagesToChapters(chapters, images, 100)

	// Should distribute roughly equally
	total := 0
	for _, ch := range chapters {
		total += len(ch.Images)
	}
	if total != 100 {
		t.Errorf("expected 100 images assigned, got %d", total)
	}
}

func TestDocumentProcessorClose(t *testing.T) {
	p := NewDocumentProcessor(0, nil)
	// Close should not panic
	err := p.Close()
	if err != nil {
		t.Errorf("unexpected error on close: %v", err)
	}
}
