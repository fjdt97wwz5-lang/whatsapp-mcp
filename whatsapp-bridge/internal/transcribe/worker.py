# Voice note transcription worker for the WhatsApp connector.
# Runs locally with faster-whisper: on the NVIDIA GPU when it works, on the
# CPU otherwise. Prints exactly one JSON object on stdout; logs go to stderr.
#
#   worker.py transcribe <audio-file> <auto|es|en>
#   worker.py download        fetch the model into MODEL_DIR
#   worker.py check           load the model and report the device that works
import json
import os
import shutil
import subprocess
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


def decode(path):
    """Read any audio file (WhatsApp sends Opus in .ogg) as 16 kHz mono float32.

    faster-whisper can do this itself, but its decoder passes an argument
    (metadata_errors) that newer PyAV removed, so we decode here and hand it
    the samples. This works with old and new PyAV alike."""
    import av
    import numpy as np
    chunks = []
    with av.open(path, mode="r") as container:
        if not container.streams.audio:
            raise RuntimeError("the file has no audio")
        stream = container.streams.audio[0]
        resampler = av.audio.resampler.AudioResampler(format="s16", layout="mono", rate=16000)

        def add(frames):
            if frames is None:
                return
            if not isinstance(frames, list):
                frames = [frames]
            for fr in frames:
                chunks.append(fr.to_ndarray().reshape(-1))

        for frame in container.decode(stream):
            frame.pts = None
            add(resampler.resample(frame))
        add(resampler.resample(None))  # flush
    if not chunks:
        raise RuntimeError("the audio is empty")
    return np.concatenate(chunks).astype(np.float32) / 32768.0


def nvidia_driver():
    """Driver version and card name as nvidia-smi reports them, or None."""
    exe = shutil.which("nvidia-smi")
    if not exe and os.name == "nt":
        for c in (os.path.join(os.environ.get("SystemRoot", r"C:\Windows"), "System32", "nvidia-smi.exe"),
                  r"C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe"):
            if os.path.isfile(c):
                exe = c
                break
    if not exe:
        return None
    try:
        flags = 0x08000000 if os.name == "nt" else 0  # no console window
        r = subprocess.run([exe, "--query-gpu=driver_version,name", "--format=csv,noheader"],
                           capture_output=True, text=True, timeout=15, creationflags=flags)
        line = (r.stdout or "").strip().splitlines()
        if r.returncode != 0 or not line:
            return None
        parts = [p.strip() for p in line[0].split(",", 1)]
        return {"version": parts[0], "name": parts[1] if len(parts) > 1 else ""}
    except Exception as e:  # nvidia-smi missing or stuck
        log("nvidia-smi: %s" % e)
        return None


def transcribe(path, lang):
    language = None if lang in ("", "auto") else lang
    try:
        audio = decode(path)
    except Exception as e:
        raise RuntimeError("could not read the audio file: %s" % e)

    def task(model):
        segments, info = model.transcribe(audio, language=language, beam_size=5, vad_filter=True)
        parts = [s.text.strip() for s in segments]  # decoding happens here
        return {"text": " ".join(p for p in parts if p), "language": info.language,
                "duration": round(float(info.duration or 0), 1)}

    t0 = time.time()
    res, dev, errors = run(task)
    res.update({"device": dev, "model": MODEL, "seconds": round(time.time() - t0, 1)})
    if errors:
        res["fallback"] = errors
        drv = nvidia_driver()
        if drv:
            res["driver"] = drv
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
    drv = nvidia_driver()
    if drv:
        res["driver"] = drv
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
