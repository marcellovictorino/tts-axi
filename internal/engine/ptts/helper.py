"""tts-axi's multilingual helper: speaks a batch of lines with ptts on the CPU.

Reads one JSON job on stdin:
  {"model": "/path/to/model-folder", "lang": "pt", "speed": 0.15,
   "items": [{"text": "...", "voice": "Davi", "out": "/path/0001.wav"}]}
and prints one JSON line per event on stdout, like the Swift helper.
"""
import json
import os
import sys
import time


def emit(**record):
    print(json.dumps(record, sort_keys=True), flush=True)


def main():
    job = json.load(sys.stdin)
    import ptts

    start = time.monotonic()
    conditions = {"duration_delta": job["speed"]}
    try:
        tts = ptts.TTS(job["model"], lang=job["lang"], quant="q8", conditions=conditions)
    except Exception as e:  # a model without this conditioner cannot change speed
        if "conditioner" not in str(e):
            raise
        emit(event="warning", message="this model has no speed setting: " + str(e))
        tts = ptts.TTS(job["model"], lang=job["lang"], quant="q8")
    emit(event="loaded", load_s=time.monotonic() - start, unit="cpu")
    for i, item in enumerate(job["items"]):
        t = time.monotonic()
        part = item["out"] + ".part.wav"
        audio_s = tts.save(part, item["text"], voice=item["voice"])
        os.replace(part, item["out"])  # never leave half a file under the real name
        emit(event="item", index=i, audio_s=audio_s, gen_s=time.monotonic() - t)
    emit(event="done")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        emit(event="error", message=f"{type(e).__name__}: {e}")
        sys.exit(1)
