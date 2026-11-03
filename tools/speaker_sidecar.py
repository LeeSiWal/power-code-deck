#!/usr/bin/env python3
"""Local-only experimental ECAPA-TDNN voice embedding sidecar for PowerCodeDeck.

Install into an isolated environment:
  python -m venv .venv
  .venv/bin/pip install speechbrain torch torchaudio soundfile
  .venv/bin/python tools/speaker_sidecar.py

This is NOT a voice anti-spoofing or biometric account-login system.
The PowerCodeDeck server must enforce enrollment consent, approval gates, and
voice-command provenance independently of this local inference helper.
"""
from __future__ import annotations

import io
import json
import os
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import torch
import torchaudio
from speechbrain.inference.speaker import EncoderClassifier

HOST = "127.0.0.1"
PORT = int(os.getenv("PCD_SPEAKER_PORT", "18743"))
MAX_BYTES = 4 * 1024 * 1024
MODEL = "speechbrain/spkrec-ecapa-voxceleb"

torch.set_num_threads(2)
classifier = EncoderClassifier.from_hparams(
    source=MODEL,
    savedir=os.path.expanduser("~/.cache/powercodedeck/speaker-model"),
    run_opts={"device": "cpu"},
)
inference_lock = threading.Lock()


def embedding(wav_bytes: bytes) -> list[float]:
    if len(wav_bytes) < 44 or len(wav_bytes) > MAX_BYTES:
        raise ValueError("invalid WAV size")
    signal, sample_rate = torchaudio.load(io.BytesIO(wav_bytes), format="wav")
    if signal.ndim != 2 or signal.shape[0] < 1 or signal.shape[0] > 2:
        raise ValueError("expected mono or stereo WAV")
    duration = signal.shape[1] / sample_rate
    if not 1.5 <= duration <= 15.0:
        raise ValueError("speech sample must be between 1.5 and 15 seconds")
    signal = signal.mean(dim=0, keepdim=True)
    if sample_rate != 16000:
        signal = torchaudio.functional.resample(signal, sample_rate, 16000)
    with inference_lock, torch.inference_mode():
        encoded = classifier.encode_batch(signal).flatten().cpu()
        encoded = torch.nn.functional.normalize(encoded, dim=0)
    return encoded.tolist()


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != "/embed":
            self.send_error(404)
            return
        size = self.headers.get("Content-Length", "")
        if not size.isdigit() or not 44 <= int(size) <= MAX_BYTES:
            self.send_error(413, "invalid sample size")
            return
        try:
            body = self.rfile.read(int(size))
            result = json.dumps({"embedding": embedding(body)}, separators=(",", ":")).encode()
        except (ValueError, RuntimeError, OSError) as exc:
            self.send_error(422, str(exc)[:128])
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(result)))
        self.end_headers()
        self.wfile.write(result)

    def log_message(self, fmt, *args):
        # Never log audio or utterance content.
        pass


if __name__ == "__main__":
    print(f"Speaker embedding helper listening on {HOST}:{PORT}; loopback only")
    ThreadingHTTPServer((HOST, PORT), Handler).serve_forever()
