"""本地 OSS 协议演示器：仅用于 SDK/指标闭环，不验证云端签名或持久性。"""

import hashlib
import http.server
import os
import sys
import socketserver
import tempfile
import urllib.parse
from pathlib import Path

BUCKET = "assets-emulated"
MAX_BODY = 1024 * 1024


class Server(http.server.HTTPServer):
    def server_bind(self):
        # 演示服务无需反向 DNS；避免隔离网络中 getfqdn 阻塞启动。
        socketserver.TCPServer.server_bind(self)
        self.server_name = "oss-emulator"
        self.server_port = self.server_address[1]


class Handler(http.server.BaseHTTPRequestHandler):
    # HTTPServer 单线程顺序处理；对象落临时目录，容器重启后即丢失。
    timeout = 10

    def object_path(self):
        path = urllib.parse.urlsplit(self.path).path
        if self.command == "GET" and path == "/healthz":
            return None
        if path.startswith("/" + BUCKET + "/"):
            key = path[len(BUCKET) + 2:]
        elif self.headers.get("Host", "").startswith(BUCKET + "."):
            key = path.lstrip("/")
        else:
            raise ValueError("unsupported bucket")
        if not key or len(key) > 1024:
            raise ValueError("invalid key")
        # 哈希命名隔离路径语义，用户对象键不能跳出临时目录。
        return self.server.root / hashlib.sha256(key.encode()).hexdigest()

    def respond(self, status, body=b""):
        self.send_response(status)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Content-Type", "text/plain")
        self.send_header("ETag", '"demo"')
        self.send_header("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
        self.send_header("x-oss-request-id", "local-demo")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def dispatch(self):
        try:
            path = self.object_path()
            if path is None:
                self.respond(200, b"ok\n")
            elif self.command == "PUT":
                # 仅接收有界的已知长度请求，拒绝 chunked 及截断上传。
                if self.headers.get("Transfer-Encoding"):
                    self.respond(400)
                    return
                length = int(self.headers.get("Content-Length", "-1"))
                if not 0 <= length <= MAX_BODY:
                    self.respond(413)
                    return
                payload = self.rfile.read(length)
                if len(payload) != length:
                    self.respond(400)
                    return
                path.write_bytes(payload)
                self.respond(200)
            elif self.command in ("GET", "HEAD"):
                if not path.exists():
                    self.respond(404)
                else:
                    self.respond(200, path.read_bytes())
            elif self.command == "DELETE":
                path.unlink(missing_ok=True)
                self.respond(204)
        except ValueError:
            self.respond(400)
        except OSError as error:
            self.log_error("dispatch | storage.failed | %s", type(error).__name__)
            self.close_connection = True

    do_GET = dispatch
    do_HEAD = dispatch
    do_PUT = dispatch
    do_DELETE = dispatch


def self_test():
    # 子进程运行单线程服务；测试驱动真实 HTTP 请求且退出后回收进程。
    import subprocess
    import time
    import urllib.error
    import urllib.request

    with tempfile.TemporaryDirectory() as directory:
        port_file = Path(directory) / "port"
        process = subprocess.Popen([sys.executable, __file__], env={**os.environ, "PORT": "0", "PORT_FILE": str(port_file)})
        try:
            deadline = time.monotonic() + 5
            while not port_file.exists():
                if process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError("emulator did not start")
                time.sleep(0.01)
            base = "http://127.0.0.1:" + port_file.read_text()
            url = base + "/" + BUCKET + "/demo.txt"
            for method, data in [("PUT", b"hello"), ("HEAD", None), ("GET", None), ("DELETE", None)]:
                with urllib.request.urlopen(urllib.request.Request(url, data=data, method=method), timeout=3) as response:
                    if method == "GET":
                        assert response.read() == b"hello"
            try:
                urllib.request.urlopen(url, timeout=3)
            except urllib.error.HTTPError as error:
                assert error.code == 404
            else:
                raise AssertionError("deleted object still exists")
            # 域名 endpoint 会采用虚拟 host；与 IP path style 指向相同 bucket。
            for method, data in [("PUT", b"virtual"), ("GET", None), ("DELETE", None)]:
                request = urllib.request.Request(base + "/virtual.txt", data=data, method=method,
                                                 headers={"Host": BUCKET + ".oss-emulator:9000"})
                with urllib.request.urlopen(request, timeout=3) as response:
                    if method == "GET":
                        assert response.read() == b"virtual"
            for path, length, status in [("/wrong/key", "0", 400), ("/" + BUCKET + "/large", str(MAX_BODY + 1), 413)]:
                request = urllib.request.Request(base + path, data=b"", method="PUT", headers={"Content-Length": length})
                try:
                    urllib.request.urlopen(request, timeout=3)
                except urllib.error.HTTPError as error:
                    assert error.code == status
                else:
                    raise AssertionError("invalid request accepted")
            print("OSS emulator self-test passed")
        finally:
            process.terminate()
            process.wait(timeout=5)


if __name__ == "__main__":
    if "--self-test" in sys.argv:
        self_test()
    else:
        with tempfile.TemporaryDirectory(prefix="oss-demo-") as directory:
            with Server(("0.0.0.0", int(os.environ.get("PORT", "9000"))), Handler) as server:
                server.root = Path(directory)
                if os.environ.get("PORT_FILE"):
                    Path(os.environ["PORT_FILE"]).write_text(str(server.server_port))
                server.serve_forever()
