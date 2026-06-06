package model

import (
	"strings"
	"testing"
)

func TestFilterSmallFontText(t *testing.T) {
	// Simulate a page with body text (H=0.020) and captions (H=0.012).
	// median H = 0.020, threshold = 0.015 → captions filtered.
	obs := []visionObs{
		{T: "Italian Renaissance", X: 0.1, Y: 0.9, W: 0.5, H: 0.035},
		{T: "The period saw great innovation in art.", X: 0.1, Y: 0.8, W: 0.8, H: 0.020},
		{T: "Artists developed new techniques for", X: 0.1, Y: 0.75, W: 0.8, H: 0.020},
		{T: "depicting perspective and light.", X: 0.1, Y: 0.70, W: 0.8, H: 0.020},
		{T: "Florence became the center of this movement.", X: 0.1, Y: 0.65, W: 0.8, H: 0.020},
		{T: "MASACCIO Holy Trinity", X: 0.1, Y: 0.30, W: 0.4, H: 0.012},
		{T: "1425-28, Fresco, 667 x 317 cm", X: 0.1, Y: 0.27, W: 0.4, H: 0.012},
		{T: "Santa Maria Novella, Florence, Italy", X: 0.1, Y: 0.24, W: 0.4, H: 0.012},
	}
	text := strings.Join([]string{
		"Italian Renaissance",
		"The period saw great innovation in art.",
		"Artists developed new techniques for",
		"depicting perspective and light.",
		"Florence became the center of this movement.",
		"MASACCIO Holy Trinity",
		"1425-28, Fresco, 667 x 317 cm",
		"Santa Maria Novella, Florence, Italy",
	}, "\n")

	got := filterSmallFontText(text, obs)

	if strings.Contains(got, "MASACCIO") {
		t.Error("caption text not filtered")
	}
	if strings.Contains(got, "Fresco") {
		t.Error("dimensions line not filtered")
	}
	if strings.Contains(got, "Santa Maria") {
		t.Error("museum line not filtered")
	}
	if !strings.Contains(got, "Italian Renaissance") {
		t.Error("heading incorrectly filtered")
	}
	if !strings.Contains(got, "great innovation") {
		t.Error("body text incorrectly filtered")
	}
	if !strings.Contains(got, "Florence became") {
		t.Error("body text incorrectly filtered")
	}
}

func TestFilterSmallFontTextTooFewObs(t *testing.T) {
	obs := []visionObs{
		{T: "Short page", X: 0.1, Y: 0.9, W: 0.3, H: 0.02},
	}
	text := "Short page"
	got := filterSmallFontText(text, obs)
	if got != text {
		t.Error("should return text unchanged with < 5 obs")
	}
}

func TestFilterSmallFontTextMismatch(t *testing.T) {
	obs := []visionObs{
		{T: "A", X: 0, Y: 0.9, W: 0.1, H: 0.02},
		{T: "B", X: 0, Y: 0.8, W: 0.1, H: 0.02},
		{T: "C", X: 0, Y: 0.7, W: 0.1, H: 0.02},
		{T: "D", X: 0, Y: 0.6, W: 0.1, H: 0.02},
		{T: "E", X: 0, Y: 0.5, W: 0.1, H: 0.02},
	}
	text := "A\nB\nC" // 3 lines != 5 obs
	got := filterSmallFontText(text, obs)
	if got != text {
		t.Error("should return text unchanged on obs/line mismatch")
	}
}

func TestFilterSmallFontTextUniformSize(t *testing.T) {
	obs := []visionObs{
		{T: "Line one", X: 0.1, Y: 0.9, W: 0.5, H: 0.020},
		{T: "Line two", X: 0.1, Y: 0.85, W: 0.5, H: 0.020},
		{T: "Line three", X: 0.1, Y: 0.80, W: 0.5, H: 0.020},
		{T: "Line four", X: 0.1, Y: 0.75, W: 0.5, H: 0.020},
		{T: "Line five", X: 0.1, Y: 0.70, W: 0.5, H: 0.020},
	}
	text := "Line one\nLine two\nLine three\nLine four\nLine five"
	got := filterSmallFontText(text, obs)
	if got != text {
		t.Error("uniform font size should keep all lines")
	}
}

func TestStripLifelineNotes(t *testing.T) {
	input := "Body text before.\n" +
		"LIFELINE\n" +
		"1452 Born in Vinci\n" +
		"1466 Enters workshop\n" +
		"1482 Moves to Milan\n" +
		"1519 Dies at Amboise\n" +
		"Body text after."

	got := stripLifelineNotes(input)

	if strings.Contains(got, "LIFELINE") {
		t.Error("LIFELINE header not stripped")
	}
	if strings.Contains(got, "1452") {
		t.Error("year entries not stripped")
	}
	if !strings.Contains(got, "Body text before") {
		t.Error("preceding body text removed")
	}
	if !strings.Contains(got, "Body text after") {
		t.Error("following body text removed")
	}
}

func TestStripLifelineNotesRequiresYears(t *testing.T) {
	input := "LIFELINE\nThis is just text without years."
	got := stripLifelineNotes(input)
	if got != input {
		t.Error("non-timeline LIFELINE block incorrectly stripped")
	}
}

func TestStripLifelineNotesCaseInsensitive(t *testing.T) {
	input := "LifeLine\n1400 Event one\n1450 Event two\n1500 Event three\nBody text."
	got := stripLifelineNotes(input)
	if strings.Contains(got, "LifeLine") {
		t.Error("case-insensitive LIFELINE not detected")
	}
	if !strings.Contains(got, "Body text.") {
		t.Error("body text after LIFELINE removed")
	}
}
