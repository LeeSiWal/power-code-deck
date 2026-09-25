#!/usr/bin/env python3
"""PowerCodeDeck RouteLLM sidecar.

Loads one RouteLLM router (default: the local BERT classifier) and answers
POST /v1/route with RouteLLM's strong-win-rate score and Controller.route's
strong/weak choice for a threshold. It knows nothing about CLIs, subscriptions
or profiles: PowerCodeDeck maps "strong"/"weak" onto a configured profile pair.

Safety defaults
- Binds 127.0.0.1. Any other host requires --token-env (Bearer auth).
- Never calls an external API. routellm imports an OpenAI client at import
  time (routers/similarity_weighted/utils.py), so a placeholder key and an
  unroutable base URL are set before import; mf/sw_ranking (which embed with
  OpenAI) are refused.
- No model download unless --allow-download; otherwise Hugging Face runs
  offline against the local cache.
- Prompt text is never logged.
"""
import argparse
import hmac
import json
import os
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOCAL_ROUTERS = {"bert": "routellm/bert_gpt4_augmented"}
MAX_BODY = 16 * 1024
MAX_TEXT = 8 * 1024


def parse_args():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--host", default="127.0.0.1")
    p.add_argument("--port", type=int, default=8765)
    p.add_argument("--router", default="bert", choices=sorted(LOCAL_ROUTERS))
    p.add_argument("--checkpoint", default="", help="HF id or local path (default: the router's GPT-4-augmented checkpoint)")
    p.add_argument("--device", default="auto", choices=["auto", "cpu", "cuda", "mps"])
    p.add_argument("--threads", type=int, default=0, help="torch CPU threads (0 = torch default)")
    p.add_argument("--allow-download", action="store_true", help="permit fetching the checkpoint from Hugging Face")
    p.add_argument("--token-env", default="", help="env var holding a bearer token (required for non-loopback --host)")
    p.add_argument("--self-test", action="store_true", help="load, score two prompts, print JSON and exit")
    return p.parse_args()


def isolate_network(allow_download):
    # Placeholder only: satisfies OpenAI() at import; any use fails locally.
    os.environ.setdefault("OPENAI_API_KEY", "pcd-routellm-sidecar-no-external-calls")
    os.environ["OPENAI_BASE_URL"] = "http://127.0.0.1:9/v1"
    os.environ.setdefault("TOKENIZERS_PARALLELISM", "false")
    os.environ.setdefault("HF_HUB_DISABLE_TELEMETRY", "1")
    if not allow_download:
        os.environ["HF_HUB_OFFLINE"] = "1"
        os.environ["TRANSFORMERS_OFFLINE"] = "1"


class Engine:
    def __init__(self, args):
        import numpy as np
        import torch
        import routellm
        from importlib.metadata import version
        from routellm.controller import Controller

        self.np, self.torch = np, torch
        if args.threads > 0:
            torch.set_num_threads(args.threads)
        self.router_name = args.router
        self.checkpoint = args.checkpoint or LOCAL_ROUTERS[args.router]
        self.device = self._pick_device(args.device)
        t0 = time.perf_counter()
        self.controller = Controller(
            routers=[self.router_name],
            strong_model="strong",
            weak_model="weak",
            config={self.router_name: {"checkpoint_path": self.checkpoint}},
        )
        self.router = self.controller.routers[self.router_name]
        self.router.model.eval()
        self.load_ms = int((time.perf_counter() - t0) * 1000)
        self.package = "routellm==" + version("routellm")
        self.versions = {"torch": torch.__version__, "transformers": version("transformers"), "numpy": np.__version__, "python": sys.version.split()[0]}
        self._lock = threading.Lock()
        self._last = None
        upstream = self.router.calculate_strong_win_rate
        if self.device != "cpu":
            # Upstream BERTRouter never moves the model/inputs to a device and
            # calls .numpy() on the logits, which fails for CUDA/MPS tensors.
            # Same formula, device-correct.
            self.router.model.to(self.device)
            upstream = self._device_score

        def recording(prompt):
            score = float(upstream(prompt))
            self._last = score
            return score

        # Controller.route → Router.route → calculate_strong_win_rate: the score
        # is computed exactly once per request and recorded here.
        self.router.calculate_strong_win_rate = recording

    def _pick_device(self, want):
        t = self.torch
        if want == "auto":
            if t.cuda.is_available():
                return "cuda"
            if getattr(t.backends, "mps", None) and t.backends.mps.is_available():
                return "mps"
            return "cpu"
        if want == "cuda" and not t.cuda.is_available():
            raise SystemExit("CUDA requested but torch.cuda.is_available() is False")
        if want == "mps" and not (getattr(t.backends, "mps", None) and t.backends.mps.is_available()):
            raise SystemExit("MPS requested but not available")
        return want

    def _device_score(self, prompt):
        r, t, np = self.router, self.torch, self.np
        inputs = r.tokenizer(prompt, return_tensors="pt", padding=True, truncation=True)
        inputs = {k: v.to(self.device) for k, v in inputs.items()}
        with t.no_grad():
            logits = r.model(**inputs).logits.detach().cpu().numpy()[0]
        exp = np.exp(logits - np.max(logits))
        soft = exp / np.sum(exp)
        return 1 - np.sum(soft[-2:])

    def route(self, text, threshold):
        with self._lock:  # one forward pass at a time; keeps memory bounded
            t0 = time.perf_counter()
            choice = self.controller.route(prompt=text, router=self.router_name, threshold=threshold)
            score = self._last
            ms = int((time.perf_counter() - t0) * 1000)
        return score, choice, ms


