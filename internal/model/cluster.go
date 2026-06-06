package model

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// FontCluster represents a group of text observations with similar font size.
type FontCluster struct {
	ID           int      `json:"id"`
	MinH         float64  `json:"min_h"`
	MaxH         float64  `json:"max_h"`
	AvgH         float64  `json:"avg_h"`
	LineCount    int      `json:"line_count"`
	RelativeSize string   `json:"relative_size"`
	SampleTexts  []string `json:"sample_texts"`
}

// ClusterResult is returned by the analyze step.
type ClusterResult struct {
	Clusters     []FontCluster `json:"clusters"`
	PageCount    int           `json:"page_count"`
	SampledPages int           `json:"sampled_pages"`
	Fingerprint  string        `json:"fingerprint"`
}

const (
	gapThreshold     = 0.12
	minClusterFrac   = 0.01
	maxClusters      = 6
	maxSampleTexts   = 5
	fingerprintLimit = 200
)

type indexedObs struct {
	obs     visionObs
	pageIdx int
	pageNum int
}

// ClusterObservations groups Vision OCR observations by font size using
// natural-breaks clustering on bounding-box height. pageNumbers maps each
// allObs index to the actual PDF page number; nil uses 1-based index.
func ClusterObservations(allObs [][]visionObs, pageNumbers []int) ClusterResult {
	var indexed []indexedObs
	for pi, pageObs := range allObs {
		pn := pi + 1
		if pi < len(pageNumbers) {
			pn = pageNumbers[pi]
		}
		for _, o := range pageObs {
			if o.H > 0 {
				indexed = append(indexed, indexedObs{obs: o, pageIdx: pi, pageNum: pn})
			}
		}
	}
	if len(indexed) == 0 {
		return ClusterResult{
			Clusters:     nil,
			PageCount:    0,
			SampledPages: len(allObs),
			Fingerprint:  "",
		}
	}

	sort.Slice(indexed, func(i, j int) bool {
		return indexed[i].obs.H < indexed[j].obs.H
	})

	trimmed := trimOutliers(indexed, 0.005)
	if len(trimmed) == 0 {
		trimmed = indexed
	}

	groups := findNaturalBreaks(trimmed)
	groups = mergeSmallClusters(groups, len(trimmed))
	groups = capClusters(groups)

	clusters := buildClusters(groups, len(allObs))
	labelClusters(clusters)
	fingerprint := computeFingerprint(clusters)

	return ClusterResult{
		Clusters:     clusters,
		PageCount:    0,
		SampledPages: len(allObs),
		Fingerprint:  fingerprint,
	}
}

func trimOutliers(sorted []indexedObs, frac float64) []indexedObs {
	n := len(sorted)
	trim := int(float64(n) * frac)
	if trim*2 >= n {
		return sorted
	}
	return sorted[trim : n-trim]
}

func findNaturalBreaks(sorted []indexedObs) [][]indexedObs {
	if len(sorted) == 0 {
		return nil
	}

	bucketMap := map[int]int{}
	for _, o := range sorted {
		key := int(math.Round(o.obs.H * 1000))
		bucketMap[key]++
	}
	keys := make([]int, 0, len(bucketMap))
	for k := range bucketMap {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	var breakpoints []float64
	for i := 1; i < len(keys); i++ {
		prev := float64(keys[i-1])
		cur := float64(keys[i])
		if prev > 0 && (cur-prev)/prev > gapThreshold {
			breakpoints = append(breakpoints, (float64(keys[i-1])+float64(keys[i]))/2/1000)
		}
	}

	if len(breakpoints) == 0 {
		return [][]indexedObs{sorted}
	}

	var groups [][]indexedObs
	start := 0
	bp := 0
	for i, o := range sorted {
		if bp < len(breakpoints) && o.obs.H > breakpoints[bp] {
			groups = append(groups, sorted[start:i])
			start = i
			bp++
		}
	}
	groups = append(groups, sorted[start:])
	return groups
}

func mergeSmallClusters(groups [][]indexedObs, total int) [][]indexedObs {
	minSize := int(math.Ceil(float64(total) * minClusterFrac))
	if minSize < 1 {
		minSize = 1
	}
	for {
		smallIdx := -1
		for i, g := range groups {
			if len(g) < minSize {
				smallIdx = i
				break
			}
		}
		if smallIdx < 0 || len(groups) <= 1 {
			break
		}
		mergeTarget := nearestGroup(groups, smallIdx)
		groups = mergeGroups(groups, smallIdx, mergeTarget)
	}
	return groups
}

func nearestGroup(groups [][]indexedObs, idx int) int {
	avgH := groupAvgH(groups[idx])
	bestDist := math.MaxFloat64
	best := 0
	for i, g := range groups {
		if i == idx {
			continue
		}
		dist := math.Abs(groupAvgH(g) - avgH)
		if dist < bestDist {
			bestDist = dist
			best = i
		}
	}
	return best
}

func groupAvgH(group []indexedObs) float64 {
	if len(group) == 0 {
		return 0
	}
	sum := 0.0
	for _, o := range group {
		sum += o.obs.H
	}
	return sum / float64(len(group))
}

func mergeGroups(groups [][]indexedObs, a, b int) [][]indexedObs {
	if a > b {
		a, b = b, a
	}
	merged := append(groups[a], groups[b]...)
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].obs.H < merged[j].obs.H
	})
	result := make([][]indexedObs, 0, len(groups)-1)
	for i, g := range groups {
		if i == a {
			result = append(result, merged)
		} else if i != b {
			result = append(result, g)
		}
	}
	return result
}

