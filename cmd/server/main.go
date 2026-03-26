package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"epublic8/internal/config"
	"epublic8/internal/handler"
	"epublic8/internal/metrics"
	"epublic8/internal/model"
	"epublic8/internal/tracing"
	"google.golang.org/grpc"
)

const pidFile = "document-service.pid"

func writePID() {
	// Remove a stale PID file from a previous crash before writing a new one.
	if data, err := os.ReadFile(pidFile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if proc, err := os.FindProcess(pid); err == nil {
				// Signal 0 checks existence without sending a real signal.
				if proc.Signal(syscall.Signal(0)) != nil {
					log.Printf("removing stale PID file (pid %d no longer running)", pid)
					os.Remove(pidFile)
				}
			}
		}
	}
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0600); err != nil {
		log.Printf("warning: could not write PID file: %v", err)
	}
}

func removePID() {
	os.Remove(pidFile)
}

func main() {
	writePID()
	defer removePID()

	// Load configuration from file and environment variables
	cfg, err := config.LoadFromFlag()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("Using configuration:\n%s", cfg.String())

	// Initialize tracing
	tracingCleanup, err := tracing.Init(cfg.Tracing)
	if err != nil {
		log.Printf("warning: failed to initialize tracing: %v", err)
	}
	defer func() {
		if err := tracingCleanup(); err != nil {
			log.Printf("warning: tracing cleanup error: %v", err)
		}
	}()

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(100 * 1024 * 1024),
	)

	model.LogToolAvailability(log.Printf)

	docHandler := handler.NewDocumentHandler(cfg.OCR.Concurrency, cfg.OCR.Languages)
	defer docHandler.Close()

	docHandler.Register(grpcServer)

	webHandler, err := handler.NewWebHandler(docHandler, cfg.EPUB.OutputDir, cfg.Security, cfg.Metrics, cfg.EPUB.ChapterWords)
	if err != nil {
		log.Fatalf("failed to initialize web handler: %v", err)
	}
	defer webHandler.Close()

	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	if cfg.EPUB.OutputDir != "" && cfg.Cleanup.Enabled {
		go cleanupLoop(cleanupCtx, cfg.EPUB.OutputDir, cfg.Cleanup.RetentionHours, cfg.Cleanup.IntervalHours)
	}

	go func() {
		lis, err := net.Listen("tcp", ":"+cfg.Server.GRPCPort)
		if err != nil {
			log.Fatalf("failed to listen on grpc port: %v", err)
		}
		log.Printf("gRPC server listening on :%s", cfg.Server.GRPCPort)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve grpc: %v", err)
		}
	}()

	httpServer := &http.Server{
		Addr:              ":" + cfg.Server.HTTPPort,
		Handler:           metrics.Middleware(cfg.Metrics.Path, http.HandlerFunc(webHandler.ServeHTTP)),
		ReadTimeout:       60 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Printf("HTTP server listening on :%s", cfg.Server.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to serve http: %v", err)
		}
	}()

	fmt.Printf("Document Processing Service ready:\n")
	fmt.Printf("  gRPC: :%s\n", cfg.Server.GRPCPort)
	fmt.Printf("  HTTP:  http://localhost:%s\n", cfg.Server.HTTPPort)
	fmt.Printf("  OCR Languages: %v\n", cfg.OCR.Languages)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down servers...")

	grpcDone := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcDone)
	}()
	select {
	case <-grpcDone:
	case <-time.After(15 * time.Second):
		log.Println("gRPC graceful stop timed out, forcing stop")
		grpcServer.Stop()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}
}

// cleanupLoop deletes EPUBs older than retentionHours from
// dir, running every intervalHours. Exits on ctx cancel.
func cleanupLoop(ctx context.Context, dir string, retentionHours, intervalHours int) {
	if retentionHours <= 0 {
		retentionHours = 24
	}
	if intervalHours <= 0 {
		intervalHours = 1
	}
	retention := time.Duration(retentionHours) * time.Hour
	interval := time.Duration(intervalHours) * time.Hour
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("cleanup: failed to read dir %s: %v", dir, err)
			continue
		}
		cutoff := time.Now().Add(-retention)
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".epub" {
				continue
			}
			info, err := e.Info()
			if err != nil {
				log.Printf("cleanup: failed to stat %s: %v", e.Name(), err)
				continue
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if err := os.Remove(path); err != nil {
				log.Printf("cleanup: failed to remove %s: %v", e.Name(), err)
			} else {
				log.Printf("cleanup: removed %s", e.Name())
			}
		}
	}
}
