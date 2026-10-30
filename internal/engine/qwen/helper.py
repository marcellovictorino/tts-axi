"""tts-axi's cloned-voice helper: speaks a batch of lines with Qwen3-TTS Base.

Reads one JSON job on stdin:
  {"model": "/path/to/model-folder", "lang": "en",
   "items": [{"text": "...", "ref_audio": "/path/ref.wav", "ref_text": "the clip's words",
              "out": "/path/0001.wav"}]}
and prints one JSON line per event on stdout, like the other helpers. Each
item is cloned from its own reference clip. Everything runs on this Mac.
"""
import json
import os
import sys
import time
import wave

LANGS = {"en": "english", "fr": "french", "pt": "portuguese"}


def emit(**record):
    print(json.dumps(record, sort_keys=True), flush=True)


def write_wav(path, samples, rate):
    import numpy as np

    pcm = (np.clip(samples, -1.0, 1.0) * 32767).astype("<i2")
    with wave.open(path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(pcm.tobytes())


def main():
    job = json.load(sys.stdin)
    import mlx.core as mx
    import numpy as np
    from mlx_audio.tts.utils import load_model

    start = time.monotonic()
    model = load_model(job["model"])
    mx.eval(model.parameters())  # mlx loads lazily; make the weights resident now
    emit(event="loaded", load_s=time.monotonic() - start, unit="gpu")
    lang = LANGS.get(job["lang"], "auto")
    for i, item in enumerate(job["items"]):
        t = time.monotonic()
        chunks = [
            np.array(r.audio, dtype="float32").reshape(-1)
            for r in model.generate(
                text=item["text"], ref_audio=item["ref_audio"], ref_text=item["ref_text"], lang_code=lang
            )
        ]
        if not chunks:
            raise RuntimeError("the model produced no audio for line %d" % (i + 1))
        audio = np.concatenate(chunks)
        part = item["out"] + ".part.wav"
        write_wav(part, audio, model.sample_rate)
        os.replace(part, item["out"])  # never leave half a file under the real name
        emit(event="item", index=i, audio_s=len(audio) / model.sample_rate, gen_s=time.monotonic() - t)
    emit(event="done")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        emit(event="error", message=f"{type(e).__name__}: {e}")
        sys.exit(1)
