package main

import (
	"context"
	"fmt"
	"log"
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
)

const pidFile = "document-service.pid"

func writePID() {
	// Remove a stale PID file from a previous crash.
	if data, err := os.ReadFile(pidFile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if proc, err := os.FindProcess(pid); err == nil {
				// Signal 0 checks existence without sending a real signal.
				if proc.Signal(syscall.Signal(0)) != nil {
					log.Printf("removing stale PID file (pid %d no longer running)", pid)
					if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
						log.Printf("warning: could not remove stale PID file: %v", err)
					}
				}
			}
		}
	}
	// Write to a temp file in the same directory, then rename atomically so
	// another process cannot observe a partially-written PID file.
	tmp, err := os.CreateTemp(".", ".pid-tmp-*")
	if err != nil {
		log.Printf("warning: could not create temp PID file: %v", err)
		return
	}
	if _, err := fmt.Fprintf(tmp, "%d", os.Getpid()); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		log.Printf("warning: could not write PID file: %v", err)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		log.Printf("warning: could not close temp PID file: %v", err)
		return
	}
	if err := os.Rename(tmp.Name(), pidFile); err != nil {
		os.Remove(tmp.Name())
		log.Printf("warning: could not rename PID file: %v", err)
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

	model.LogToolAvailability(log.Printf)

	docHandler := handler.NewDocumentHandler(cfg.OCR.Concurrency, cfg.OCR.Languages)
	defer docHandler.Close()

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

	httpServer := &http.Server{
		Addr:              ":" + cfg.Server.HTTPPort,
		Handler:           metrics.Middleware(cfg.Metrics.Path, http.HandlerFunc(webHandler.ServeHTTP)),
		ReadTimeout:       60 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      15 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Printf("HTTP server listening on :%s", cfg.Server.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to serve http: %v", err)
		}
	}()

	log.Printf("Document Processing Service ready: http://localhost:%s", cfg.Server.HTTPPort)
	log.Printf("  OCR Languages: %v", cfg.OCR.Languages)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
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