func capClusters(groups [][]indexedObs) [][]indexedObs {
	for len(groups) > maxClusters {
		bestI, bestJ := 0, 1
		bestDist := math.MaxFloat64
		for i := 0; i < len(groups)-1; i++ {
			dist := math.Abs(groupAvgH(groups[i]) - groupAvgH(groups[i+1]))
			if dist < bestDist {
				bestDist = dist
				bestI, bestJ = i, i+1
			}
		}
		groups = mergeGroups(groups, bestI, bestJ)
	}
	return groups
}

func buildClusters(groups [][]indexedObs, totalPages int) []FontCluster {
	clusters := make([]FontCluster, len(groups))
	for i, g := range groups {
		minH := g[0].obs.H
		maxH := g[len(g)-1].obs.H
		avgH := groupAvgH(g)

		samples := sampleConsecutiveLines(g)

		relSize := ""
		if totalPages >= 3 {
			relSize = detectRunningHeader(g, totalPages)
		}

		clusters[i] = FontCluster{
			ID:           i + 1,
			MinH:         minH,
			MaxH:         maxH,
			AvgH:         avgH,
			LineCount:    len(g),
			RelativeSize: relSize,
			SampleTexts:  samples,
		}
	}
	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].AvgH > clusters[j].AvgH
	})
	for i := range clusters {
		clusters[i].ID = i + 1
	}
	return clusters
}

func detectRunningHeader(g []indexedObs, totalPages int) string {
	textCounts := map[string]int{}
	pagesSeen := map[int]bool{}
	for _, o := range g {
		norm := strings.ToUpper(strings.TrimSpace(o.obs.T))
		if norm == "" {
			continue
		}
		textCounts[norm]++
		pagesSeen[o.pageIdx] = true
	}
	var maxRepeat int
	for _, cnt := range textCounts {
		if cnt > maxRepeat {
			maxRepeat = cnt
		}
	}
	pageRatio := float64(len(pagesSeen)) / float64(totalPages)
	repeatRatio := float64(maxRepeat) / float64(len(g))
	if pageRatio > 0.5 && repeatRatio > 0.4 {
		return "running_header"
	}
	return ""
}

func labelClusters(clusters []FontCluster) {
	if len(clusters) == 0 {
		return
	}

	bodyIdx := 0
	maxLines := 0
	for i, c := range clusters {
		if c.RelativeSize == "running_header" {
			continue
		}
		if c.LineCount > maxLines {
			maxLines = c.LineCount
			bodyIdx = i
		}
	}
	bodyAvgH := clusters[bodyIdx].AvgH

	for i := range clusters {
		if clusters[i].RelativeSize == "running_header" {
			continue
		}
		switch {
		case i < bodyIdx:
			if clusters[i].AvgH > bodyAvgH*1.3 {
				clusters[i].RelativeSize = "heading"
			} else {
				clusters[i].RelativeSize = "subheading"
			}
		case i == bodyIdx:
			clusters[i].RelativeSize = "body"
		case clusters[i].AvgH < bodyAvgH*0.50:
			clusters[i].RelativeSize = "metadata"
		default:
			clusters[i].RelativeSize = "caption"
		}
	}
}

func computeFingerprint(clusters []FontCluster) string {
	// Use text from the largest-H cluster (headings).
	if len(clusters) == 0 {
		return ""
	}
	var parts []string
	for _, c := range clusters {
		if c.RelativeSize == "heading" || c.RelativeSize == "subheading" {
			for _, s := range c.SampleTexts {
				parts = append(parts, stripPagePrefix(s))
			}
		}
	}
	if len(parts) == 0 && len(clusters[0].SampleTexts) > 0 {
		for _, s := range clusters[0].SampleTexts {
			parts = append(parts, stripPagePrefix(s))
		}
	}

	raw := strings.Join(parts, " ")
	raw = normalizeFingerprint(raw)
	if len(raw) > fingerprintLimit {
		raw = raw[:fingerprintLimit]
	}
	return raw
}

