package model

import (
	"regexp"
	"sort"
	"strings"
)

// smallFontRatio is the fraction of median bounding-box height below which
// an observation is considered caption/metadata rather than body text.
const smallFontRatio = 0.75

// filterSmallFontText removes text lines whose bounding-box height is
// significantly smaller than the page median. Vision OCR produces a 1:1
// mapping between obs entries and text lines (joined by \n), so we can
// directly correlate height with lines.
func filterSmallFontText(text string, obs []visionObs) string {
	if len(obs) < 5 {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) != len(obs) {
		return text
	}

	heights := make([]float64, len(obs))
	for i, o := range obs {
		heights[i] = o.H
	}
	sort.Float64s(heights)
	medianH := heights[len(heights)/2]
	if medianH <= 0 {
		return text
	}
	threshold := medianH * smallFontRatio

	kept := make([]string, 0, len(lines))
	for i, o := range obs {
		if o.H >= threshold {
			kept = append(kept, lines[i])
		}
	}
	return strings.Join(kept, "\n")
}

var (
	lifelineHeaderRe = regexp.MustCompile(`(?i)^LIFE\s?LINE\b`)
	yearLineRe       = regexp.MustCompile(`^\d{3,4}\b`)
)

// stripLifelineNotes removes LIFELINE timeline sidebar blocks.
// Requires at least 2 year-prefixed lines following the header.
func stripLifelineNotes(page string) string {
	lines := strings.Split(page, "\n")
	remove := make([]bool, len(lines))

	for i := range lines {
		if !lifelineHeaderRe.MatchString(strings.TrimSpace(lines[i])) {
			continue
		}
		lookEnd := min(i+9, len(lines))
		yearCount := 0
		for j := i + 1; j < lookEnd; j++ {
			if yearLineRe.MatchString(strings.TrimSpace(lines[j])) {
				yearCount++
			}
		}
		if yearCount < 2 {
			continue
		}
		remove[i] = true
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == "" || !yearLineRe.MatchString(trimmed) {
				break
			}
			remove[j] = true
		}
	}

	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		if !remove[i] {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}
