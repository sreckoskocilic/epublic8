package handler

import (
	"archive/zip"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"epublic8/internal/model"
	"epublic8/pb"
)

type WebHandler struct {
	documentHandler *DocumentHandler
	epubGenerator   *model.EPUBGenerator
	uploadDir       string
	persistentDir   bool
	templates       *template.Template
	pb.UnimplementedDocumentServiceServer
}

var uploadPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Document to EPUB Converter</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            display: flex;
            align-items: flex-start;
            justify-content: center;
            padding: 40px 20px;
        }
        .page {
            display: flex;
            gap: 24px;
            width: 100%;
            max-width: 1100px;
            align-items: flex-start;
        }
        .left {
            display: flex;
            flex-direction: column;
            gap: 20px;
            flex: 1;
            min-width: 0;
        }
        .right {
            width: 300px;
            flex-shrink: 0;
            display: flex;
            flex-direction: column;
            gap: 16px;
        }
        .card {
            background: white;
            border-radius: 16px;
            box-shadow: 0 20px 60px rgba(0,0,0,0.3);
            padding: 28px;
        }
        h1 { color: #1a1a1a; margin-bottom: 4px; font-size: 22px; font-weight: 700; }
        .subtitle { color: #888; margin-bottom: 20px; font-size: 13px; }

        /* ── Drop zone (empty state) ── */
        input[type="file"] { display: none; }
        .drop-zone {
            border: 1.5px dashed #d0d0d0;
            border-radius: 10px;
            padding: 18px 16px;
            display: flex;
            align-items: center;
            gap: 12px;
            cursor: pointer;
            transition: border-color 0.2s, background 0.2s;
            margin-bottom: 14px;
            position: relative;
        }
        .drop-zone:hover, .drop-zone.dragover {
            border-color: #667eea;
            background: #f6f7ff;
        }
        .drop-zone-icon {
            width: 36px;
            height: 36px;
            border-radius: 8px;
            background: #f0f0f0;
            display: flex;
            align-items: center;
            justify-content: center;
            flex-shrink: 0;
            color: #aaa;
        }
        .drop-zone-icon svg { width: 18px; height: 18px; }
        .drop-zone-text { flex: 1; min-width: 0; }
        .drop-zone-text strong { font-size: 13px; color: #333; font-weight: 600; display: block; }
        .drop-zone-text span { font-size: 12px; color: #aaa; }

        /* ── Selected file state ── */
        .drop-zone.has-file {
            border-style: solid;
            border-color: #667eea;
            background: #f6f7ff;
            cursor: default;
        }
        .drop-zone.has-file .drop-zone-icon {
            background: #667eea;
            color: white;
        }
        .drop-zone.has-file .drop-zone-text strong { color: #1a1a1a; }
        .drop-zone.has-file .drop-zone-text span { color: #888; }
        .clear-btn {
            display: none;
            background: none;
            border: none;
            color: #bbb;
            cursor: pointer;
            padding: 4px;
            border-radius: 4px;
            width: auto;
            font-size: 0;
            flex-shrink: 0;
            transition: color 0.15s;
        }
        .clear-btn:hover { color: #e74c3c; transform: none; box-shadow: none; }
        .clear-btn svg { width: 16px; height: 16px; }
        .drop-zone.has-file .clear-btn { display: flex; align-items: center; }
        .drop-zone.has-file:hover { background: #f6f7ff; border-color: #667eea; }

        /* ── Advanced toggle ── */
        .advanced-toggle {
            display: flex;
            align-items: center;
            gap: 6px;
            font-size: 12px;
            color: #aaa;
            cursor: pointer;
            margin-bottom: 14px;
            user-select: none;
            width: fit-content;
        }
        .advanced-toggle:hover { color: #667eea; }
        .advanced-toggle svg { width: 12px; height: 12px; transition: transform 0.2s; }
        .advanced-toggle.open svg { transform: rotate(90deg); }
        .advanced-body { display: none; margin-bottom: 14px; }
        .advanced-body.open { display: block; }
        .option-row {
            display: flex;
            align-items: flex-start;
            gap: 8px;
            font-size: 13px;
            color: #555;
            cursor: pointer;
            line-height: 1.4;
        }
        .option-row input[type="checkbox"] { margin-top: 2px; flex-shrink: 0; cursor: pointer; accent-color: #667eea; }
        .option-hint { color: #bbb; font-size: 12px; }

        /* ── Buttons ── */
        button {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            border: none;
            padding: 12px 24px;
            border-radius: 8px;
            font-size: 14px;
            font-weight: 600;
            cursor: pointer;
            width: 100%;
            transition: transform 0.15s, box-shadow 0.15s, opacity 0.15s;
            display: flex;
            align-items: center;
            justify-content: center;
            gap: 8px;
        }
        button:hover:not(:disabled) { transform: translateY(-1px); box-shadow: 0 6px 16px rgba(102,126,234,0.4); }
        button:disabled { opacity: 0.5; cursor: not-allowed; }
        .cancel-btn {
            display: none;
            margin-top: 8px;
            background: none;
            border: 1px solid #e0e0e0;
            color: #999;
            font-size: 13px;
            font-weight: 500;
            padding: 8px;
            border-radius: 6px;
        }
        .cancel-btn:hover { border-color: #e74c3c; color: #e74c3c; transform: none; box-shadow: none; }
        .loading .cancel-btn { display: flex; }
        .loading button[type="submit"] { pointer-events: none; }

        /* ── Spinner ── */
        .spinner {
            display: none;
            width: 16px; height: 16px;
            border: 2.5px solid rgba(255,255,255,0.35);
            border-top-color: white;
            border-radius: 50%;
            animation: spin 0.7s linear infinite;
            flex-shrink: 0;
        }
        @keyframes spin { to { transform: rotate(360deg); } }
        .loading .spinner { display: block; }
        .timer { color: #bbb; font-size: 12px; text-align: center; margin-top: 8px; display: none; }
        .loading .timer { display: block; }

        /* ── Result ── */
        .result { padding: 12px 14px; border-radius: 8px; display: none; margin-top: 12px; font-size: 13px; }
        .result.show { display: block; }
        .result.error { background: #fff0f0; border: 1px solid #fcc; color: #c0392b; }

        /* ── Log box ── */
        .log-box { border: 1px solid #2d2d2d; border-radius: 10px; overflow: hidden; display: none; }
        .log-box.visible { display: block; }
        .log-box-header {
            background: #2d2d2d; color: #666;
            font-family: 'Courier New', monospace;
            font-size: 10px; font-weight: 700;
            letter-spacing: 0.1em; text-transform: uppercase;
            padding: 5px 12px; user-select: none;
        }
        .logs {
            background: #1a1a1a; color: #d4d4d4;
            font-family: 'Courier New', monospace;
            font-size: 12px; line-height: 1.6;
            min-height: 48px; max-height: 280px;
            overflow-y: auto;
            padding: 8px 12px;
            scrollbar-width: thin; scrollbar-color: #444 #1a1a1a;
        }
        .logs::-webkit-scrollbar { width: 5px; }
        .logs::-webkit-scrollbar-track { background: #1a1a1a; }
        .logs::-webkit-scrollbar-thumb { background: #3a3a3a; border-radius: 3px; }
        .logs div { padding: 1px 0; white-space: pre-wrap; word-break: break-all; }
        .logs div::before { content: '▸ '; color: #444; }

        /* ── Summary panel ── */
        .summary-card {
            background: white; border-radius: 16px;
            box-shadow: 0 20px 60px rgba(0,0,0,0.3);
            overflow: hidden; display: none;
        }
        .summary-card.visible { display: block; }
        .summary-header {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white; padding: 14px 20px;
            font-size: 13px; font-weight: 600; letter-spacing: 0.03em;
        }
        .summary-body { padding: 16px 20px; }
        .stat-row {
            display: flex; justify-content: space-between; align-items: baseline;
            padding: 8px 0; border-bottom: 1px solid #f2f2f2; font-size: 13px;
        }
        .stat-row:last-child { border-bottom: none; }
        .stat-label { color: #999; }
        .stat-value { color: #222; font-weight: 600; text-align: right; max-width: 170px; word-break: break-all; }
        .download-btn {
            display: block; margin-top: 14px;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white; text-align: center;
            padding: 10px; border-radius: 8px;
            font-weight: 600; font-size: 13px;
            text-decoration: none; transition: opacity 0.2s;
        }
        .download-btn:hover { opacity: 0.88; }

        @media (max-width: 680px) {
            .page { flex-direction: column; }
            .right { width: 100%; }
        }
    </style>
</head>
<body>
    <div class="page">
        <div class="left">
            <div class="card">
                <h1>Document to EPUB</h1>
                <p class="subtitle">PDF, TXT, MD, HTML &middot; max 200 MB</p>

                <form id="uploadForm" enctype="multipart/form-data">
                    <!-- Drop zone -->
                    <label class="drop-zone" id="dropZone">
                        <div class="drop-zone-icon" id="dzIcon">
                            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                                <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
                                <polyline points="14 2 14 8 20 8"/>
                            </svg>
                        </div>
                        <div class="drop-zone-text">
                            <strong id="dzTitle">Click to choose or drop a file</strong>
                            <span id="dzSub">PDF, TXT, Markdown, HTML</span>
                        </div>
                        <button type="button" class="clear-btn" id="clearBtn" title="Remove file">
                            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
                                <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
                            </svg>
                        </button>
                        <input type="file" id="fileInput" name="file" accept=".pdf,.txt,.md,.html">
                    </label>

                    <!-- Advanced options -->
                    <div class="advanced-toggle" id="advancedToggle">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                            <polyline points="9 18 15 12 9 6"/>
                        </svg>
                        Advanced
                    </div>
                    <div class="advanced-body" id="advancedBody">
                        <label class="option-row">
                            <input type="checkbox" id="smartOcr" name="smart_ocr" value="true" checked>
                            <span>Smart OCR <span class="option-hint">&mdash; detect garbled fonts and scan-based PDFs; re-render with OCR for cleaner text</span></span>
                        </label>
                        <label class="option-row" style="margin-top:8px">
                            <input type="checkbox" id="forceOcr" name="force_ocr" value="true">
                            <span>Force OCR <span class="option-hint">&mdash; always re-render pages, ignoring any embedded text (useful when Smart OCR isn&apos;t enough)</span></span>
                        </label>
                        <label class="option-row" style="margin-top:8px">
                            <input type="checkbox" id="stripHeaders" name="strip_headers" value="true" checked>
                            <span>Strip running headers <span class="option-hint">&mdash; remove repeated book/chapter titles printed at the top of each page</span></span>
                        </label>
                        <label class="option-row" style="margin-top:8px">
                            <input type="checkbox" id="stripFootnotes" name="strip_footnotes" value="true" checked>
                            <span>Strip footnotes <span class="option-hint">&mdash; remove footnote sections at the bottom of pages</span></span>
                        </label>
                        <label class="option-row" style="margin-top:8px">
                            <input type="checkbox" id="textOnly" name="text_only" value="true">
                            <span>Text only <span class="option-hint">&mdash; skip embedded image extraction; useful for scanned books with no real figures</span></span>
                        </label>
                    </div>

                    <button type="submit" id="submitBtn" disabled>
                        <span id="submitLabel">Convert to EPUB</span>
                        <span class="spinner"></span>
                    </button>
                    <button type="button" class="cancel-btn" id="cancelBtn">Cancel</button>
                    <div class="timer" id="timer"></div>
                </form>

                <div class="result" id="result"></div>
            </div>

            <div class="log-box" id="logBox">
                <div class="log-box-header">conversion log</div>
                <div class="logs" id="logs" aria-live="polite"></div>
            </div>
        </div>

        <div class="right">
            <div class="summary-card" id="summaryCard">
                <div class="summary-header">Conversion Summary</div>
                <div class="summary-body" id="summaryBody"></div>
            </div>
        </div>
    </div>

    <script>
        const dropZone    = document.getElementById('dropZone');
        const fileInput   = document.getElementById('fileInput');
        const dzTitle     = document.getElementById('dzTitle');
        const dzSub       = document.getElementById('dzSub');
        const dzIcon      = document.getElementById('dzIcon');
        const clearBtn    = document.getElementById('clearBtn');
        const uploadForm  = document.getElementById('uploadForm');
        const result      = document.getElementById('result');
        const logBox      = document.getElementById('logBox');
        const logsEl      = document.getElementById('logs');
        const summaryCard = document.getElementById('summaryCard');
        const summaryBody = document.getElementById('summaryBody');
        const baseTitle   = document.title;
        const smartOcrEl  = document.getElementById('smartOcr');
        const submitBtn   = document.getElementById('submitBtn');
        const submitLabel = document.getElementById('submitLabel');
        const cancelBtn   = document.getElementById('cancelBtn');
        const timerEl     = document.getElementById('timer');
        const advToggle   = document.getElementById('advancedToggle');
        const advBody     = document.getElementById('advancedBody');

        let abortController = null;
        let timerInterval   = null;
        let dragDepth       = 0;

        // ── Advanced toggle ──
        advToggle.addEventListener('click', () => {
            advToggle.classList.toggle('open');
            advBody.classList.toggle('open');
        });

        // ── Drag & drop ──
        document.addEventListener('dragover',  (e) => e.preventDefault());
        document.addEventListener('dragenter', (e) => { e.preventDefault(); if (++dragDepth === 1) dropZone.classList.add('dragover'); });
        document.addEventListener('dragleave', ()  => { if (--dragDepth <= 0) { dragDepth = 0; dropZone.classList.remove('dragover'); } });
        document.addEventListener('drop', (e) => {
            e.preventDefault();
            dragDepth = 0;
            dropZone.classList.remove('dragover');
            const f = e.dataTransfer.files[0];
            if (f) setFile(f);
        });

        fileInput.addEventListener('change', () => {
            if (fileInput.files[0]) setFile(fileInput.files[0]);
        });

        clearBtn.addEventListener('click', (e) => {
            e.preventDefault();
            e.stopPropagation();
            clearFile();
        });

        cancelBtn.addEventListener('click', () => {
            if (abortController) abortController.abort();
        });

        function setFile(file) {
            // Transfer to the hidden input when coming from drag-and-drop.
            if (fileInput.files[0] !== file) {
                const dt = new DataTransfer();
                dt.items.add(file);
                fileInput.files = dt.files;
            }
            dzTitle.textContent = file.name;
            dzSub.textContent   = formatSize(file.size);
            dropZone.classList.add('has-file');
            submitBtn.disabled = false;
            result.classList.remove('show');
        }

        function clearFile() {
            fileInput.value = '';
            dzTitle.textContent = 'Click to choose or drop a file';
            dzSub.textContent   = 'PDF, TXT, Markdown, HTML';
            dropZone.classList.remove('has-file');
            submitBtn.disabled = true;
        }

        function formatSize(bytes) {
            if (bytes < 1024) return bytes + '\u00a0B';
            if (bytes < 1048576) return (bytes / 1024).toFixed(1) + '\u00a0KB';
            return (bytes / 1048576).toFixed(1) + '\u00a0MB';
        }

        function appendLog(message) {
            logBox.classList.add('visible');
            const div = document.createElement('div');
            div.textContent = message;
            logsEl.appendChild(div);
            logsEl.scrollTop = logsEl.scrollHeight;
        }

        function startTimer() {
            const t0 = Date.now();
            timerEl.textContent = '0s';
            timerInterval = setInterval(() => {
                timerEl.textContent = ((Date.now() - t0) / 1000).toFixed(0) + 's';
            }, 1000);
        }

        function stopTimer() {
            clearInterval(timerInterval);
            timerInterval = null;
            timerEl.textContent = '';
        }

        uploadForm.addEventListener('submit', async (e) => {
            e.preventDefault();
            if (!fileInput.files.length) { showResult('error', 'Please select a file'); return; }
            if (fileInput.files[0].size > 200 * 1048576) { showResult('error', 'File exceeds 200\u00a0MB limit'); return; }

            const formData = new FormData();
            formData.append('file', fileInput.files[0]);
            formData.append('smart_ocr', smartOcrEl.checked ? 'true' : 'false');
            formData.append('force_ocr', document.getElementById('forceOcr').checked ? 'true' : 'false');
            formData.append('strip_headers', document.getElementById('stripHeaders').checked ? 'true' : 'false');
            formData.append('strip_footnotes', document.getElementById('stripFootnotes').checked ? 'true' : 'false');
            formData.append('text_only', document.getElementById('textOnly').checked ? 'true' : 'false');

            abortController = new AbortController();
            uploadForm.classList.add('loading');
            submitBtn.disabled = true;
            submitLabel.textContent = 'Converting\u2026';
            result.classList.remove('show');
            logsEl.innerHTML = '';
            logBox.classList.remove('visible');
            summaryBody.innerHTML = '';
            summaryCard.classList.add('visible');
            document.title = baseTitle;
            startTimer();

            try {
                const response = await fetch('/api/upload', {
                    method: 'POST',
                    body: formData,
                    signal: abortController.signal,
                });
                const reader  = response.body.getReader();
                const decoder = new TextDecoder();
                let buf = '', finished = false;

                while (true) {
                    const { done, value } = await reader.read();
                    if (done) break;
                    buf += decoder.decode(value, { stream: true });
                    const parts = buf.split('\n\n');
                    buf = parts.pop();
                    for (const part of parts) {
                        const line = part.replace(/^data: /, '').trim();
                        if (!line) continue;
                        let evt;
                        try { evt = JSON.parse(line); } catch { continue; }
                        if (evt.type === 'log') {
                            appendLog(evt.message);
                        } else if (evt.type === 'done') {
                            finished = true;
                            document.title = '\u2713 ' + (evt.filename || 'Done') + ' \u2014 ' + baseTitle;
                            showSummary(evt);
                            summaryCard.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
                        } else if (evt.type === 'error') {
                            finished = true;
                            document.title = '\u2717 Failed \u2014 ' + baseTitle;
                            showResult('error', evt.message || 'Conversion failed');
                            summaryBody.innerHTML = '';
                        }
                    }
                }
                if (!finished) {
                    document.title = '\u2717 Failed \u2014 ' + baseTitle;
                    showResult('error', 'Connection lost \u2014 conversion may have failed');
                    summaryBody.innerHTML = '';
                }
            } catch (err) {
                if (err.name === 'AbortError') {
                    document.title = baseTitle;
                    showResult('error', 'Conversion cancelled.');
                    summaryBody.innerHTML = '';
                } else {
                    document.title = '\u2717 Failed \u2014 ' + baseTitle;
                    showResult('error', 'Network error: ' + err.message);
                }
            } finally {
                abortController = null;
                stopTimer();
                uploadForm.classList.remove('loading');
                submitBtn.disabled = !fileInput.files.length;
                submitLabel.textContent = 'Convert to EPUB';
            }
        });

        function showResult(type, message) {
            result.className = 'result show ' + type;
            result.textContent = message;
        }

        function showSummary(evt) {
            const rows = [
                ['File',       evt.filename     || '\u2014'],
                ['Chapters',   evt.chapters  != null ? evt.chapters              : '\u2014'],
                ['Characters', evt.chars     != null ? evt.chars.toLocaleString(): '\u2014'],
                ['EPUB size',  evt.epub_kb   != null ? evt.epub_kb.toFixed(1) + '\u00a0KB' : '\u2014'],
                ['Processing', evt.processing_ms != null ? (evt.processing_ms / 1000).toFixed(1) + 's' : '\u2014'],
            ];
            const frag = document.createDocumentFragment();
            for (const [label, val] of rows) {
                const row = document.createElement('div');
                row.className = 'stat-row';
                const l = document.createElement('span'); l.className = 'stat-label'; l.textContent = label;
                const v = document.createElement('span'); v.className = 'stat-value'; v.textContent = val;
                row.appendChild(l); row.appendChild(v);
                frag.appendChild(row);
            }
            summaryBody.innerHTML = '';
            summaryBody.appendChild(frag);
            if (evt.download_url) {
                const a = document.createElement('a');
                a.className = 'download-btn';
                a.href = evt.download_url;
                a.download = evt.filename || '';
                a.textContent = 'Download EPUB';
                summaryBody.appendChild(a);
            }
        }
    </script>
</body>
</html>`

func NewWebHandler(docHandler *DocumentHandler, outputDir string) (*WebHandler, error) {
	persistent := outputDir != ""
	if !persistent {
		var err error
		outputDir, err = os.MkdirTemp("", "uploads")
		if err != nil {
			return nil, fmt.Errorf("failed to create upload dir: %v", err)
		}
	} else if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output dir %q: %v", outputDir, err)
	}

	tmpl, err := template.New("upload").Parse(uploadPageHTML)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %v", err)
	}

	epubGen := model.NewEPUBGenerator()

	return &WebHandler{
		documentHandler:                    docHandler,
		epubGenerator:                      epubGen,
		uploadDir:                          outputDir,
		templates:                          tmpl,
		persistentDir:                      persistent,
		UnimplementedDocumentServiceServer: pb.UnimplementedDocumentServiceServer{},
	}, nil
}

func (h *WebHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/", "/upload":
		h.serveUploadPage(w, r)
	case "/api/upload":
		h.handleUpload(w, r)
	case "/download":
		h.handleDownload(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *WebHandler) serveUploadPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	if err := h.templates.Execute(w, nil); err != nil {
		log.Printf("template execute error: %v", err)
	}
}

func (h *WebHandler) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 200<<20) // 200 MB limit

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	reqID, _ := randomHex(4) // correlation ID for log correlation; ignore error, empty string is fine

	jsonStr := func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	sendEvent := func(typ, payload string) {
		// Escape newlines so SSE frame boundaries (\n\n) are never broken.
		payload = strings.ReplaceAll(payload, "\n", " ")
		fmt.Fprintf(w, "data: {\"type\":%s,\"message\":%s}\n\n", jsonStr(typ), jsonStr(payload))
		flusher.Flush()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		sendEvent("error", "No file uploaded")
		return
	}
	defer file.Close()

	// Stream upload to disk to avoid holding two copies in memory.
	tmpFile, err := os.CreateTemp("", "upload_*")
	if err != nil {
		sendEvent("error", "failed to create temporary file")
		return
	}
	uploadPath := tmpFile.Name()
	// Safety net: clean up the temp file if we return before the goroutine
	// takes ownership. Set goroutineTookOwnership=true to cancel the defer.
	goroutineTookOwnership := false
	defer func() {
		if !goroutineTookOwnership {
			os.Remove(uploadPath)
		}
	}()
	if _, err := io.Copy(tmpFile, file); err != nil {
		tmpFile.Close()
		sendEvent("error", "Failed to read file")
		return
	}
	tmpFile.Close()

	fi, _ := os.Stat(uploadPath)
	var fileSizeKB float64
	if fi != nil {
		fileSizeKB = float64(fi.Size()) / 1024
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		ext := strings.ToLower(filepath.Ext(header.Filename))
		mimeType = map[string]string{
			".pdf":  "application/pdf",
			".txt":  "text/plain",
			".md":   "text/markdown",
			".html": "text/html",
		}[ext]
	}

	// Process with a 10-minute timeout.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	logf := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		log.Printf("[upload %s] %s", reqID, msg)
		select {
		case <-ctx.Done():
			return // connection closed or timed out, stop writing to response
		default:
			sendEvent("log", msg)
		}
	}

	logf("File: %q (%s, %.1f KB)", header.Filename, mimeType, fileSizeKB)

	type procResult struct {
		doc *model.ProcessResult
		err error
	}
	resCh := make(chan procResult, 1)
	opts := model.ProcessOptions{
		SmartOCR:       r.FormValue("smart_ocr") == "true",
		ForceOCR:       r.FormValue("force_ocr") == "true",
		StripHeaders:   r.FormValue("strip_headers") != "false",
		StripFootnotes: r.FormValue("strip_footnotes") != "false",
		TextOnly:       r.FormValue("text_only") == "true",
	}

	goroutineTookOwnership = true
	go func() {
		defer os.Remove(uploadPath)
		doc, err := h.documentHandler.Processor.ProcessDocumentFromPath(uploadPath, mimeType, opts, logf)
		resCh <- procResult{doc, err}
	}()

	var docResult *model.ProcessResult
	select {
	case <-ctx.Done():
		sendEvent("error", "conversion timed out after 10 minutes")
		return
	case res := <-resCh:
		if res.err != nil {
			logf("Processing error: %v", res.err)
			sendEvent("error", res.err.Error())
			return
		}
		docResult = res.doc
	}

	logf("Extracted %d chars in %dms", len(docResult.ExtractedText), docResult.ProcessingMs)

	title := strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	epubResult, err := h.epubGenerator.GenerateFromText(docResult.ExtractedText, docResult.Images, docResult.PageCount, title, "Document Author")
	if err != nil {
		logf("EPUB generation error: %v", err)
		sendEvent("error", err.Error())
		return
	}
	logf("EPUB: %d chapters", len(epubResult.Chapters))

	randSuffix, err := randomHex(8)
	if err != nil {
		logf("Failed to generate filename: %v", err)
		sendEvent("error", "server error generating filename")
		return
	}
	filename := fmt.Sprintf("%s_%s.epub", strings.ReplaceAll(title, " ", "_"), randSuffix)
	epubPath := filepath.Join(h.uploadDir, filename)
	if err := h.generateEPUBFile(epubResult, epubPath); err != nil {
		logf("EPUB file error: %v", err)
		os.Remove(epubPath)
		sendEvent("error", err.Error())
		return
	}

	fi, err = os.Stat(epubPath)
	if err != nil {
		logf("Failed to stat epub: %v", err)
		sendEvent("error", "Failed to save file")
		return
	}
	epubKB := float64(fi.Size()) / 1024
	logf("Done -> %s (%.1f KB)", filename, epubKB)
	type doneEvent struct {
		Type         string  `json:"type"`
		DownloadURL  string  `json:"download_url"`
		Filename     string  `json:"filename"`
		Chapters     int     `json:"chapters"`
		Chars        int     `json:"chars"`
		EpubKB       float64 `json:"epub_kb"`
		ProcessingMs int64   `json:"processing_ms"`
	}
	doneJSON, _ := json.Marshal(doneEvent{
		Type:         "done",
		DownloadURL:  "/download?file=" + url.QueryEscape(filename),
		Filename:     filename,
		Chapters:     len(epubResult.Chapters),
		Chars:        len(docResult.ExtractedText),
		EpubKB:       epubKB,
		ProcessingMs: docResult.ProcessingMs,
	})
	fmt.Fprintf(w, "data: %s\n\n", doneJSON)
	flusher.Flush()
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (h *WebHandler) handleDownload(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("file")
	if filename == "" {
		http.Error(w, "Missing filename", http.StatusBadRequest)
		return
	}

	safeName := filepath.Base(filename)
	if filepath.Ext(safeName) != ".epub" {
		http.Error(w, "invalid file", http.StatusBadRequest)
		return
	}
	filePath := filepath.Join(h.uploadDir, safeName)
	if !strings.HasPrefix(filepath.Clean(filePath)+string(filepath.Separator), filepath.Clean(h.uploadDir)+string(filepath.Separator)) {
		http.Error(w, "invalid file", http.StatusBadRequest)
		return
	}

	f, err := os.Open(filePath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/epub+zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", safeName))
	http.ServeContent(w, r, safeName, fi.ModTime(), f)
}

// epubZip wraps zip.Writer and propagates the first write error so callers
// only need to check once at the end via zw.Close().
type epubZip struct {
	zw  *zip.Writer
	err error
}

func (z *epubZip) create(name string) io.Writer {
	if z.err != nil {
		return io.Discard
	}
	w, err := z.zw.Create(name)
	if err != nil {
		z.err = err
		return io.Discard
	}
	return w
}

func (z *epubZip) createHeader(h *zip.FileHeader) io.Writer {
	if z.err != nil {
		return io.Discard
	}
	w, err := z.zw.CreateHeader(h)
	if err != nil {
		z.err = err
		return io.Discard
	}
	return w
}

func (z *epubZip) write(name string, data []byte) {
	if z.err != nil {
		return
	}
	w, err := z.zw.Create(name)
	if err != nil {
		z.err = err
		return
	}
	if _, err := w.Write(data); err != nil {
		z.err = err
	}
}

func (z *epubZip) close() error {
	if z.err != nil {
		return z.err
	}
	return z.zw.Close()
}

func (h *WebHandler) generateEPUBFile(epubResult *model.EPUBResult, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	z := &epubZip{zw: zip.NewWriter(f), err: nil}

	// mimetype must be the first entry and stored uncompressed per EPUB spec.
	fmt.Fprintf(z.createHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store}), "application/epub+zip")

	fmt.Fprintf(z.create("META-INF/container.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`)

	chapters := epubResult.Chapters

	// Collect all images across all chapters for the OPF manifest.
	var allImages []model.PDFImage
	for _, ch := range chapters {
		allImages = append(allImages, ch.Images...)
	}

	// Build manifest items, spine itemrefs, and NCX navPoints for each chapter.
	var manifest, spine, navMap strings.Builder
	manifest.WriteString("    <item id=\"ncx\" href=\"toc.ncx\" media-type=\"application/x-dtbncx+xml\"/>\n")
	for i, ch := range chapters {
		id := fmt.Sprintf("chapter%d", i+1)
		href := fmt.Sprintf("chapter%d.xhtml", i+1)
		fmt.Fprintf(&manifest, "    <item id=\"%s\" href=\"%s\" media-type=\"application/xhtml+xml\"/>\n", id, href)
		fmt.Fprintf(&spine, "    <itemref idref=\"%s\"/>\n", id)
		fmt.Fprintf(&navMap,
			"    <navPoint id=\"navpoint-%d\" playOrder=\"%d\">\n      <navLabel><text>%s</text></navLabel>\n      <content src=\"%s\"/>\n    </navPoint>\n",
			i+1, i+1, template.HTMLEscapeString(ch.Title), href)
	}
	// Add image items to manifest.
	for _, img := range allImages {
		fmt.Fprintf(&manifest, "    <item id=\"%s\" href=\"images/%s\" media-type=\"%s\"/>\n",
			strings.TrimSuffix(img.Name, filepath.Ext(img.Name)), img.Name, img.MimeType)
	}

	fmt.Fprintf(z.create("OEBPS/content.opf"), `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>%s</dc:title>
    <dc:creator>%s</dc:creator>
    <dc:language>%s</dc:language>
    <dc:identifier id="bookid">%s</dc:identifier>
  </metadata>
  <manifest>
%s  </manifest>
  <spine toc="ncx">
%s  </spine>
</package>`,
		template.HTMLEscapeString(epubResult.Title),
		template.HTMLEscapeString(epubResult.Author),
		epubResult.Language,
		template.HTMLEscapeString(epubResult.Title),
		manifest.String(), spine.String())

	fmt.Fprintf(z.create("OEBPS/toc.ncx"), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE ncx PUBLIC "-//NISO//DTD ncx 2005-1//EN" "http://www.daisy.org/z3986/2005/ncx-2005-1.dtd">
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head><meta name="dtb:uid" content="bookid"/></head>
  <navMap>
%s  </navMap>
</ncx>`, navMap.String())

	// Write image files into OEBPS/images/.
	for _, img := range allImages {
		z.write("OEBPS/images/"+img.Name, img.Data)
	}

	// Write each chapter as a separate XHTML document.
	for i, ch := range chapters {
		// Build figNum → image map for inline insertion.
		type figEntry struct {
			name          string
			widthFraction float64
		}
		figMap := make(map[int]figEntry)
		for _, img := range ch.Images {
			if img.FigNum > 0 {
				figMap[img.FigNum] = figEntry{img.Name, img.WidthFraction}
			}
		}

		bodyHTML := textToHTML(ch.Content)

		imgStyle := func(wf float64) string {
			if wf > 0 && wf < 0.99 {
				return fmt.Sprintf(` style="width:%.0f%%"`, wf*100)
			}
			return ""
		}

		// Insert figure images inline: collect all insertion points first (one
		// scan per figure), then apply in reverse position order so earlier
		// offsets are not shifted by subsequent insertions.
		type figInsertion struct {
			pos   int
			block string
		}
		var insertions []figInsertion
		for figNum, fe := range figMap {
			imgBlock := fmt.Sprintf(
				`<div class="fig"><img src="images/%s"%s alt="Fig. %d"/></div>`,
				template.HTMLEscapeString(fe.name), imgStyle(fe.widthFraction), figNum)

			// Candidates in priority order: caption-start paragraph first,
			// then any paragraph that mentions the figure number.
			candidates := []string{
				fmt.Sprintf("<p>Fig. %d", figNum),
				fmt.Sprintf("<p>Fig %d", figNum),
				fmt.Sprintf("<p>fig. %d", figNum),
				fmt.Sprintf("Fig. %d", figNum),
				fmt.Sprintf("Fig %d", figNum),
			}
			for _, needle := range candidates {
				idx := strings.Index(bodyHTML, needle)
				if idx < 0 {
					continue
				}
				var pStart int
				if strings.HasPrefix(needle, "<p>") {
					pStart = idx
				} else {
					pStart = strings.LastIndex(bodyHTML[:idx], "<p>")
					if pStart < 0 {
						pStart = idx
					}
				}
				insertions = append(insertions, figInsertion{pStart, imgBlock})
				break
			}
		}
		// Apply insertions from highest offset to lowest so preceding offsets
		// remain valid as we splice the string.
		sort.Slice(insertions, func(i, j int) bool {
			return insertions[i].pos > insertions[j].pos
		})
		for _, ins := range insertions {
			bodyHTML = bodyHTML[:ins.pos] + ins.block + bodyHTML[ins.pos:]
		}

		// Append any images that couldn't be placed inline (no matching caption
		// found in text) as a fallback gallery at chapter end.
		var fallback strings.Builder
		for _, img := range ch.Images {
			style := imgStyle(img.WidthFraction)
			if img.FigNum == 0 {
				fmt.Fprintf(&fallback, `<div class="fig"><img src="images/%s"%s alt=""/></div>`,
					template.HTMLEscapeString(img.Name), style)
			} else if !strings.Contains(bodyHTML, "images/"+img.Name) {
				fmt.Fprintf(&fallback, `<div class="fig"><img src="images/%s"%s alt="Fig. %d"/></div>`,
					template.HTMLEscapeString(img.Name), style, img.FigNum)
			}
		}

		fmt.Fprintf(z.create(fmt.Sprintf("OEBPS/chapter%d.xhtml", i+1)),
			`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.1//EN" "http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>%s</title><style type="text/css">div.fig{text-align:center;margin:1.5em 0;}img{max-width:100%%;height:auto;}</style></head>
<body>
<h2>%s</h2>
%s%s
</body>
</html>`, template.HTMLEscapeString(ch.Title), template.HTMLEscapeString(ch.Title),
			bodyHTML, fallback.String())
	}

	return z.close()
}

// textToHTML converts plain text from pdftotext/OCR into XHTML paragraphs.
// It handles:
//   - Soft-hyphenated line breaks (­\n or -\n at line end) → joined without space
//   - Sentence-ending lines (ending with . ? ! " ") followed by a new paragraph-start
//     (capital/quote/„) → split into separate <p> elements
//   - All other single newlines → space (rejoin wrapped lines)
//   - Explicit double-newline gaps → paragraph break
func textToHTML(text string) string {
	// 1. Join soft-hyphenated breaks: "wor­\nld" → "world", "wor-\nld" → "world"
	text = strings.ReplaceAll(text, "\u00ad\n", "") // soft hyphen
	text = strings.ReplaceAll(text, "-\n", "-")

	// 2. Split into lines and detect paragraph boundaries.
	lines := strings.Split(text, "\n")
	var paragraphs []string
	var current strings.Builder

	sentenceEnd := func(s string) bool {
		if s == "" {
			return false
		}
		runes := []rune(s)
		last := runes[len(runes)-1]
		return last == '.' || last == '?' || last == '!' || last == '"' || last == '\'' || last == '\u201d' || last == '»'
	}
	paraStart := func(s string) bool {
		if s == "" {
			return false
		}
		r := []rune(s)
		first := r[0]
		return first == '„' || first == '"' || first == '—' || first == '-' ||
			(first >= 'A' && first <= 'Z') ||
			(first >= 'А' && first <= 'Я') || // Cyrillic uppercase
			(first >= 0x00C0 && first <= 0x024F) // Latin extended uppercase (Č Š Ž Ć Đ…)
	}

	flush := func() {
		if s := strings.TrimSpace(current.String()); s != "" {
			paragraphs = append(paragraphs, s) // unescaped; escape happens at render time
		}
		current.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Blank line → always a paragraph break
		if trimmed == "" {
			flush()
			continue
		}

		if current.Len() > 0 {
			// Decide: continue current paragraph or start a new one?
			prev := strings.TrimSpace(current.String())
			if sentenceEnd(prev) && paraStart(trimmed) {
				flush()
			} else {
				current.WriteString(" ")
			}
		}
		current.WriteString(trimmed)
	}
	flush()

	// For OCR output there are no blank lines between paragraphs, so the
	// heuristic above can miss sentence boundaries that fall mid-line. Split
	// any paragraph that accumulated too many sentences into smaller chunks.
	paragraphs = splitLongParagraphs(paragraphs, 4)

	if len(paragraphs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range paragraphs {
		b.WriteString("<p>")
		b.WriteString(template.HTMLEscapeString(p))
		b.WriteString("</p>")
	}
	return b.String()
}

// splitLongParagraphs splits any paragraph that contains more than maxSentences
// sentence-ending punctuation marks into multiple paragraphs. Splitting occurs
// at ". "/? "/! " boundaries followed by an uppercase letter, grouping every
// maxSentences sentences into one paragraph.
func splitLongParagraphs(paragraphs []string, maxSentences int) []string {
	out := make([]string, 0, len(paragraphs))
	for _, para := range paragraphs {
		out = append(out, splitAtSentenceBoundaries(para, maxSentences)...)
	}
	return out
}

// minParaLenToSplit is the minimum byte length a paragraph must exceed before
// sentence-boundary splitting is attempted. Short paragraphs are fine as-is.
const minParaLenToSplit = 350

func splitAtSentenceBoundaries(para string, maxSentences int) []string {
	if len(para) < minParaLenToSplit {
		return []string{para}
	}
	runes := []rune(para)
	n := len(runes)
	var result []string
	start := 0
	sentences := 0
	for i := 0; i < n; i++ {
		r := runes[i]
		if r == '.' || r == '?' || r == '!' {
			sentences++
			// Split after maxSentences sentences when followed by " [UpperLetter]".
			if sentences >= maxSentences && i+2 < n && runes[i+1] == ' ' && isUpperStart(runes[i+2]) {
				result = append(result, strings.TrimSpace(string(runes[start:i+1])))
				start = i + 2 // skip the space
				sentences = 0
			}
		}
	}
	if tail := strings.TrimSpace(string(runes[start:])); tail != "" {
		result = append(result, tail)
	}
	if len(result) == 0 {
		return []string{para}
	}
	return result
}

// isUpperStart returns true for characters that can open a new sentence after
// a paragraph split: uppercase ASCII/Latin-extended/Cyrillic letters, quotes,
// and dash characters.
func isUpperStart(r rune) bool {
	return (r >= 'A' && r <= 'Z') ||
		(r >= 'А' && r <= 'Я') || // Cyrillic uppercase
		(r >= 0x00C0 && r <= 0x024F) || // Latin extended (Č Š Ž Ć Đ…)
		r == '"' || r == '„' || r == '—' || r == '-'
}

func (h *WebHandler) Close() error {
	if !h.persistentDir {
		os.RemoveAll(h.uploadDir)
	}
	if h.epubGenerator != nil {
		h.epubGenerator.Close()
	}
	return nil
}
