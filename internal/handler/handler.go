package handler

import (
	"sync/atomic"

	"epublic8/internal/model"
)

type DocumentHandler struct {
	Processor      *model.DocumentProcessor
	activeRequests atomic.Int32
}

func NewDocumentHandler(ocrConcurrencyLimit int, ocrLanguages []string) *DocumentHandler {
	return &DocumentHandler{
		Processor:      model.NewDocumentProcessor(ocrConcurrencyLimit, ocrLanguages),
		activeRequests: atomic.Int32{},
	}
}

func (h *DocumentHandler) Close() error {
	if h.Processor != nil {
		h.Processor.Close()
	}
	return nil
}
