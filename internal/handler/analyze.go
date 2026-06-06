package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"epublic8/internal/errors"
	"epublic8/internal/model"
)

type analyzeResponse struct {
	Clusters       []model.FontCluster `json:"clusters"`
	PageCount      int                 `json:"page_count"`
	SampledPages   int                 `json:"sampled_pages"`
	Fingerprint    string              `json:"fingerprint"`
	MatchedProfile *matchedProfileInfo `json:"matched_profile"`
}

type matchedProfileInfo struct {
	Title       string `json:"title"`
	SelectedIDs []int  `json:"selected_ids"`
}

func (h *WebHandler) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Per-IP rate limit, shared with handleUpload.
	ip := clientIP(r)
	limiterVal, ok := h.uploadLimiters.Load(ip)
	if !ok {
		limiterVal, _ = h.uploadLimiters.LoadOrStore(ip, rate.NewLimiter(rate.Every(6*time.Second), uploadBurst))
	}
	if !limiterVal.(*rate.Limiter).Allow() {
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<30) // 1 GB limit

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ct := header.Header.Get("Content-Type")
	if ct != "application/pdf" && ct != "" {
		// Also accept by extension (case-insensitive).
		if !strings.HasSuffix(strings.ToLower(header.Filename), ".pdf") {
			http.Error(w, "Only PDF files can be analyzed", http.StatusBadRequest)
			return
		}
	}

	tmpFile, err := os.CreateTemp("", "analyze_*.pdf")
	if err != nil {
		http.Error(w, "Failed to create temp file", http.StatusInternalServerError)
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, file); err != nil {
		tmpFile.Close()
		http.Error(w, "Failed to save upload", http.StatusInternalServerError)
		return
	}
	tmpFile.Close()

	var pageFrom, pageTo int
	if v := r.FormValue("page_from"); v != "" {
		pageFrom, _ = strconv.Atoi(v)
	}
	if v := r.FormValue("page_to"); v != "" {
		pageTo, _ = strconv.Atoi(v)
	}

	logf := func(format string, args ...any) {
		errors.LogInfo(format, args...)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()

	result, err := h.documentHandler.Processor.AnalyzePDF(ctx, tmpPath, pageFrom, pageTo, logf)
	if err != nil {
		http.Error(w, sanitizeErrorMessage(fmt.Sprintf("Analysis failed: %v", err)), http.StatusInternalServerError)
		return
	}

	resp := analyzeResponse{
		Clusters:       result.Clusters,
		PageCount:      result.PageCount,
		SampledPages:   result.SampledPages,
		Fingerprint:    result.Fingerprint,
		MatchedProfile: nil,
	}

	if h.profileStore != nil {
		if profile, ok := h.profileStore.Match(result.Fingerprint); ok {
			resp.MatchedProfile = &matchedProfileInfo{
				Title:       profile.Title,
				SelectedIDs: profile.SelectedIDs,
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	//nolint:errcheck
	json.NewEncoder(w).Encode(resp)
}
