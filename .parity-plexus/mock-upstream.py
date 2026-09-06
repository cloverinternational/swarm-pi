import json, http.server, time
LOG = "/tmp/plexus-ab/mock-upstream.jsonl"
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        n = int(self.headers.get("content-length", 0)); body = self.rfile.read(n)
        try: j = json.loads(body)
        except Exception: j = {"_raw": body[:200].decode("utf8", "replace")}
        with open(LOG, "a") as f: f.write(json.dumps({"path": self.path, "body": j}) + "\n")
        resp = {"id": "resp_mock", "object": "response", "created_at": int(time.time()), "status": "completed",
                "model": j.get("model", "m"),
                "output": [{"type": "message", "id": "msg_1", "role": "assistant", "status": "completed",
                            "content": [{"type": "output_text", "text": "MOCK-OK", "annotations": []}]}],
                "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
        if j.get("stream"):
            self.send_response(200); self.send_header("content-type", "text/event-stream"); self.end_headers()
            def ev(t, d): self.wfile.write(("event: %s\ndata: %s\n\n" % (t, json.dumps(d))).encode())
            ev("response.created", {"type": "response.created", "response": dict(resp, status="in_progress", output=[])})
            ev("response.output_text.delta", {"type": "response.output_text.delta", "item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": "MOCK-OK"})
            ev("response.completed", {"type": "response.completed", "response": resp})
        else:
            b = json.dumps(resp).encode(); self.send_response(200); self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(b))); self.end_headers(); self.wfile.write(b)
http.server.ThreadingHTTPServer(("127.0.0.1", 4999), H).serve_forever()
