// vision_ocr.swift — macOS Vision framework OCR helper.
// Inspired by https://github.com/schappim/macOCR
//
// Usage: vision_ocr --image <path> [--language <lang>]
//   --language  BCP-47 language tag (e.g. "hr", "en-US"). Defaults to "en-US".
//
// Always outputs a single JSON line to stdout:
//   {"text":"…","obs":[{"t":"…","x":f,"y":f,"w":f,"h":f},…]}
//
// boundingBox coordinates follow Vision convention: origin at bottom-left of
// the image, Y=0 is the bottom edge, Y=1 is the top edge.

import AppKit
import Foundation
import Vision

// MARK: — Argument parsing

var imagePath: String?
var primaryLanguage = "en-US"

var args = CommandLine.arguments.dropFirst()
while !args.isEmpty {
    let arg = args.removeFirst()
    switch arg {
    case "--image":
        if !args.isEmpty { imagePath = String(args.removeFirst()) }
    case "--language":
        if !args.isEmpty { primaryLanguage = String(args.removeFirst()) }
    default:
        break
    }
}

guard let path = imagePath else {
    fputs("Usage: vision_ocr --image <path> [--language <lang>]\n", stderr)
    exit(1)
}

// MARK: — Load image

guard let nsImage = NSImage(contentsOfFile: path),
      let cgImage = nsImage.cgImage(forProposedRect: nil, context: nil, hints: nil)
else {
    fputs("vision_ocr: failed to load image at \(path)\n", stderr)
    exit(1)
}

// MARK: — Vision request (macOCR approach)

struct ObsEntry: Encodable {
    let t: String
    let x, y, w, h: Double
}
struct PageOutput: Encodable {
    let text: String
    let obs: [ObsEntry]
}

let semaphore = DispatchSemaphore(value: 0)
var recognisedObs: [(text: String, bbox: CGRect)] = []
var visionError: Error?

let request = VNRecognizeTextRequest { req, err in
    defer { semaphore.signal() }
    if let err = err { visionError = err; return }
    guard let observations = req.results as? [VNRecognizedTextObservation] else { return }
    for obs in observations {
        if let top = obs.topCandidates(1).first, !top.string.isEmpty {
            recognisedObs.append((text: top.string, bbox: obs.boundingBox))
        }
    }
}

// Use the accurate recognition level (same as macOCR).
request.recognitionLevel = .accurate
request.usesLanguageCorrection = true

if #available(macOS 15.0, *) {
    request.revision = VNRecognizeTextRequestRevision3
} else if #available(macOS 11.0, *) {
    request.revision = VNRecognizeTextRequestRevision2
}

var languages = [primaryLanguage]
if primaryLanguage != "en-US" { languages.append("en-US") }
request.recognitionLanguages = languages

// MARK: — Perform

let handler = VNImageRequestHandler(cgImage: cgImage, options: [:])
do {
    try handler.perform([request])
} catch {
    fputs("vision_ocr: perform error: \(error)\n", stderr)
    exit(1)
}

semaphore.wait()

if let err = visionError {
    fputs("vision_ocr: recognition error: \(err)\n", stderr)
    exit(1)
}

// MARK: — Output JSON

let obsEntries = recognisedObs.map { o in
    ObsEntry(t: o.text,
             x: o.bbox.origin.x,
             y: o.bbox.origin.y,
             w: o.bbox.size.width,
             h: o.bbox.size.height)
}
let output = PageOutput(
    text: recognisedObs.map(\.text).joined(separator: "\n"),
    obs: obsEntries
)

let encoder = JSONEncoder()
encoder.outputFormatting = []
if let data = try? encoder.encode(output),
   let json = String(data: data, encoding: .utf8) {
    print(json)
} else {
    fputs("vision_ocr: failed to encode output\n", stderr)
    exit(1)
}
