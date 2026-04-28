#!/usr/bin/env python3
"""
联调 mp_sched 与「业务 App」：

1) 回调 mock（业务侧 HTTP 服务）
   在 worker/controller 配置的 callback.url 指向本服务，例如：
     callback:
       enable: true
       url: "http://host.docker.internal:8787/callback"   # worker 能访问到的地址
       method: POST

   Worker/Controller 会对该 URL 发 POST，Content-Type: application/json，body 形如：
     {
       "event": "<pending|processing|admitted|running|succeeded|failed|stopped|timeout>",
       "task_id": "<uuid>",
       "status": "<与 model.Task 的 status 一致>",
       "task": { ... Go model.Task 的 JSON ... }
     }
   注意：Go 里 Task.Business 字段带 json:"-"，因此 "task" 里通常**不会**含 business 原文；
   需要 business 时请用下面的 GET /tasks/{id} 查详情。

2) 查任务状态（真实调 mp_sched，一般不需要 mock）
   GET {controller_base}/api/sched/v1/tasks/{task_id}
   响应：{ "ok": true, "data": { "task_id", "business", "status", ... } }

3) 提交任务（对应你给的 docker run）
   POST {controller_base}/api/sched/v1/tasks
   卷挂载 host_path -> /data 必须在 worker 的 YAML docker.mounts 里先配好，
   API 本体不负责传 -v；下面 JSON 只表达镜像、GPU、command 等。
"""

from __future__ import annotations

import argparse
import json
import threading
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Any


# 等价于：
# docker run --rm -v .../tab_dataset:/data --gpus all uni_dock_zcc:v4 \
#   python /app/uni_dock_screen.py --receptor /data/receptor.pdb ...
#
# 请将 TAB_DATASET_HOST_PATH 在 worker 配置里 [[docker.mounts]] 绑到 /data。
TAB_DATASET_HOST_PATH = "/mnt/disk_1_4t/zcc/ChemToolAgent-main/02_docking_agent/test_templ/agents/docking/core/UniDock_block/tab_dataset"

UNI_DOCK_SUBMIT_BODY: dict[str, Any] = {
    "operation": "start",
    "task_class": "slow",
    "provider": "docker",
    "image": "uni_dock_zcc:v4",
    "res_cpu": "4",
    "res_memory": "16Gi",
    "res_gpu": "all",
    "business": {
        "command": [
            "python",
            "/app/uni_dock_screen.py",
            "--receptor",
            "/data/receptor.pdb",
            "--ligands",
            "/data/ref_lig.sdf",
            "--box_center",
            "110",
            "60",
            "10",
            "--box_size",
            "20",
            "20",
            "20",
            "--dir",
            "./uni_results2",
        ],
        # 若镜像默认工作目录不合适，可解开：
        # "workdir": "/app",
    },
}


def _http_json(method: str, url: str, body: Any | None = None, timeout: float = 60.0) -> Any:
    data = None
    headers = {"Accept": "application/json"}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8")
            return json.loads(raw) if raw else None
    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {e.code} {e.reason}: {err_body}") from e


def submit_task(controller_base: str, body: dict[str, Any] | None = None) -> dict[str, Any]:
    base = controller_base.rstrip("/")
    return _http_json("POST", f"{base}/api/sched/v1/tasks", body or UNI_DOCK_SUBMIT_BODY)


def get_task(controller_base: str, task_id: str) -> dict[str, Any]:
    base = controller_base.rstrip("/")
    return _http_json("GET", f"{base}/api/sched/v1/tasks/{task_id}")


class _CallbackHandler(BaseHTTPRequestHandler):
    log_lock = threading.Lock()

    def log_message(self, fmt: str, *args: Any) -> None:
        # 仍输出到 stderr，格式与默认一致
        super().log_message(fmt, *args)

    def _read_json_body(self) -> Any:
        n = int(self.headers.get("Content-Length", "0") or 0)
        raw = self.rfile.read(n) if n else b""
        if not raw:
            return None
        return json.loads(raw.decode("utf-8"))

    def do_POST(self) -> None:
        if self.path != "/callback" and self.path.rstrip("/") != "/callback":
            self.send_error(404, "use POST /callback")
            return
        try:
            payload = self._read_json_body()
        except json.JSONDecodeError:
            self.send_error(400, "invalid json")
            return

        line = json.dumps(payload, ensure_ascii=False, indent=2)
        with self.log_lock:
            print("\n=== mp_sched callback POST ===")
            print(line)
            print("=== end ===\n")

        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"ok":true}')

    def do_GET(self) -> None:
        if self.path in ("/healthz", "/healthz/"):
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"ok":true}')
            return
        self.send_error(404)


def run_callback_server(host: str, port: int) -> None:
    server = HTTPServer((host, port), _CallbackHandler)
    print(f"Mock callback listening on http://{host}:{port}/callback (GET /healthz)")
    server.serve_forever()


def main() -> None:
    p = argparse.ArgumentParser(description="mock callback + submit/poll mp_sched")
    sub = p.add_subparsers(dest="cmd", required=True)

    s_serve = sub.add_parser("serve", help="只起回调 mock 服务")
    s_serve.add_argument("--host", default="0.0.0.0")
    s_serve.add_argument("--port", type=int, default=8787)

    s_submit = sub.add_parser("submit", help="POST 提交 UniDock 示例任务")
    s_submit.add_argument(
        "--controller",
        default="http://127.0.0.1:8080",
        help="Controller HTTP 根（含端口），默认本机 8080",
    )
    s_submit.add_argument(
        "--print-body-only",
        action="store_true",
        help="只打印 JSON 请求体，不真正 POST",
    )

    s_poll = sub.add_parser("poll", help="GET 轮询任务状态")
    s_poll.add_argument("--controller", default="http://127.0.0.1:8080")
    s_poll.add_argument("task_id")
    s_poll.add_argument("--interval", type=float, default=3.0)
    s_poll.add_argument("--max", type=int, default=0, help="最多轮询次数，0=只查一次")

    s_print = sub.add_parser("print-json", help="打印示例 submit JSON（可复制 curl）")
    args = p.parse_args()

    if args.cmd == "serve":
        run_callback_server(args.host, args.port)
    elif args.cmd == "print-json":
        print(json.dumps(UNI_DOCK_SUBMIT_BODY, ensure_ascii=False, indent=2))
        print(
            "\n# 提醒：host 目录挂载需在 worker configs 中配置，例如：",
            file=__import__("sys").stderr,
        )
        print(
            f"#   host_path = {TAB_DATASET_HOST_PATH!r} -> mount_path = \"/data\"",
            file=__import__("sys").stderr,
        )
    elif args.cmd == "submit":
        if args.print_body_only:
            print(json.dumps(UNI_DOCK_SUBMIT_BODY, ensure_ascii=False, indent=2))
            return
        out = submit_task(args.controller)
        print(json.dumps(out, ensure_ascii=False, indent=2))
    elif args.cmd == "poll":
        import time

        n = 0
        while True:
            n += 1
            out = get_task(args.controller, args.task_id)
            print(json.dumps(out, ensure_ascii=False, indent=2))
            if args.max and n >= args.max:
                break
            if args.max == 0:
                break
            time.sleep(args.interval)


if __name__ == "__main__":
    main()
