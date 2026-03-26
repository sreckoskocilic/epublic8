FROM golang:1.26-bookworm AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X epublic8/internal/handler.Version=${VERSION} -X epublic8/internal/handler.Commit=${COMMIT} -X epublic8/internal/handler.BuildTime=${BUILD_TIME}" \
    -o document-service ./cmd/server

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    poppler-utils \
    tesseract-ocr \
    tesseract-ocr-srp-latn \
    tesseract-ocr-hrv \
    && rm -rf /var/lib/apt/lists/* \
    && adduser --disabled-password --gecos "" appuser

COPY --from=builder /app/document-service /usr/local/bin/document-service

USER appuser

EXPOSE 8080 50051

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -sf http://localhost:8080/health/live > /dev/null || exit 1

ENTRYPOINT ["document-service"]
