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
	"syscall"
	"time"

	"epublic8/internal/handler"
	"epublic8/internal/model"
	"google.golang.org/grpc"
)

const pidFile = "document-service.pid"

func writePID() {
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		log.Printf("warning: could not write PID file: %v", err)
	}
}

func removePID() {
	os.Remove(pidFile)
}

func main() {
	writePID()
	defer removePID()

	grpcPort := getEnv("GRPC_PORT", "50051")
	httpPort := getEnv("HTTP_PORT", "8080")
	outputDir := getEnv("OUTPUT_DIR", "")

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(100 * 1024 * 1024),
	)

	model.LogToolAvailability(log.Printf)

	docHandler := handler.NewDocumentHandler()
	defer docHandler.Close()

	docHandler.Register(grpcServer)

	webHandler, err := handler.NewWebHandler(docHandler, outputDir)
	if err != nil {
		log.Fatalf("failed to initialize web handler: %v", err)
	}
	defer webHandler.Close()

	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	if outputDir != "" {
		go cleanupLoop(cleanupCtx, outputDir)
	}

	go func() {
		lis, err := net.Listen("tcp", ":"+grpcPort)
		if err != nil {
			log.Fatalf("failed to listen on grpc port: %v", err)
		}
		log.Printf("gRPC server listening on :%s", grpcPort)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve grpc: %v", err)
		}
	}()

	httpServer := &http.Server{
		Addr:              ":" + httpPort,
		Handler:           http.HandlerFunc(webHandler.ServeHTTP),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("HTTP server listening on :%s", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to serve http: %v", err)
		}
	}()

	fmt.Printf("Document Processing Service ready:\n")
	fmt.Printf("  gRPC: :%s\n", grpcPort)
	fmt.Printf("  HTTP:  http://localhost:%s\n", httpPort)

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

// cleanupLoop deletes EPUBs older than EPUB_RETENTION_HOURS (default 24) from
// dir, running every EPUB_CLEANUP_INTERVAL_HOURS (default 1). Exits on ctx cancel.
func cleanupLoop(ctx context.Context, dir string) {
	retention := time.Duration(getEnvInt("EPUB_RETENTION_HOURS", 24)) * time.Hour
	interval := time.Duration(getEnvInt("EPUB_CLEANUP_INTERVAL_HOURS", 1)) * time.Hour
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("cleanup: failed to read dir %s: %v", dir, err)
			continue
		}
		cutoff := time.Now().Add(-retention)
		for _, e := range entries {
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

func getEnvInt(key string, defaultVal int) int {
	if s := os.Getenv(key); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
		log.Printf("warning: invalid value for %s, using default %d", key, defaultVal)
	}
	return defaultVal
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}
