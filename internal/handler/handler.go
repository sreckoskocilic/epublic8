package handler

import (
	"bytes"
	"context"
	"io"
	"log"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"epublic8/internal/model"
	"epublic8/internal/tracing"
	"epublic8/pb"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// processTimeout is the maximum time allowed for a single document processing request.
const processTimeout = 10 * time.Minute

// Pre-compiled entity extraction patterns, keyed by entity type.
var entityPatterns = map[string][]*regexp.Regexp{
	"PERSON": {
		// First Last name pattern (capitalized words)
		regexp.MustCompile(`\b[A-Z][a-z]+ [A-Z][a-z]+\b`),
		// Titles + Name (Mr., Mrs., Dr., etc.)
		regexp.MustCompile(`\b(?:Mr\.|Mrs\.|Ms\.|Dr\.|Prof\.)\s+[A-Z][a-z]+(?:\s+[A-Z][a-z]+)?\b`),
	},
	"LOCATION": {
		// City, State patterns
		regexp.MustCompile(`\b[A-Z][a-z]+,?\s+[A-Z]{2}\b`),
		// Common location suffixes
		regexp.MustCompile(`\b[A-Z][a-z]+(?:\s+[A-Z][a-z]+)?\s+(?:Street|St\.|Avenue|Ave\.|Road|Rd\.|Boulevard|Blvd\.|Drive|Dr\.|Lane|Ln\.|Way|Court|Ct\.|Place|Pl\.)\b`),
		// Country names
		regexp.MustCompile(`\b(?:United States|United Kingdom|Canada|Australia|Germany|France|Japan|China|India|Brazil|Mexico|Spain|Italy|Russia|Korea)\b`),
	},
	"ORGANIZATION": {
		// Common company suffixes (optional periods) - handles Inc, Corp, LLC, Ltd
		regexp.MustCompile(`\b[A-Z][a-zA-Z]+(?:\s+[A-Z][a-zA-Z]+){0,5}\s+(?:Inc\.?|Corp\.?|LLC|Ltd\.?|Co\.?|Company|Corporation|Group|International|Technologies|Solutions|Services|Labs?)\b`),
		// All-caps suffixes like LLC, LTD
		regexp.MustCompile(`\b[A-Z][a-zA-Z]+(?:\s+[A-Z][a-zA-Z]+){0,5}\s+[A-Z]{2,}\b`),
		// Acronyms followed by common words
		regexp.MustCompile(`\b[A-Z]{2,}\s+(?:University|College|Institute|Foundation|Association|Organization)\b`),
	},
	"DATE": {
		// Various date formats
		regexp.MustCompile(`\b(?:\d{1,2}[/-]\d{1,2}[/-]\d{2,4}|\d{4}[/-]\d{1,2}[/-]\d{1,2})\b`),
		// Month Day, Year
		regexp.MustCompile(`\b(?:January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{1,2},?\s+\d{4}\b`),
		// Day Month Year
		regexp.MustCompile(`\b\d{1,2}\s+(?:January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{4}\b`),
	},
	"EMAIL": {
		regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`),
	},
	"PHONE": {
		// US phone format
		regexp.MustCompile(`\b(?:\+1[-.\s]?)?\(?[0-9]{3}\)?[-.\s]?[0-9]{3}[-.\s]?[0-9]{4}\b`),
	},
}

type DocumentHandler struct {
	Processor      *model.DocumentProcessor
	activeRequests atomic.Int32
	pb.UnimplementedDocumentServiceServer
}

// NewDocumentHandler creates a new DocumentHandler with the given OCR settings.
// If ocrConcurrencyLimit is <= 0, the default (GOMAXPROCS) will be used.
// If ocrLanguages is empty, the default languages ["srp_latn+hrv", "srp_latn", "eng"] will be used.
func NewDocumentHandler(ocrConcurrencyLimit int, ocrLanguages []string) *DocumentHandler {
	return &DocumentHandler{
		Processor:                          model.NewDocumentProcessor(ocrConcurrencyLimit, ocrLanguages),
		activeRequests:                     atomic.Int32{},
		UnimplementedDocumentServiceServer: pb.UnimplementedDocumentServiceServer{},
	}
}

func (h *DocumentHandler) ProcessDocument(ctx context.Context, req *pb.DocumentRequest) (*pb.DocumentResponse, error) {
	ctx, span := tracing.StartSpan(ctx, "ProcessDocument",
		trace.WithAttributes(
			attribute.String("document.id", req.DocumentId),
			attribute.String("document.mime_type", req.MimeType),
		),
	)
	defer span.End()

	h.incrementRequests()
	defer h.decrementRequests()

	// Enforce a processing timeout matching the HTTP path.
	ctx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()

	opts := model.DefaultProcessOptions()
	if req.Options != nil {
		opts.ForceOCR = req.Options.EnableOcr
	}
	result, err := h.Processor.ProcessDocument(ctx, req.Content, req.MimeType, opts, log.Printf)
	if err != nil {
		tracing.AddSpanError(ctx, err)
		return nil, status.Errorf(codes.Internal, "failed to process document: %v", err)
	}
	return &pb.DocumentResponse{
		DocumentId:    req.DocumentId,
		ExtractedText: result.ExtractedText,
		Metadata: &pb.DocumentMetadata{
			PageCount:        int32(result.PageCount),
			DetectedLanguage: result.Language,
			DocumentType:     req.MimeType,
		},
		Stats: &pb.ProcessingStats{
			ProcessingTimeMs: result.ProcessingMs,
			TextLength:       int32(len(result.ExtractedText)),
		},
	}, nil
}

func (h *DocumentHandler) StreamProcessDocument(stream pb.DocumentService_StreamProcessDocumentServer) error {
	ctx, span := tracing.StartSpan(stream.Context(), "StreamProcessDocument")
	defer span.End()

	h.incrementRequests()
	defer h.decrementRequests()

	var documentID string
	var mimeType string
	var chunkOptions *pb.ProcessOptions
	var buf bytes.Buffer
	var totalChunks int32
	chunksReceived := 0

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			tracing.AddSpanError(ctx, err)
			return status.Errorf(codes.Internal, "failed to receive chunk: %v", err)
		}

		documentID = chunk.DocumentId
		totalChunks = chunk.TotalChunks
		if mimeType == "" && chunk.MimeType != "" {
			mimeType = chunk.MimeType
		}
		if chunkOptions == nil && chunk.Options != nil {
			chunkOptions = chunk.Options
		}
		buf.Write(chunk.Content)
		chunksReceived++
	}

	if chunksReceived == 0 {
		return status.Error(codes.InvalidArgument, "stream contained no chunks")
	}
	if totalChunks > 0 && int32(chunksReceived) != totalChunks {
		return status.Errorf(codes.DataLoss, "stream truncated: received %d of %d declared chunks", chunksReceived, totalChunks)
	}

	span.SetAttributes(attribute.Int("stream.chunks_received", chunksReceived))

	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	streamOpts := model.DefaultProcessOptions()
	if chunkOptions != nil {
		streamOpts.ForceOCR = chunkOptions.EnableOcr
	}
	ctx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()
	result, err := h.Processor.ProcessDocument(ctx, buf.Bytes(), mimeType, streamOpts, log.Printf)
	if err != nil {
		tracing.AddSpanError(ctx, err)
		return status.Errorf(codes.Internal, "failed to process document: %v", err)
	}

	return stream.Send(&pb.DocumentChunkResponse{
		DocumentId:       documentID,
		ExtractedText:    result.ExtractedText,
		ProcessedChunks:  totalChunks,
		IsFinal:          true,
		DetectedLanguage: result.Language,
		Stats: &pb.ProcessingStats{
			ProcessingTimeMs: result.ProcessingMs,
		},
	})
}

func (h *DocumentHandler) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{
		Healthy:        h.Processor.IsLoaded(),
		Status:         "ready",
		ActiveRequests: h.activeRequests.Load(),
	}, nil
}

// ExtractEntities extracts named entities from the provided text.
// It uses regex-based pattern matching to identify common entity types:
// PERSON (names), LOCATION (places), ORGANIZATION (companies), DATE (dates),
// EMAIL (email addresses), and PHONE (phone numbers).
func (h *DocumentHandler) ExtractEntities(ctx context.Context, req *pb.EntityRequest) (*pb.EntityResponse, error) {
	_, span := tracing.StartSpan(ctx, "ExtractEntities",
		trace.WithAttributes(
			attribute.String("document.id", req.DocumentId),
		),
	)
	defer span.End()

	if req.Text == "" {
		return &pb.EntityResponse{
			DocumentId: req.DocumentId,
			Entities:   []*pb.Entity{},
		}, nil
	}

	const maxTextBytes = 1 << 20 // 1 MB limit for regex matching
	if len(req.Text) > maxTextBytes {
		return nil, status.Errorf(codes.InvalidArgument, "text too large for entity extraction (max 1 MB)")
	}

	// Determine which entity types to extract
	// If none specified, extract all supported types
	typesToExtract := req.EntityTypes
	if len(typesToExtract) == 0 {
		typesToExtract = []string{"PERSON", "LOCATION", "ORGANIZATION", "DATE", "EMAIL", "PHONE"}
	}

	var entities []*pb.Entity

	// Extract each requested entity type
	for _, entityType := range typesToExtract {
		patterns := getEntityPatterns(entityType)
		for _, pattern := range patterns {
			matches := pattern.FindAllStringIndex(req.Text, -1)
			for _, match := range matches {
				entities = append(entities, &pb.Entity{
					Text:        req.Text[match[0]:match[1]],
					Type:        entityType,
					Confidence:  0.8, // Regex-based extraction has moderate confidence
					StartOffset: strconv.Itoa(match[0]),
					EndOffset:   strconv.Itoa(match[1]),
				})
			}
		}
	}

	// Remove duplicates based on text and type
	entities = deduplicateEntities(entities)

	span.SetAttributes(attribute.Int("entities.count", len(entities)))

	return &pb.EntityResponse{
		DocumentId: req.DocumentId,
		Entities:   entities,
	}, nil
}

// getEntityPatterns returns pre-compiled regex patterns for the specified entity type.
func getEntityPatterns(entityType string) []*regexp.Regexp {
	return entityPatterns[entityType]
}

// deduplicateEntities removes duplicate entities based on text and type.
func deduplicateEntities(entities []*pb.Entity) []*pb.Entity {
	seen := make(map[string]bool)
	var result []*pb.Entity

	for _, e := range entities {
		key := e.Type + ":" + e.Text
		if !seen[key] {
			seen[key] = true
			result = append(result, e)
		}
	}

	return result
}

func (h *DocumentHandler) Register(grpcServer *grpc.Server) {
	pb.RegisterDocumentServiceServer(grpcServer, h)
}

func (h *DocumentHandler) incrementRequests() {
	h.activeRequests.Add(1)
}

func (h *DocumentHandler) decrementRequests() {
	h.activeRequests.Add(-1)
}

func (h *DocumentHandler) Close() error {
	if h.Processor != nil {
		h.Processor.Close()
	}
	return nil
}