func normalizeFingerprint(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSpace = false
		} else if !prevSpace {
			b.WriteRune(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// classifyObs returns the cluster ID for a given observation based on H range.
func classifyObs(o visionObs, clusters []FontCluster) int {
	bestID := 0
	bestDist := math.MaxFloat64
	for _, c := range clusters {
		if o.H >= c.MinH && o.H <= c.MaxH {
			return c.ID
		}
		mid := (c.MinH + c.MaxH) / 2
		dist := math.Abs(o.H - mid)
		if dist < bestDist {
			bestDist = dist
			bestID = c.ID
		}
	}
	return bestID
}

// FilterObsByCluster keeps only observations whose font-size cluster is in
// selectedIDs. Returns filtered text and filtered obs, maintaining 1:1 alignment.
func FilterObsByCluster(text string, obs []visionObs, selectedIDs []int, clusters []FontCluster) (string, []visionObs) {
	lines := strings.Split(text, "\n")
	if len(lines) != len(obs) {
		return text, obs
	}

	selected := make(map[int]bool, len(selectedIDs))
	for _, id := range selectedIDs {
		selected[id] = true
	}

	var filteredLines []string
	var filteredObs []visionObs
	for i, o := range obs {
		cid := classifyObs(o, clusters)
		if selected[cid] {
			filteredLines = append(filteredLines, lines[i])
			filteredObs = append(filteredObs, o)
		}
	}
	return strings.Join(filteredLines, "\n"), filteredObs
}

const sampleBlockLines = 4

func sampleConsecutiveLines(g []indexedObs) []string {
	pageGroups := map[int][]indexedObs{}
	for _, o := range g {
		pageGroups[o.pageIdx] = append(pageGroups[o.pageIdx], o)
	}

	repeatingHeaders := detectRepeatingTopText(pageGroups)

	bestPage := -1
	bestCount := 0
	for pg, obs := range pageGroups {
		if len(obs) > bestCount {
			bestCount = len(obs)
			bestPage = pg
		}
	}
	if bestPage < 0 {
		return nil
	}

	pageObs := pageGroups[bestPage]
	sort.Slice(pageObs, func(a, b int) bool {
		return pageObs[a].obs.Y > pageObs[b].obs.Y
	})

	start := 0
	for i, o := range pageObs {
		t := strings.TrimSpace(o.obs.T)
		if len(t) >= 15 && !repeatingHeaders[t] {
			start = i
			break
		}
	}

	end := start + sampleBlockLines
	if end > len(pageObs) {
		end = len(pageObs)
	}

	var lines []string
	for _, o := range pageObs[start:end] {
		t := strings.TrimSpace(o.obs.T)
		if t != "" && !repeatingHeaders[t] {
			lines = append(lines, t)
		}
	}
	if len(lines) == 0 {
		return nil
	}

	pn := pageObs[start].pageNum
	return []string{fmt.Sprintf("p.%d:\n%s", pn, strings.Join(lines, "\n"))}
}

// detectRepeatingTopText finds text that appears as the topmost observation
// on many pages — running headers mixed into a body-size cluster.
func detectRepeatingTopText(pageGroups map[int][]indexedObs) map[string]bool {
	topTextCounts := map[string]int{}
	for _, obs := range pageGroups {
		if len(obs) == 0 {
			continue
		}
		// Find topmost obs (highest Y).
		top := obs[0]
		for _, o := range obs[1:] {
			if o.obs.Y > top.obs.Y {
				top = o
			}
		}
		t := strings.TrimSpace(top.obs.T)
		if t != "" && len(strings.Fields(t)) <= 6 {
			topTextCounts[t]++
		}
	}

	threshold := len(pageGroups) * 30 / 100
	if threshold < 3 {
		threshold = 3
	}
	result := map[string]bool{}
	for text, count := range topTextCounts {
		if count >= threshold {
			result[text] = true
		}
	}
	return result
}

func stripPagePrefix(s string) string {
	if after, ok := strings.CutPrefix(s, "p."); ok {
		for i, c := range after {
			if c == ':' {
				rest := after[i+1:]
				rest = strings.TrimLeft(rest, "\n ")
				return rest
			}
		}
	}
	return s
}
