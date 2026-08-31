#!/usr/bin/env python3
"""Deterministic OpenAI-compatible workload for swarm-tui profiling.

Each user turn creates and completes a task and performs two real Read tool
calls against a long source file. The server never contacts an external model.
"""

from __future__ import annotations

import argparse
import json
import re
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


MODEL = "fake-profile-model"
LONG_FILE = "/workspace/swarm-sdk/swarm-tui/internal/chat/app_update.go"


def chunk(delta: dict[str, Any], finish_reason: str | None = None) -> dict[str, Any]:
    return {
        "id": "chatcmpl-swarm-profile",
        "object": "chat.completion.chunk",
        "created": 1,
        "model": MODEL,
        "choices": [
            {
                "index": 0,
                "delta": delta,
                "finish_reason": finish_reason,
            }
        ],
    }


def messages_since_last_user(messages: list[dict[str, Any]]) -> list[dict[str, Any]]:
    for index in range(len(messages) - 1, -1, -1):
        if messages[index].get("role") == "user":
            return messages[index + 1 :]
    return messages


def task_id_from(messages: list[dict[str, Any]]) -> str:
    for message in messages:
        if message.get("role") != "tool":
            continue
        match = re.search(r'"id"\s*:\s*"([^"]+)"', str(message.get("content", "")))
        if match:
            return match.group(1)
    return "1"


class FakeOpenAIHandler(BaseHTTPRequestHandler):
    server_version = "SwarmProfileFakeOpenAI/1.0"

    def log_message(self, format_string: str, *args: Any) -> None:
        print(format_string % args, flush=True)

    def send_json(self, value: dict[str, Any]) -> None:
        body = json.dumps(value).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        if self.path.rstrip("/") == "/v1/models":
            self.send_json(
                {
                    "object": "list",
                    "data": [{"id": MODEL, "object": "model", "owned_by": "local"}],
                }
            )
            return
        self.send_error(404)

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        if self.path.rstrip("/") != "/v1/chat/completions":
            self.send_error(404)
            return

        length = int(self.headers.get("Content-Length", "0"))
        request = json.loads(self.rfile.read(length))
        messages = request.get("messages", [])

        if not request.get("stream"):
            self.send_json(
                {
                    "id": "chatcmpl-profile-title",
                    "object": "chat.completion",
                    "model": MODEL,
                    "choices": [
                        {
                            "index": 0,
                            "message": {"role": "assistant", "content": "Profile run"},
                            "finish_reason": "stop",
                        }
                    ],
                    "usage": {
                        "prompt_tokens": 1,
                        "completion_tokens": 1,
                        "total_tokens": 2,
                    },
                }
            )
            return

        tools = {
            tool.get("function", {}).get("name")
            for tool in request.get("tools", [])
        }
        turn_messages = messages_since_last_user(messages)
        completed_tools = sum(
            message.get("role") == "tool" for message in turn_messages
        )
        user_turn = sum(message.get("role") == "user" for message in messages)

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()

        if completed_tools == 0 and "TaskManage" in tools:
            self.send_tool_call(
                completed_tools,
                "TaskManage",
                {
                    "operations": [
                        {
                            "key": f"profile-turn-{user_turn}",
                            "op": "create",
                            "subject": f"Profile fake-provider turn {user_turn}",
                            "description": "Exercise task updates and long Read rendering",
                            "status": "in_progress",
                        }
                    ]
                },
            )
        elif completed_tools in (1, 2) and "Read" in tools:
            self.send_tool_call(
                completed_tools,
                "Read",
                {"file_path": LONG_FILE, "max_size": 0},
            )
        elif completed_tools == 3 and "TaskManage" in tools:
            self.send_tool_call(
                completed_tools,
                "TaskManage",
                {
                    "operations": [
                        {
                            "key": f"complete-turn-{user_turn}",
                            "op": "update",
                            "taskId": task_id_from(turn_messages),
                            "status": "completed",
                        }
                    ]
                },
            )
        else:
            self.send_event(chunk({"role": "assistant", "reasoning_content": "Profiling deterministic render path. "}))
            for text in (
                "FAKE STREAM: task updates and two long Reads completed. ",
                f"FAKE COMPLETE turn={user_turn}",
            ):
                self.send_event(chunk({"content": text}))
                time.sleep(0.2)
            self.send_event(chunk({}, "stop"))
            self.send_event(
                {
                    "id": "chatcmpl-swarm-profile",
                    "object": "chat.completion.chunk",
                    "created": 1,
                    "model": MODEL,
                    "choices": [],
                    "usage": {
                        "prompt_tokens": 1000,
                        "completion_tokens": 20,
                        "total_tokens": 1020,
                    },
                }
            )

        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()

    def send_tool_call(
        self, index: int, name: str, arguments: dict[str, Any]
    ) -> None:
        self.send_event(
            chunk(
                {
                    "role": "assistant",
                    "tool_calls": [
                        {
                            "index": 0,
                            "id": f"profile-call-{index}",
                            "type": "function",
                            "function": {
                                "name": name,
                                "arguments": json.dumps(arguments),
                            },
                        }
                    ],
                }
            )
        )
        self.send_event(chunk({}, "tool_calls"))

    def send_event(self, value: dict[str, Any]) -> None:
        self.wfile.write(("data: " + json.dumps(value) + "\n\n").encode())
        self.wfile.flush()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=18080)
    args = parser.parse_args()
    server = ThreadingHTTPServer((args.host, args.port), FakeOpenAIHandler)
    print(f"fake OpenAI profile server listening on {args.host}:{args.port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
