# epublic8

[![Go Version](https://img.shields.io/github/go-mod/go-version/sreckoskocilic/epublic8?label=Go)](https://github.com/sreckoskocilic/epublic8)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Report Card](https://goreportcard.com/badge/github.com/sreckoskocilic/epublic8)](https://goreportcard.com/report/github.com/sreckoskocilic/epublic8)

A Go-based gRPC/HTTP service for converting documents to EPUB format with OCR support.

## Features

- **OCR** - Extract text from PDFs with garbled/CE-encoded fonts using Vision (macOS) or Tesseract
- **Figure detection** - Automatically crops embedded figures from OCR'd pages and inserts them inline at the correct position in the EPUB, scaled proportionally to their PDF page width
- **EPUB Generation** - Convert documents to EPUB with automatic chapter detection
- **gRPC API** - High-performance gRPC interface for programmatic access
- **Web UI** - Drag-and-drop browser interface with real-time conversion logs
- **Kubernetes Ready** - Production-ready K8s manifests with HPA
- **Prometheus Metrics** - Built-in metrics endpoint for monitoring
- **OpenTelemetry Tracing** - Optional distributed tracing support
- **Security** - Basic authentication and allowed hosts filtering

## Architecture

The service exposes two interfaces on the same process: a gRPC server on `:50051` for programmatic access and an HTTP server on `:8080` for the web UI. Both interfaces feed into the same document processor, which extracts text from the uploaded file and passes it to the EPUB generator. The generated EPUB is written to disk and served back via a download URL.

OCR parallelism is bounded by a package-level semaphore sized to `GOMAXPROCS`, so one busy conversion can use all allocated cores while multiple concurrent conversions share them fairly.

On macOS the service compiles and uses the Vision framework (`ocr/vision_ocr.swift`) for OCR, which returns per-word bounding boxes in addition to text. Those bounding boxes drive figure detection: vertical gaps followed by a "Fig. N" caption are cropped from the page image and embedded in the EPUB at the paragraph where the caption appears. Figure images carry a width fraction (crop width ÷ page width) so they render at their original proportional size rather than being stretched to the full column width. On Linux, Vision is unavailable and Tesseract is used; figure detection is skipped.

## Quick Start

### Local Development

```bash
# Clone and navigate to project
cd epublic8

# Install dependencies
go mod tidy

# Generate protobuf code (only needed if you modify document.proto)
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  pb/document.proto

# Build
go build -o bin/epublic8 ./cmd/server

# Run (uses all CPUs for parallel OCR by default)
./bin/epublic8

# Run with limited parallelism
GOMAXPROCS=2 OMP_NUM_THREADS=1 ./bin/epublic8

# Stop
./stop

# Open browser
# http://localhost:8080
```

### Docker

```bash
# Build image
docker build -t document-service:latest .

# Run container
# OMP_NUM_THREADS=1 prevents each Tesseract process from spawning extra threads;
# without it, GOMAXPROCS=2 workers each try to claim all available CPU threads.
# GOMAXPROCS should match --cpus so the OCR semaphore is sized correctly.
docker run -p 8080:8080 -p 50051:50051 \
  --cpus 2 --memory 2g \
  -e GOMAXPROCS=2 \
  -e OMP_NUM_THREADS=1 \
  document-service:latest
```

### Kubernetes (minikube)

The manifest uses `imagePullPolicy: Never`, so the image must exist inside
minikube's Docker daemon before deploying. Build it there directly:

```bash
# Point your shell at minikube's Docker daemon, build, then restore
eval $(minikube docker-env)
docker build -t document-service:latest .
eval $(minikube docker-env -u)
```

Deploy and verify:

```bash
kubectl apply -f deploy/k8s.yaml

kubectl get pods -l app=document-service   # wait for 1/1 Running
kubectl get svc document-service-lb        # EXTERNAL-IP will be <pending> until tunnel
```

The manifest creates a `LoadBalancer` service. On macOS with the Docker driver
the node IP is not reachable from the host, so run `minikube tunnel` in a
dedicated terminal to assign `127.0.0.1` as the external IP:

```bash
minikube tunnel   # keep this running; may prompt for sudo
```

The service is then available at http://localhost:8080 and gRPC at `localhost:50051`.

To rebuild and redeploy after code changes:

```bash
eval $(minikube docker-env)
docker build -t document-service:latest .
eval $(minikube docker-env -u)
kubectl rollout restart deployment/document-service
```

## API Reference

### gRPC

```protobuf
service DocumentService {
  rpc ProcessDocument(DocumentRequest) returns (DocumentResponse);
  rpc StreamProcessDocument(stream DocumentChunk) returns (stream DocumentChunkResponse);
  rpc ExtractEntities(EntityRequest) returns (EntityResponse);
  rpc Health(HealthRequest) returns (HealthResponse);
}
```

`ExtractEntities` uses regex-based pattern matching to identify PERSON, LOCATION, ORGANIZATION, DATE, EMAIL, and PHONE entities. It accepts a 1 MB text limit and returns deduplicated results with confidence scores.

`Health` returns the current service status and number of active requests for readiness probes.

### HTTP

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/` | Upload page |
| POST | `/api/upload` | Upload and convert document (streams SSE log events) |
| GET | `/download?file=filename` | Download generated EPUB |
| GET | `/metrics` | Prometheus metrics endpoint |

#### `POST /api/upload`

Upload form fields:
- `file` — the document to convert (max 200 MB)
- `smart_ocr` — `true` (default) to run `pdffonts` before extraction and skip straight to OCR when Custom-encoded fonts are detected

Response: `text/event-stream`. Each SSE event is a JSON object on the `data:` line:

```json
{"type":"log","message":"..."}
```
Progress line, streamed in real time.

```json
{"type":"done","download_url":"/download?file=...","filename":"...","chapters":4,"chars":85000,"epub_kb":142.3,"processing_ms":3210}
```
Conversion complete.

```json
{"type":"error","message":"..."}
```
Conversion failed.

## Configuration

The service supports configuration via YAML config file, environment variables, or command-line flags. Environment variables take precedence over config file values.

### Config File

```yaml
# config.yaml example
server:
  grpcPort: "50051"
  httpPort: "8080"

ocr:
  concurrency: 2
  languages:
    - srp_latn+hrv
    - srp_latn
    - eng

epub:
  chapterWords: 1500
  outputDir: "/tmp/epubs"

cleanup:
  enabled: true
  retentionHours: 24
  intervalHours: 1

security:
  basicAuth: "admin:secret"  # or bcrypt hash
  allowedHosts:
    - example.com

tracing:
  enabled: false
  serviceName: "epublic8"
  consoleExporter: true

metrics:
  enabled: true
  path: "/metrics"
```

Use `-config` flag or `CONFIG_PATH` environment variable to load config file:

```bash
./bin/epublic8 -config config.yaml
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CONFIG_PATH` | - | Path to YAML config file |
| `GRPC_PORT` | `50051` | gRPC server port |
| `HTTP_PORT` | `8080` | HTTP server port |
| `OUTPUT_DIR` | *(temp dir)* | Directory for generated EPUBs. If unset, a temp dir is created and removed on exit. If set, files persist and are cleaned up after 24 hours by an in-process loop. |
| `GOMAXPROCS` | all CPUs | Go scheduler parallelism. Set to match container CPU limit. |
| `OMP_NUM_THREADS` | all CPUs | Threads per Tesseract process (OpenMP). Set to `1` to prevent each concurrent Tesseract worker from spawning extra threads and saturating the CPU limit. |
| `OCR_CONCURRENCY` | `GOMAXPROCS` | Max concurrent OCR page workers. Defaults to the Go scheduler parallelism. |
| `OCR_LANGUAGES` | `srp_latn+hrv, srp_latn, eng` | OCR language codes (comma-separated) |
| `EPUB_CHAPTER_WORDS` | `1500` | Word count target per chapter when no headings are detected. |
| `EPUB_CLEANUP_ENABLED` | `false` | Enable automatic EPUB cleanup |
| `EPUB_RETENTION_HOURS` | `24` | Hours before generated EPUBs are deleted by the cleanup loop |
| `EPUB_CLEANUP_INTERVAL_HOURS` | `1` | How often the cleanup loop runs (hours) |
| `BASIC_AUTH` | - | Basic auth credentials (format: `username:password` or bcrypt hash) |
| `ALLOWED_HOSTS` | - | Comma-separated list of allowed Host headers |
| `TRACING_ENABLED` | `false` | Enable OpenTelemetry tracing |
| `TRACING_SERVICE_NAME` | `epublic8` | Service name for tracing |
| `TRACING_CONSOLE_EXPORTER` | `true` | Enable console tracing output |
| `METRICS_ENABLED` | `true` | Enable Prometheus metrics |
| `METRICS_PATH` | `/metrics` | Metrics endpoint path |

## Supported Formats

Input: PDF, plain text (`.txt`), Markdown (`.md`), HTML (`.html`)

Output: EPUB 2.0

### PDF handling

`pdftotext` is used for text extraction. If garbled Central European encoding is detected (common in Bosnian/Croatian/Serbian PDFs where font maps CE characters to ASCII positions), the service falls back to OCR at 300 DPI. On macOS, Apple Vision OCR is used and returns per-word bounding boxes used for figure detection. On Linux, Tesseract is the fallback. Language priority for both engines: `srp_latn+hrv` → `srp_latn` → `eng`.

For native-text PDFs (no OCR needed), `pdfimages` extracts any embedded raster images directly.

Footnotes embedded at the bottom of pages (separated by form feeds) are detected and stripped from the extracted text.

### Figure detection (macOS / Vision OCR only)

Two strategies are used to locate figures on each OCR'd page:

1. **Gap-based** — a vertical gap larger than 4% of page height followed by a "Fig. N" caption. The figure region spans full page width.
2. **Side-by-side** — all text above a "Fig. N" caption sits in a right column (x > 40% of page width), indicating the figure occupies the left column.

Cropped figure images are embedded in the EPUB at the paragraph where the caption appears. Each image carries its original width as a fraction of the page width so it renders at proportional size (e.g. a half-page figure renders at 50% column width).

### Chapter detection

Chapters are split on headings matching keywords like `Glava`, `Poglavlje`, `Chapter`, standalone Roman numerals, or known section names (`UVOD`, `PREDGOVOR`, `POGOVOR`, etc.). If no headings are found, the text is split every 1500 words.

## Metrics

The service exposes Prometheus metrics at `/metrics` (configurable via `METRICS_PATH`):

| Metric | Type | Description |
|--------|------|-------------|
| `http_requests_total` | Counter | Total HTTP requests by method, path, status |
| `http_request_duration_seconds` | Histogram | HTTP request duration |
| `documents_processed_total` | Counter | Documents processed (success/error) |
| `documents_in_progress` | Gauge | Documents currently being processed |
| `ocr_calls_total` | Counter | Total OCR API calls |
| `ocr_processing_duration_seconds` | Histogram | OCR processing duration |
| `http_active_requests` | Gauge | Currently active HTTP requests |

## Kubernetes

Resource limits are derived from actual workload measurements:

| | Value | Reasoning |
|--|-------|-----------|
| CPU request | 1000m | GOMAXPROCS=2 OCR workers + Go scheduler at steady state |
| CPU limit | 2000m | 2 Tesseract processes × 1 OMP thread = 2 CPUs peak |
| Memory request | 768Mi | ~250 MB heap per in-flight request × 2 concurrent |
| Memory limit | 2Gi | Headroom for 3 concurrent max-size (200 MB) requests |
| `/tmp` sizeLimit | 1Gi | 200 MB upload + ~50 MB pdftoppm PNGs per request × ~4 concurrent |

**Why `OMP_NUM_THREADS=1` matters**: Tesseract defaults to using all available CPU threads via OpenMP. Without this setting, `GOMAXPROCS=2` concurrent Tesseract workers each try to claim all CPUs, resulting in severe CFS throttling and 2–3× slower OCR.

**Why `/tmp` is disk-backed**: On cgroupsv2 (all modern k8s nodes), `medium: Memory` tmpfs usage counts against the container memory cgroup. A memory-backed `/tmp` with a 2 Gi sizeLimit and a 2 Gi container memory limit leaves almost nothing for the Go heap, causing OOM kills under realistic load. The disk-backed emptyDir keeps temp file I/O off the memory budget entirely.

Other K8s features:
- **Horizontal Pod Autoscaler** — scales 1–20 replicas; CPU target 70% of 2000m (~1400m, triggers when a second OCR request arrives while the first is active)
- **Liveness/Readiness Probes** — HTTP GET `/` on port 8080; `terminationGracePeriodSeconds: 40` gives the 15 s gRPC + 10 s HTTP shutdown sequence room to complete
- **In-process cleanup** — EPUBs older than 24 hours are removed automatically by a background goroutine

## Project Structure

```
.
├── cmd/
│   └── server/
│       └── main.go           # Entry point, server startup, cleanup loop
├── ocr/
│   └── vision_ocr.swift      # macOS Vision OCR binary (compiled at startup)
├── pb/
│   └── document.proto        # Protocol Buffer definitions
├── internal/
│   ├── config/
│   │   └── config.go         # YAML config, env vars, CLI flags
│   ├── model/
│   │   ├── document.go       # PDF extraction, OCR, footnote processing, EPUB generation
│   │   └── figures.go        # Vision OCR bounding-box figure detection and cropping
│   ├── handler/
│   │   ├── handler.go        # gRPC handlers
│   │   ├── web.go            # HTTP handlers, SSE streaming, EPUB zip writer, web UI
│   │   └── middleware/
│   │       └── auth.go       # Basic auth middleware
│   ├── metrics/
│   │   └── metrics.go        # Prometheus metrics
│   ├── tracing/
│   │   └── tracing.go        # OpenTelemetry tracing
│   └── errors/
│       └── errors.go        # Error types and handling
├── deploy/
│   └── k8s.yaml              # Kubernetes manifests (Deployment, Services, HPA, ConfigMap)
├── Dockerfile
├── Makefile
├── .golangci.yml
├── go.mod
├── go.sum
├── stop                      # Stop script (sends SIGTERM via PID file)
└── README.md
```

## Tech Stack

- **Go 1.26** - Programming language
- **gRPC / Protocol Buffers** - RPC interface
- **pdftotext / pdftoppm / pdfimages** - PDF text extraction and rendering (poppler-utils)
- **Apple Vision** - Primary OCR engine on macOS (provides bounding boxes for figure detection)
- **Tesseract OCR** - OCR fallback on Linux / when Vision is unavailable
- **Prometheus** - Metrics collection
- **OpenTelemetry** - Distributed tracing
- **Kubernetes** - Container orchestration

## Development

```bash
# Run tests
make test

# Lint code
make lint

# Build binary
make build

# Full build (lint + test + build)
make all
```

## License

MIT
