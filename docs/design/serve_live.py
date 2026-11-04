"""手稿实况预览:静态文件 + /api/* 代理到本机 yogod(127.0.0.1:25264)。

页面在同源下自动从「模拟帧」切换到「实时帧」:
帧全部由 yogod 的 Go 引擎计算,页面只染色,JS 一帧都不算。

用法:
    yogod serve                     # 先让引擎跑起来
    python docs/design/serve_live.py [端口=8792]
    # 打开 http://127.0.0.1:8792/mockup.html
"""
import http.client
import http.server
import os
import pathlib
import socketserver
import sys

ROOT = pathlib.Path(__file__).resolve().parent
BACKEND = ("127.0.0.1", int(os.environ.get("YOGO_PORT", "25264")))


class H(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **kw):
        super().__init__(*a, directory=str(ROOT), **kw)

    def do_GET(self):
        if self.path.startswith("/api/"):
            return self.proxy()
        super().do_GET()

    def do_POST(self):
        self.proxy()

    def proxy(self):
        body = None
        if self.command == "POST":
            n = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(n) if n else b"{}"
        c = http.client.HTTPConnection(*BACKEND, timeout=15)
        try:
            c.request(self.command, self.path, body=body, headers={
                "Content-Type": self.headers.get("Content-Type") or "application/json"})
            r = c.getresponse()
            self.send_response(r.status)
            sse = (r.getheader("Content-Type") or "").startswith("text/event-stream")
            for k, v in r.getheaders():
                if k.lower() in ("transfer-encoding", "connection", "content-length"):
                    continue
                self.send_header(k, v)
            self.end_headers()
            if sse:
                while True:  # SSE 逐行透传,帧到了就推给页面
                    line = r.readline()
                    if not line:
                        break
                    self.wfile.write(line)
                    self.wfile.flush()
            else:
                self.wfile.write(r.read())
        except (BrokenPipeError, ConnectionResetError):
            pass
        finally:
            c.close()

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8792
    socketserver.ThreadingTCPServer.allow_reuse_address = True
    with socketserver.ThreadingTCPServer(("", port), H) as srv:
        print(f"手稿实况预览 → http://127.0.0.1:{port}/mockup.html  (/api → yogod :25264)")
        srv.serve_forever()
