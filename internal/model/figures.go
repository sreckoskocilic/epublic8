package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // register JPEG decoder
	"image/png"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// visionObs is one text observation from vision_ocr JSON output.
// Coordinates follow Vision convention: origin bottom-left, Y=0 is bottom of
// image, Y=1 is top. So the box spans [Y, Y+H] in Vision coords.
type visionObs struct {
	T string  `json:"t"`
	X float64 `json:"x"`
	Y float64 `json:"y"` // bottom edge of box (Vision: 0=bottom of image)
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// top returns the Vision-coord top of the observation (higher Y = higher in image).
func (o visionObs) top() float64 { return o.Y + o.H }

type visionPageResult struct {
	Text string      `json:"text"`
	Obs  []visionObs `json:"obs"`
}

// runVisionOCR runs the compiled vision_ocr binary on imgPath and returns the
// structured result. Returns zero value (empty text, nil obs) on any failure.
func runVisionOCR(imgPath, tesseractLang string) visionPageResult {
	visionOCROnce.Do(initVisionOCR)
	if visionOCRBin == "" {
		return visionPageResult{Text: "", Obs: nil}
	}
	lang := "en-US"
	first := strings.SplitN(tesseractLang, "+", 2)[0]
	if mapped, ok := tesseractLangToVision[first]; ok {
		lang = mapped
	}
	out, err := exec.Command(visionOCRBin, "--image", imgPath, "--language", lang).Output()
	if err != nil {
		return visionPageResult{Text: "", Obs: nil}
	}
	var res visionPageResult
	if jsonErr := json.Unmarshal(out, &res); jsonErr != nil {
		return visionPageResult{Text: "", Obs: nil}
	}
	return res
}

// figureCapRe matches "Fig. N" or "Fig N" at the start of an observation.
var figureCapRe = regexp.MustCompile(`(?i)^fig\.?\s*(\d+)`)

// cropFigures analyses Vision observations from a single page image and returns
// cropped PDFImages for each detected figure. Two detection strategies are used:
//
//  1. Gap-based: a vertical gap > minGap followed by a "Fig. N" caption.
//  2. Side-by-side: a "Fig. N" caption with all text above it in a right column
//     (x > rightColThreshold), meaning the figure occupies the left column.
//
// imgPath must be the PNG rendered by pdftoppm for this page (still on disk).
func cropFigures(imgPath string, obs []visionObs, pageNum int) []PDFImage {
	if len(obs) < 2 || imgPath == "" {
		return nil
	}

	// Sort observations top-to-bottom: highest Vision-Y first.
	sorted := make([]visionObs, len(obs))
	copy(sorted, obs)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].top() > sorted[j].top()
	})

	// Load the page image for cropping.
	f, err := os.Open(imgPath)
	if err != nil {
		return nil
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return nil
	}
	imgW := img.Bounds().Dx()
	imgH := img.Bounds().Dy()

	const (
		minGap         = 0.04  // minimum Vision-coord gap to consider a figure region
		minHeight      = 0.05  // minimum figure height (fraction of page) to keep
		capLineSep     = 0.020 // max gap between caption continuation lines
		rightColThresh = 0.40  // x threshold: observations left of this are NOT right-column
	)

	figSeen := map[int]bool{}
	var figures []PDFImage

	// appendFig encodes a crop and appends it to figures.
	appendFig := func(figNum, topPx, bottomPx, leftPx, rightPx int, caption string) {
		if topPx < 0 {
			topPx = 0
		}
		if bottomPx > imgH {
			bottomPx = imgH
		}
		if rightPx > imgW {
			rightPx = imgW
		}
		if bottomPx <= topPx || rightPx <= leftPx {
			return
		}
		rect := image.Rect(leftPx, topPx, rightPx, bottomPx)
		cropped := cropImage(img, rect)
		var buf bytes.Buffer
		if err := png.Encode(&buf, cropped); err != nil {
			return
		}
		wf := float64(rightPx-leftPx) / float64(imgW)
		if wf <= 0 || wf > 1 {
			wf = 1
		}
		figures = append(figures, PDFImage{
			Name:          fmt.Sprintf("fig%03d_p%d.png", figNum, pageNum),
			Data:          buf.Bytes(),
			MimeType:      "image/png",
			PageNum:       pageNum,
			FigNum:        figNum,
			Caption:       caption,
			WidthFraction: wf,
		})
		figSeen[figNum] = true
	}

	// collectCaption gathers the single-line or multi-line caption starting at
	// sorted[start]. Returns (capMinY, endIndex, captionText).
	collectCaption := func(start int) (float64, int, string) {
		capMinY := sorted[start].Y
		j := start
		for j+1 < len(sorted) {
			separation := sorted[j].Y - sorted[j+1].top()
			if separation > capLineSep {
				break
			}
			capMinY = sorted[j+1].Y
			j++
		}
		var lines []string
		for k := start; k <= j; k++ {
			lines = append(lines, strings.TrimSpace(sorted[k].T))
		}
		caption := strings.Join(lines, " ")
		const maxCaptionLen = 500
		if len(caption) > maxCaptionLen {
			caption = caption[:maxCaptionLen]
		}
		return capMinY, j, caption
	}

	// ── Pass 1: Gap-based detection ───────────────────────────────────────────
	for i := 0; i < len(sorted)-1; i++ {
		above := sorted[i]
		below := sorted[i+1]

		gap := above.Y - below.top()
		if gap < minGap {
			continue
		}

		m := figureCapRe.FindStringSubmatch(strings.TrimSpace(below.T))
		if m == nil {
			continue
		}
		figNum, _ := strconv.Atoi(m[1])

		capMinY, j, caption := collectCaption(i + 1)

		figTopVision := above.Y
		figBottomVision := capMinY
		if figTopVision-figBottomVision < minHeight {
			i = j
			continue
		}

		topPx := int((1.0 - figTopVision) * float64(imgH))
		bottomPx := int((1.0 - figBottomVision) * float64(imgH))
		appendFig(figNum, topPx, bottomPx, 0, imgW, caption)
		i = j
	}

	// ── Pass 2: Side-by-side detection ───────────────────────────────────────
	// Figures placed beside text: all observations above the caption are in a
	// right column (x > rightColThresh); the left column is the figure.
	for ci, capObs := range sorted {
		m := figureCapRe.FindStringSubmatch(strings.TrimSpace(capObs.T))
		if m == nil {
			continue
		}
		figNum, _ := strconv.Atoi(m[1])
		if figSeen[figNum] {
			continue
		}

		// Gather observations above this caption (earlier in sorted = higher up).
		aboveSlice := sorted[:ci]
		if len(aboveSlice) == 0 {
			continue
		}

		// All of them must be in the right column.
		allRight := true
		minX := 1.0
		maxTop := 0.0
		for _, o := range aboveSlice {
			if o.X < rightColThresh {
				allRight = false
				break
			}
			if o.X < minX {
				minX = o.X
			}
			if o.top() > maxTop {
				maxTop = o.top()
			}
		}
		if !allRight || minX < 0.2 {
			continue
		}

		figTopVision := maxTop
		figBottomVision := capObs.Y // include caption line in crop
		if figTopVision-figBottomVision < minHeight {
			continue
		}

		// Right edge of figure = left edge of right-column text.
		rightPx := int(minX * float64(imgW))
		topPx := int((1.0 - figTopVision) * float64(imgH))
		bottomPx := int((1.0 - figBottomVision) * float64(imgH))

		caption := strings.TrimSpace(capObs.T)
		appendFig(figNum, topPx, bottomPx, 0, rightPx, caption)
	}

	return figures
}

// cropImage returns the sub-region of src defined by rect.
// It uses SubImage when available (all standard image types support it),
// otherwise falls back to pixel-by-pixel copy.
func cropImage(src image.Image, rect image.Rectangle) image.Image {
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if si, ok := src.(subImager); ok {
		return si.SubImage(rect)
	}
	dst := image.NewNRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), src, rect.Min, draw.Src)
	return dst
}
