"""Protocol tests for the sidecar HTTP layer with a stub engine (no torch)."""
import json
import threading
import unittest
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer

import sidecar


class StubEngine:
    router_name, checkpoint, package, device, load_ms, versions = "bert", "ckpt", "routellm==0.2.0", "cpu", 1, {}

    def route(self, text, threshold):
        score = 0.7
        return score, "strong" if score >= threshold else "weak", 1


class SidecarHTTPTest(unittest.TestCase):
    def serve(self, token=""):
        srv = ThreadingHTTPServer(("127.0.0.1", 0), sidecar.make_handler(StubEngine(), token))
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.shutdown)
        return f"http://127.0.0.1:{srv.server_address[1]}"

    def post(self, url, body, headers=None):
        req = urllib.request.Request(url + "/v1/route", data=json.dumps(body).encode(), headers={"Content-Type": "application/json", **(headers or {})})
        try:
            with urllib.request.urlopen(req) as r:
                return r.status, json.loads(r.read())
        except urllib.error.HTTPError as e:
            return e.code, json.loads(e.read())

    def test_route_ok(self):
        code, body = self.post(self.serve(), {"requestId": "r1", "router": "bert", "text": "x", "threshold": 0.5})
        self.assertEqual(code, 200)
        self.assertEqual((body["requestId"], body["choice"], body["checkpoint"]), ("r1", "strong", "ckpt"))

    def test_rejects_bad_input(self):
        url = self.serve()
        for bad in [{"requestId": "r", "router": "mf", "text": "x", "threshold": 0.5},
                    {"requestId": "r", "router": "bert", "text": "x", "threshold": 2},
                    {"requestId": "r", "router": "bert", "text": "x", "threshold": True},
                    {"requestId": "r", "router": "bert", "text": "", "threshold": 0.5},
                    {"requestId": "r", "router": "bert", "text": "x" * 9000, "threshold": 0.5},
                    {"requestId": "r", "router": "bert", "text": "x", "threshold": 0.5, "strong": "gpt"}]:
            self.assertEqual(self.post(url, bad)[0], 400, bad)

    def test_token(self):
        url = self.serve(token="s3cret")
        self.assertEqual(self.post(url, {"requestId": "r", "router": "bert", "text": "x", "threshold": 0.5})[0], 401)
        self.assertEqual(self.post(url, {"requestId": "r", "router": "bert", "text": "x", "threshold": 0.5}, {"Authorization": "Bearer s3cret"})[0], 200)

    def test_network_isolation_env(self):
        import os
        sidecar.isolate_network(False)
        self.assertEqual(os.environ["OPENAI_BASE_URL"], "http://127.0.0.1:9/v1")
        self.assertEqual(os.environ["HF_HUB_OFFLINE"], "1")


if __name__ == "__main__":
    unittest.main()