def make_handler(engine, token):
    class Handler(BaseHTTPRequestHandler):
        server_version = "pcd-routellm/1"

        def log_message(self, fmt, *args):  # no prompt text, no headers
            pass

        def _send(self, code, body):
            data = json.dumps(body).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def _authorized(self):
            if not token:
                return True
            got = self.headers.get("Authorization", "")
            return hmac.compare_digest(got.encode(), ("Bearer " + token).encode())

        def do_GET(self):
            if not self._authorized():
                return self._send(401, {"error": "unauthorized"})
            if self.path != "/v1/health":
                return self._send(404, {"error": "not found"})
            self._send(200, {"status": "ok", "router": engine.router_name, "checkpoint": engine.checkpoint, "package": engine.package,
                             "device": engine.device, "loadMs": engine.load_ms, "versions": engine.versions})

        def do_POST(self):
            if not self._authorized():
                return self._send(401, {"error": "unauthorized"})
            if self.path != "/v1/route":
                return self._send(404, {"error": "not found"})
            try:
                n = int(self.headers.get("Content-Length", "0"))
            except ValueError:
                n = -1
            if n <= 0 or n > MAX_BODY:
                return self._send(413, {"error": "body size"})
            try:
                req = json.loads(self.rfile.read(n))
                rid, router, text, threshold = req["requestId"], req["router"], req["text"], req["threshold"]
                if set(req) != {"requestId", "router", "text", "threshold"}:
                    raise ValueError("unexpected fields")
                if not isinstance(rid, str) or not 0 < len(rid) <= 64 or not isinstance(text, str) or not text or len(text.encode()) > MAX_TEXT:
                    raise ValueError("bad requestId/text")
                if isinstance(threshold, bool) or not isinstance(threshold, (int, float)) or not 0 <= threshold <= 1:
                    raise ValueError("bad threshold")
                if router != engine.router_name:
                    raise ValueError("router not loaded")
            except (ValueError, KeyError, TypeError, json.JSONDecodeError) as e:
                return self._send(400, {"error": str(e)})
            try:
                score, choice, ms = engine.route(text, float(threshold))
            except Exception as e:  # never crash the server on one input
                return self._send(500, {"error": type(e).__name__})
            self._send(200, {"requestId": rid, "router": engine.router_name, "score": score, "choice": choice, "threshold": threshold,
                             "checkpoint": engine.checkpoint, "package": engine.package, "device": engine.device, "latencyMs": ms, "cached": False})

    return Handler


def main():
    args = parse_args()
    loopback = args.host in ("127.0.0.1", "::1", "localhost")
    token = os.environ.get(args.token_env, "") if args.token_env else ""
    if not loopback and not token:
        raise SystemExit("refusing to bind a non-loopback address without --token-env")
    isolate_network(args.allow_download)
    engine = Engine(args)
    if args.self_test:
        out = {"router": engine.router_name, "checkpoint": engine.checkpoint, "package": engine.package, "device": engine.device, "loadMs": engine.load_ms, "versions": engine.versions, "samples": []}
        for text in ["Fix the typo in README.md", "Redesign the distributed lock manager to remove the race between lease renewal and failover, then prove it with tests."]:
            score, choice, ms = engine.route(text, 0.5)
            out["samples"].append({"chars": len(text), "score": round(score, 6), "choice@0.5": choice, "ms": ms})
        print(json.dumps(out, indent=2))
        return
    srv = ThreadingHTTPServer((args.host, args.port), make_handler(engine, token))
    print(json.dumps({"listening": f"{args.host}:{args.port}", "router": engine.router_name, "device": engine.device, "loadMs": engine.load_ms}), flush=True)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
