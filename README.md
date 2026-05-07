# epublic8

A web-based PDF to EPUB converter with OCR support.

## Requirements

- Go 1.25+
- `poppler-utils` (pdftotext, pdftoppm, pdfimages)
- `tesseract-ocr` (Linux) or macOS with Vision framework

## Run

```bash
go build -o bin/epublic8 ./cmd/server
./bin/epublic8
```

Open http://localhost:8080, drag and drop a PDF, download the EPUB.

### Docker

```bash
docker build -t epublic8 .
docker run -p 8080:8080 --cpus 2 --memory 2g \
  -e GOMAXPROCS=2 -e OMP_NUM_THREADS=1 epublic8
```

## Configuration

Environment variables or `-config config.yaml`. Env vars take precedence.

| Variable | Default | Description |
|----------|---------|-------------|
| `HTTP_PORT` | `8080` | Server port |
| `GOMAXPROCS` | all CPUs | Parallel OCR workers. Match to container CPU limit. |
| `OMP_NUM_THREADS` | all CPUs | Threads per Tesseract process. Set to `1` in containers. |
| `OCR_LANGUAGES` | `srp_latn+hrv, srp_latn, eng` | OCR language codes (comma-separated) |
| `EPUB_CHAPTER_WORDS` | `1500` | Word count per chapter when no headings detected |
| `OUTPUT_DIR` | *(temp dir)* | Persistent EPUB output directory |
| `EPUB_CLEANUP_ENABLED` | `false` | Auto-delete old EPUBs |
| `EPUB_RETENTION_HOURS` | `24` | Hours before cleanup |
| `BASIC_AUTH` | - | `username:password` or bcrypt hash |

## Supported Formats

**Input:** PDF, plain text, Markdown, HTML

**Output:** EPUB 2.0

### How it works

1. Upload via web UI
2. Text extraction via `pdftotext`. Falls back to OCR if garbled CE-encoded fonts detected.
3. On macOS: Vision OCR with figure detection. On Linux: Tesseract (no figures).
4. Chapter splitting on headings (`Glava`, `Poglavlje`, `Chapter`, Roman numerals) or word count.
5. EPUB generated and served for download.

## Development

```bash
make test    # run tests
make lint    # golangci-lint
make all     # lint + test + build
```

## License

MIT
