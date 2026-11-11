# Voice note transcription worker for the WhatsApp connector.
# Runs locally with faster-whisper: on the NVIDIA GPU when it works, on the
# CPU otherwise. Prints exactly one JSON object on stdout; logs go to stderr.
#
#   worker.py transcribe <audio-file> <auto|es|en>
#   worker.py download        fetch the model into MODEL_DIR
#   worker.py check           load the model and report the device that works
import json
import os
import sys
import time

MODEL = os.environ.get("WHISPER_MODEL", "large-v3-turbo")
MODEL_DIR = os.environ.get("MODEL_DIR") or os.path.join(os.path.dirname(os.path.abspath(__file__)), "models")
PREFER = os.environ.get("WHISPER_DEVICE", "auto")  # auto, cuda, cpu


def out(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def log(msg):
    sys.stderr.write(str(msg) + "\n")
    sys.stderr.flush()


def add_nvidia_dlls():
    """The CUDA libraries come as pip packages; Windows has to be told where."""
    if os.name != "nt":
        return
    import site
    dirs = []
    for sp in site.getsitepackages() + [site.getusersitepackages()]:
        for sub in ("cublas", "cudnn", "cuda_nvrtc"):
            d = os.path.join(sp, "nvidia", sub, "bin")
            if os.path.isdir(d):
                dirs.append(d)
    for d in dirs:
        try:
            os.add_dll_directory(d)
        except OSError:
            pass
    if dirs:
        os.environ["PATH"] = os.pathsep.join(dirs) + os.pathsep + os.environ.get("PATH", "")


def load(device):
    from faster_whisper import WhisperModel
    compute = "float16" if device == "cuda" else "int8"
    return WhisperModel(MODEL, device=device, compute_type=compute, download_root=MODEL_DIR,
                        local_files_only=os.environ.get("HF_HUB_OFFLINE") == "1")


def devices():
    if PREFER == "cpu":
        return ["cpu"]
    if PREFER == "cuda":
        return ["cuda"]
    return ["cuda", "cpu"]


def run(task):
    """Try the GPU first; if anything about it fails, do the same on the CPU."""
    errors = []
    for dev in devices():
        try:
            model = load(dev)
            return task(model), dev, errors
        except Exception as e:  # missing cuDNN, no GPU, out of memory...
            errors.append("%s: %s" % (dev, e))
            log("device %s failed: %s" % (dev, e))
    raise RuntimeError("; ".join(errors))


def transcribe(path, lang):
    language = None if lang in ("", "auto") else lang

    def task(model):
        segments, info = model.transcribe(path, language=language, beam_size=5, vad_filter=True)
        parts = [s.text.strip() for s in segments]  # decoding happens here
        return {"text": " ".join(p for p in parts if p), "language": info.language,
                "duration": round(float(info.duration or 0), 1)}

    t0 = time.time()
    res, dev, errors = run(task)
    res.update({"device": dev, "model": MODEL, "seconds": round(time.time() - t0, 1)})
    if errors:
        res["fallback"] = errors
    out(res)


def check():
    def task(model):
        import numpy as np
        segments, _ = model.transcribe(np.zeros(16000, dtype="float32"), language="es", vad_filter=False)
        list(segments)
        return {}

    res, dev, errors = run(task)
    res.update({"ok": True, "device": dev, "model": MODEL})
    if errors:
        res["fallback"] = errors
    out(res)


def download():
    from faster_whisper.utils import download_model
    path = download_model(MODEL, cache_dir=MODEL_DIR)
    out({"ok": True, "path": path})


def main():
    add_nvidia_dlls()
    if len(sys.argv) < 2:
        out({"error": "usage"})
        return 2
    cmd = sys.argv[1]
    try:
        if cmd == "transcribe" and len(sys.argv) >= 3:
            transcribe(sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else "auto")
        elif cmd == "check":
            check()
        elif cmd == "download":
            download()
        else:
            out({"error": "unknown command"})
            return 2
    except Exception as e:
        out({"error": str(e)})
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
