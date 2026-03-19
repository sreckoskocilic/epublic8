package handler

import (
	"bytes"
	"context"
	"io"
	"log"
	"sync/atomic"

	"epublic8/internal/model"
	"epublic8/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DocumentHandler struct {
	Processor      *model.DocumentProcessor
	activeRequests int32
	pb.UnimplementedDocumentServiceServer
}

func NewDocumentHandler() *DocumentHandler {
	return &DocumentHandler{
		Processor:                          model.NewDocumentProcessor(),
		activeRequests:                     0,
		UnimplementedDocumentServiceServer: pb.UnimplementedDocumentServiceServer{},
	}
}

func (h *DocumentHandler) ProcessDocument(ctx context.Context, req *pb.DocumentRequest) (*pb.DocumentResponse, error) {
	h.incrementRequests()
	defer h.decrementRequests()

	result, err := h.Processor.ProcessDocument(req.Content, req.MimeType, log.Printf)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to process document: %v", err)
	}
	return &pb.DocumentResponse{
		DocumentId:    req.DocumentId,
		ExtractedText: result.ExtractedText,
		Metadata: &pb.DocumentMetadata{
			PageCount:        int32(result.PageCount),
			DetectedLanguage: "en",
			DocumentType:     req.MimeType,
		},
		Stats: &pb.ProcessingStats{
			ProcessingTimeMs: result.ProcessingMs,
			TextLength:       int32(len(result.ExtractedText)),
		},
	}, nil
}

func (h *DocumentHandler) StreamProcessDocument(stream pb.DocumentService_StreamProcessDocumentServer) error {
	h.incrementRequests()
	defer h.decrementRequests()

	var documentID string
	var buf bytes.Buffer
	var totalChunks int32
	chunksReceived := 0

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return status.Errorf(codes.Internal, "failed to receive chunk: %v", err)
		}

		documentID = chunk.DocumentId
		totalChunks = chunk.TotalChunks
		buf.Write(chunk.Content)
		chunksReceived++
	}

	if chunksReceived == 0 {
		return status.Error(codes.InvalidArgument, "stream contained no chunks")
	}

	result, err := h.Processor.ProcessDocument(buf.Bytes(), "application/octet-stream", log.Printf)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to process document: %v", err)
	}

	return stream.Send(&pb.DocumentChunkResponse{
		DocumentId:      documentID,
		ExtractedText:   result.ExtractedText,
		ProcessedChunks: totalChunks,
		IsFinal:         true,
		Stats: &pb.ProcessingStats{
			ProcessingTimeMs: result.ProcessingMs,
		},
	})
}

func (h *DocumentHandler) ExtractEntities(ctx context.Context, req *pb.EntityRequest) (*pb.EntityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ExtractEntities has been removed")
}

func (h *DocumentHandler) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{
		Healthy:        h.Processor.IsLoaded(),
		Status:         "ready",
		ActiveRequests: atomic.LoadInt32(&h.activeRequests),
	}, nil
}

func (h *DocumentHandler) Register(grpcServer *grpc.Server) {
	pb.RegisterDocumentServiceServer(grpcServer, h)
}

func (h *DocumentHandler) incrementRequests() {
	atomic.AddInt32(&h.activeRequests, 1)
}

func (h *DocumentHandler) decrementRequests() {
	atomic.AddInt32(&h.activeRequests, -1)
}

func (h *DocumentHandler) Close() error {
	if h.Processor != nil {
		h.Processor.Close()
	}
	return nil
}
