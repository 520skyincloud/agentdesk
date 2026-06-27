#!/usr/bin/env python3
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import http.client
import sys

UPSTREAM_HOST = "127.0.0.1"
UPSTREAM_PORT = 8083


class ProxyHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        self.forward()

    def do_POST(self):
        self.forward()

    def do_PUT(self):
        self.forward()

    def do_PATCH(self):
        self.forward()

    def do_DELETE(self):
        self.forward()

    def do_OPTIONS(self):
        self.forward()

    def forward(self):
        content_length = int(self.headers.get("Content-Length") or "0")
        body = self.rfile.read(content_length) if content_length > 0 else None
        headers = {
            key: value
            for key, value in self.headers.items()
            if key.lower() not in {"host", "connection", "proxy-connection", "keep-alive", "transfer-encoding"}
        }
        headers["Host"] = f"{UPSTREAM_HOST}:{UPSTREAM_PORT}"
        headers["X-Forwarded-Host"] = self.headers.get("Host", "")
        headers["X-Forwarded-Proto"] = "http"
        conn = http.client.HTTPConnection(UPSTREAM_HOST, UPSTREAM_PORT, timeout=30)
        try:
            conn.request(self.command, self.path, body=body, headers=headers)
            resp = conn.getresponse()
            resp_body = resp.read()
            self.send_response(resp.status, resp.reason)
            for key, value in resp.getheaders():
                if key.lower() in {"connection", "transfer-encoding"}:
                    continue
                self.send_header(key, value)
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(resp_body)
        except Exception as exc:
            message = str(exc).encode("utf-8", errors="replace")
            self.send_response(502)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Content-Length", str(len(message)))
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(message)
        finally:
            conn.close()

    def log_message(self, fmt, *args):
        sys.stderr.write("%s - - [%s] %s\n" % (self.client_address[0], self.log_date_time_string(), fmt % args))


if __name__ == "__main__":
    server = ThreadingHTTPServer(("127.0.0.1", 18083), ProxyHandler)
    print("agent-desk frp proxy listening on 127.0.0.1:18083 -> 127.0.0.1:8083", flush=True)
    server.serve_forever()
