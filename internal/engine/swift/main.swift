// tts-axi-helper speaks a batch of lines with Phonon through Core ML.
//
// It reads one JSON job on stdin, loads the model once on the requested
// compute unit, writes one 24 kHz mono 16-bit WAV per item and prints one JSON
// line per event on stdout. tts-axi builds it with swiftc against Gradium's
// released PhononCore.xcframework and the official Phonon.swift wrapper.
import Foundation
import Synchronization

struct Job: Decodable {
    struct Item: Decodable {
        let text: String
        let voice: String
        let out: String
    }
    let model: String
    let unit: String  // "ane" or "cpu"
    let lang: String
    let items: [Item]
}

func emit(_ record: [String: Any]) {
    let data = try! JSONSerialization.data(withJSONObject: record, options: [.sortedKeys])
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data([10]))
}

func writeWAV(_ samples: [Float], to path: String) throws {
    var d = Data()
    func s(_ v: String) { d.append(contentsOf: v.utf8) }
    func u16(_ v: UInt16) { withUnsafeBytes(of: v.littleEndian) { d.append(contentsOf: $0) } }
    func u32(_ v: UInt32) { withUnsafeBytes(of: v.littleEndian) { d.append(contentsOf: $0) } }
    let rate = UInt32(Phonon.sampleRate)
    s("RIFF"); u32(UInt32(36 + samples.count * 2)); s("WAVE")
    s("fmt "); u32(16); u16(1); u16(1); u32(rate); u32(rate * 2); u16(2); u16(16)
    s("data"); u32(UInt32(samples.count * 2))
    for a in samples { u16(UInt16(bitPattern: Int16(max(-1, min(1, a)) * 32767))) }
    // Write beside the target and rename, so a stopped run never leaves half a file.
    let tmp = path + ".part"
    try d.write(to: URL(fileURLWithPath: tmp))
    _ = try FileManager.default.replaceItemAt(URL(fileURLWithPath: path), withItemAt: URL(fileURLWithPath: tmp))
}

func now() -> Double { ProcessInfo.processInfo.systemUptime }

@main
struct Helper {
    static func main() async {
        do {
            let input = FileHandle.standardInput.readDataToEndOfFile()
            let job = try JSONDecoder().decode(Job.self, from: input)
            guard let lang = Language(rawValue: job.lang) else {
                throw PhononError(description: "unknown language \(job.lang)")
            }
            let unit: ComputeUnit = job.unit == "cpu" ? .cpu : .neuralEngine
            let start = now()
            let compiling = Phonon.needsCompiling(URL(fileURLWithPath: job.model))
            let tts = try await Phonon.load(models: URL(fileURLWithPath: job.model), language: lang, computeUnit: unit)
            emit(["event": "loaded", "load_s": now() - start, "compiled": compiling, "unit": job.unit])
            for (i, item) in job.items.enumerated() {
                if tts.voice != item.voice {
                    try await tts.setVoice(item.voice)
                }
                let t = now()
                let samples = try await tts.synthesize(item.text)
                try writeWAV(samples, to: item.out)
                let audio = Double(samples.count) / Phonon.sampleRate
                emit(["event": "item", "index": i, "audio_s": audio, "gen_s": now() - t, "samples": samples.count])
            }
            emit(["event": "done"])
        } catch {
            emit(["event": "error", "message": "\(error)"])
            exit(1)
        }
    }
}
