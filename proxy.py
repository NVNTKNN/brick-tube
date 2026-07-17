#!/usr/bin/env python3
# http->https streaming proxy: serves plain http to tplayerdemo, fetches the
# https googlevideo target with TLS + Range forwarding (so mp4 seeking works).
import http.server, socketserver, urllib.request, urllib.error
TARGET = open('/tmp/target_url.txt').read().strip()
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        req = urllib.request.Request(TARGET, method='GET')
        rng = self.headers.get('Range')
        if rng: req.add_header('Range', rng)
        req.add_header('User-Agent', 'Mozilla/5.0')
        try:
            up = urllib.request.urlopen(req, timeout=25)
        except urllib.error.HTTPError as e:
            up = e
        self.send_response(getattr(up, 'status', 200) or 200)
        for h in ('Content-Type','Content-Length','Content-Range','Accept-Ranges'):
            v = up.headers.get(h)
            if v: self.send_header(h, v)
        self.end_headers()
        try:
            while True:
                chunk = up.read(65536)
                if not chunk: break
                self.wfile.write(chunk)
        except (BrokenPipeError, ConnectionResetError, OSError):
            pass
    def do_HEAD(self):
        self.do_GET()
    def log_message(self, *a): pass
class S(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
print("proxy on 0.0.0.0:8000 ->", TARGET[:80], "...")
S(('0.0.0.0', 8000), H).serve_forever()
