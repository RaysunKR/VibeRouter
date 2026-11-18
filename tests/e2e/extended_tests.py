#!/usr/bin/env python3
import json
import os
import threading
import time
import traceback
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import requests
from anthropic import Anthropic
from openai import OpenAI


TINY_PNG = ("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk"
            "+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")


class RepeatSearchHandler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length).decode("utf-8", errors="replace")
        if self.path != "/v1/chat/completions":
            self.send_response(404)
            self.end_headers()
            return
        if "max_uses_exceeded" in body:
            message = {"role": "assistant", "content": "repeat upstream complete"}
            finish = "stop"
        else:
            message = {"role": "assistant", "content": None, "tool_calls": [{
                "id": "call_repeat", "type": "function", "function": {
                    "name": "web_search", "arguments": '{"query":"repeat search","count":1}'
                }
            }]}
            finish = "tool_calls"
        payload = json.dumps({
            "id": "chatcmpl-repeat", "object": "chat.completion", "created": int(time.time()),
            "model": "real-repeat", "choices": [{"index": 0, "message": message,
                                                  "finish_reason": finish}],
            "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
        }).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


def response_text(response):
    try:
        data = response.json()
    except ValueError:
        return response.text
    if "output_text" in data:
        return data["output_text"]
    if "choices" in data and data["choices"]:
        message = data["choices"][0].get("message", {})
        return message.get("content") or ""
    if "content" in data:
        return "".join(block.get("text", "") for block in data["content"]
                       if isinstance(block, dict) and block.get("type") == "text")
    return response.text


def response_output_text(response):
    data = response.json()
    if data.get("output_text"):
        return data["output_text"]
    return "".join(content.get("text", "") for item in data.get("output", [])
                   if item.get("type") == "message"
                   for content in item.get("content", [])
                   if content.get("type") == "output_text")


def request_body(record):
    body = record.get("body")
    if isinstance(body, str):
        try:
            return json.loads(body)
        except ValueError:
            return {}
    return body if isinstance(body, dict) else {}


class ExtendedTests:
    def __init__(self, router, mock, key, check, skip, config_path=None, restart_router=None):
        self.router = router.rstrip("/")
        self.mock = mock.rstrip("/")
        self.key = key
        self.check = check
        self.skip = skip
        self.config_path = config_path
        self.restart_router = restart_router
        self.prefix = f"e2e-{os.getpid()}-{int(time.time())}"
        self.created_groups = set()
        self.created_models = set()
        self.created_providers = set()
        self.created_keys = set()
        login = requests.post(self.router + "/api/login", timeout=10,
                              json={"username": "admin", "password": "admin"})
        login.raise_for_status()
        self.token = login.json()["token"]
        self.admin_headers = {"Authorization": f"Bearer {self.token}"}
        self.client_headers = {"Authorization": f"Bearer {self.key}"}
        self.oai = OpenAI(base_url=self.router + "/v1", api_key=self.key, timeout=30)
        self.ant = Anthropic(base_url=self.router, api_key=self.key, timeout=30)

    def name(self, suffix):
        return f"{self.prefix}-{suffix}"

    def api(self, method, path, **kwargs):
        kwargs.setdefault("timeout", 20)
        return requests.request(method, self.router + path, headers=self.admin_headers, **kwargs)

    def client(self, path, payload, key=None, headers=None):
        request_headers = dict(self.client_headers if key is None else
                               {"Authorization": f"Bearer {key}"})
        if headers:
            request_headers.update(headers)
        return requests.post(self.router + path, headers=request_headers, json=payload, timeout=45)

    def chat(self, model, text="extended e2e", key=None, headers=None):
        return self.client("/v1/chat/completions", {
            "model": model, "messages": [{"role": "user", "content": text}]
        }, key=key, headers=headers)

    def must_ok(self, response, action):
        if response.status_code != 200:
            raise RuntimeError(f"{action}: HTTP {response.status_code}: {response.text[:500]}")
        return response

    def create_provider(self, suffix, **overrides):
        provider_id = self.name(suffix)
        payload = {"id": provider_id, "name": provider_id, "protocol": "chat_completions",
                   "base_url": self.mock, "api_key": provider_id + "-key"}
        payload.update(overrides)
        self.must_ok(self.api("POST", "/api/providers", json=payload), "create provider")
        self.created_providers.add(provider_id)
        return payload

    def create_model(self, suffix, provider, upstream=None, **overrides):
        model = self.name(suffix)
        payload = {"name": model, "provider": provider, "upstream_model": upstream or model,
                   "multimodal": True, "long_context": True, "max_context_tokens": 1000000}
        payload.update(overrides)
        self.must_ok(self.api("POST", "/api/models", json=payload), "create model")
        self.created_models.add(model)
        return payload

    def create_group(self, suffix, members, mode="balance", **overrides):
        group = self.name(suffix)
        payload = {"name": group, "mode": mode, "members": members}
        payload.update(overrides)
        self.must_ok(self.api("POST", "/api/groups", json=payload), "create group")
        self.created_groups.add(group)
        return payload

    def update_model(self, model):
        self.must_ok(self.api("PUT", "/api/models/" + model["name"], json=model), "update model")

    def update_group(self, group):
        self.must_ok(self.api("PUT", "/api/groups/" + group["name"], json=group), "update group")

    def mock_reset(self):
        response = requests.post(self.mock + "/__mock/reset", timeout=10)
        if response.status_code != 200:
            raise RuntimeError(f"mock reset unavailable: HTTP {response.status_code}: {response.text[:200]}")

    def mock_requests(self):
        response = requests.get(self.mock + "/__mock/requests", timeout=10)
        if response.status_code != 200:
            raise RuntimeError(f"mock request log unavailable: HTTP {response.status_code}: {response.text[:200]}")
        return response.json().get("requests", [])

    def config(self):
        return self.must_ok(self.api("GET", "/api/config"), "get config").json()

    def settings(self, **changes):
        return self.must_ok(self.api("PUT", "/api/settings", json=changes), "update settings")

    def run(self):
        scenarios = [
            ("G. 关键跨协议流式组合", self.protocol_streams),
            ("H. 多账号轮询与供应商 CRUD", self.accounts_and_providers),
            ("I. 故障转移与错误语义", self.failover),
            ("J. 模型、分组与 Key 生命周期", self.model_group_key_lifecycle),
            ("K. 能力过滤与智能调度", self.capabilities_and_smart),
            ("L. 预设刷新与同步", self.presets),
            ("M. 四家 Web 工具适配", self.web_providers),
            ("N. MCP 与 Hosted 工具控制", self.mcp_and_hosted_controls),
            ("O. 日志过滤与错误字段", self.log_filters),
            ("P. 配置写盘与重启恢复", self.persistence),
        ]
        for title, scenario in scenarios:
            print(f"\n--- {title} ---")
            try:
                scenario()
            except Exception as exc:
                self.check(f"{title}: 场景执行", False, f"{type(exc).__name__}: {exc}")
                traceback.print_exc()
        self.cleanup()

    def protocol_streams(self):
        text = "".join(chunk.choices[0].delta.content or ""
                       for chunk in self.oai.chat.completions.create(
                           model="m-resp", stream=True,
                           messages=[{"role": "user", "content": "chat responses stream"}])
                       if chunk.choices)
        self.check("chat->responses stream", "MOCK[responses]" in text, text[:300])

        text = "".join(getattr(event, "delta", "") for event in self.oai.responses.create(
            model="m-chat", input="responses chat stream", stream=True)
            if getattr(event, "type", "") == "response.output_text.delta")
        self.check("responses->chat stream", "MOCK[chat]" in text, text[:300])

        text = "".join(getattr(event, "delta", "") for event in self.oai.responses.create(
            model="m-anth", input="responses anthropic stream", stream=True)
            if getattr(event, "type", "") == "response.output_text.delta")
        self.check("responses->anthropic stream", "MOCK[anthropic]" in text, text[:300])

        with self.ant.messages.stream(model="m-chat", max_tokens=100,
                                      messages=[{"role": "user", "content": "anthropic chat stream"}]) as stream:
            text = stream.get_final_text()
        self.check("anthropic->chat stream", "MOCK[chat]" in text, text[:300])

        result = self.ant.messages.create(model="m-resp", max_tokens=100,
                                          messages=[{"role": "user", "content": "anthropic responses unary"}])
        self.check("anthropic->responses unary",
                   "MOCK[responses]" in result.content[0].text, str(result.content))

    def accounts_and_providers(self):
        pa = self.create_provider("account-a", api_key=self.name("credential-a"))
        pb = self.create_provider("account-b", api_key=self.name("credential-b"))
        ma = self.create_model("account-a", pa["id"], "real-chat")
        mb = self.create_model("account-b", pb["id"], "real-chat")
        group = self.create_group("round-robin", [
            {"model": ma["name"], "priority": 1}, {"model": mb["name"], "priority": 1}
        ])
        self.mock_reset()
        responses = [self.chat(group["name"], f"round robin {index}") for index in range(4)]
        self.check("同优先级四账号轮询均成功", all(r.status_code == 200 for r in responses),
                   str([(r.status_code, r.text[:100]) for r in responses]))
        records = [record for record in self.mock_requests()
                   if record.get("path") == "/v1/chat/completions"]
        expected_auth = ["Bearer " + pa["api_key"], "Bearer " + pb["api_key"]] * 2
        actual_auth = [record.get("authorization") for record in records[-4:]]
        self.check("同 provider 多账号真实 Authorization 轮询", actual_auth == expected_auth,
                   f"want={expected_auth}, got={actual_auth}")

        updated = dict(pa, name=self.name("account-a-renamed"))
        self.must_ok(self.api("PUT", "/api/providers/" + pa["id"], json=updated), "update provider")
        stored = next(p for p in self.config()["providers"] if p["id"] == pa["id"])
        self.check("供应商更新热生效", stored.get("name") == updated["name"], str(stored))
        duplicate = dict(pb, id=self.name("duplicate"), name=self.name("duplicate"))
        response = self.api("POST", "/api/providers", json=duplicate)
        self.check("重复供应商凭证返回 409", response.status_code == 409, response.text[:300])
        response = self.api("DELETE", "/api/providers/" + pa["id"])
        self.check("供应商被模型引用时禁止删除", response.status_code == 409, response.text[:300])

        self.must_ok(self.api("DELETE", "/api/groups/" + group["name"]), "delete group")
        self.created_groups.discard(group["name"])
        for model in (ma, mb):
            self.must_ok(self.api("DELETE", "/api/models/" + model["name"]), "delete model")
            self.created_models.discard(model["name"])
        self.must_ok(self.api("DELETE", "/api/providers/" + pa["id"]), "delete provider")
        self.created_providers.discard(pa["id"])
        self.check("供应商删除后从配置消失",
                   all(p["id"] != pa["id"] for p in self.config()["providers"]), pa["id"])

    def failover(self):
        provider = self.create_provider("failover")
        healthy = "m-chat"
        cases = [("500", "fail500"), ("429", "fail429"), ("403", "fail403"),
                 ("408", "fail408")]
        for status, trigger in cases:
            broken = self.create_model("broken-" + status, provider["id"], "real-" + trigger)
            group = self.create_group("failover-" + status, [
                {"model": broken["name"], "priority": 1}, {"model": healthy, "priority": 2}
            ])
            self.mock_reset()
            response = self.chat(group["name"], "failover " + status)
            self.check(f"上游 {status} 故障转移", response.status_code == 200
                       and "model=real-chat" in response_text(response),
                       f"HTTP {response.status_code}: {response.text[:300]}")
            records = [r for r in self.mock_requests() if r.get("path") == "/v1/chat/completions"]
            models = [request_body(r).get("model") for r in records]
            self.check(f"上游 {status} 实际尝试顺序", models == [broken["upstream_model"], "real-chat"],
                       str(models))

        broken402 = self.create_model("broken-402", provider["id"], "real-broke", api_key="badkey")
        group402 = self.create_group("failover-402", [
            {"model": broken402["name"], "priority": 1}, {"model": healthy, "priority": 2}
        ])
        response = self.chat(group402["name"], "failover 402")
        self.check("上游 402 故障转移", response.status_code == 200
                   and "model=real-chat" in response_text(response),
                   f"HTTP {response.status_code}: {response.text[:300]}")

        network = self.create_model("broken-network", provider["id"], "real-network",
                                    base_url="http://127.0.0.1:1")
        network_group = self.create_group("failover-network", [
            {"model": network["name"], "priority": 1}, {"model": healthy, "priority": 2}
        ])
        response = self.chat(network_group["name"], "network failover")
        self.check("网络连接失败故障转移", response.status_code == 200
                   and "model=real-chat" in response_text(response),
                   f"HTTP {response.status_code}: {response.text[:300]}")

        bad400 = self.create_model("broken-400", provider["id"], "real-fail400")
        group400 = self.create_group("no-failover-400", [
            {"model": bad400["name"], "priority": 1}, {"model": healthy, "priority": 2}
        ])
        self.mock_reset()
        response = self.chat(group400["name"], "client error")
        self.check("上游 400 原样返回且不转移", response.status_code == 400,
                   f"HTTP {response.status_code}: {response.text[:300]}")
        models = [request_body(r).get("model") for r in self.mock_requests()
                  if r.get("path") == "/v1/chat/completions"]
        self.check("上游 400 只尝试一次", models == [bad400["upstream_model"]], str(models))

        bad500 = self.create_model("all-fail-500", provider["id"], "real-fail500")
        bad429 = self.create_model("all-fail-429", provider["id"], "real-fail429")
        all_fail = self.create_group("all-fail", [
            {"model": bad500["name"], "priority": 1}, {"model": bad429["name"], "priority": 2}
        ])
        response = self.chat(all_fail["name"], "all fail")
        self.check("全部上游失败返回 502", response.status_code == 502
                   and "all upstreams failed" in response.text,
                   f"HTTP {response.status_code}: {response.text[:300]}")

    def model_group_key_lifecycle(self):
        provider = self.create_provider("override-provider", base_url="http://127.0.0.1:1",
                                        api_key=self.name("provider-key"))
        model = self.create_model("override-model", provider["id"], "real-override",
                                  protocol="responses", base_url=self.mock,
                                  api_key=self.name("model-key"))
        self.mock_reset()
        response = self.chat(model["name"], "model overrides")
        self.check("模型 protocol/base_url 覆盖真实生效", response.status_code == 200
                   and "MOCK[responses]" in response_text(response),
                   f"HTTP {response.status_code}: {response.text[:300]}")
        records = [r for r in self.mock_requests() if r.get("path") == "/v1/responses"]
        self.check("模型 API Key 覆盖真实 Authorization", len(records) == 1
                   and records[0].get("authorization") == "Bearer " + model["api_key"],
                   str([r.get("authorization") for r in records]))

        model["upstream_model"] = "real-override-updated"
        self.update_model(model)
        response = self.chat(model["name"], "model update")
        self.check("模型更新改变上游模型", response.status_code == 200
                   and "model=real-override-updated" in response_text(response), response.text[:300])

        group = self.create_group("crud-group", [
            {"model": "m-chat", "priority": 1}, {"model": model["name"], "priority": 2}
        ])
        response = self.chat(group["name"], "initial group")
        self.check("新建分组按初始优先级路由", response.status_code == 200
                   and "model=real-chat" in response_text(response), response.text[:300])
        group["members"] = [{"model": model["name"], "priority": 1},
                            {"model": "m-chat", "priority": 2}]
        self.update_group(group)
        response = self.chat(group["name"], "updated group")
        self.check("分组更新改变路由优先级", response.status_code == 200
                   and "model=real-override-updated" in response_text(response), response.text[:300])
        response = self.api("DELETE", "/api/models/" + model["name"])
        self.check("模型被分组引用时禁止删除", response.status_code == 409, response.text[:300])
        self.must_ok(self.api("DELETE", "/api/groups/" + group["name"]), "delete group")
        self.created_groups.discard(group["name"])
        response = self.chat(group["name"], "deleted group")
        self.check("分组删除后路由变为 404", response.status_code == 404, response.text[:300])
        self.must_ok(self.api("DELETE", "/api/models/" + model["name"]), "delete model")
        self.created_models.discard(model["name"])
        listed = requests.get(self.router + "/v1/models", headers=self.client_headers, timeout=10).json()
        self.check("模型和分组删除后从 /v1/models 消失",
                   all(item["id"] not in {model["name"], group["name"]} for item in listed["data"]),
                   str([item["id"] for item in listed["data"] if self.prefix in item["id"]]))

        created = self.must_ok(self.api("POST", "/api/keys", json={"name": self.name("key-before")}),
                               "create key").json()
        self.created_keys.add(created["id"])
        key_id, key_value = created["id"], created["key"]
        renamed = self.name("key-after")
        self.must_ok(self.api("PUT", "/api/keys/" + key_id,
                              json={"name": renamed, "enabled": True}), "rename key")
        response = self.chat("m-chat", "renamed key", key=key_value)
        self.check("Key 改名后仍可使用", response.status_code == 200, response.text[:300])
        self.must_ok(self.api("PUT", "/api/keys/" + key_id,
                              json={"name": renamed, "enabled": False}), "disable key")
        response = self.chat("m-chat", "disabled key", key=key_value)
        self.check("Key 禁用后返回 401", response.status_code == 401, response.text[:300])
        self.must_ok(self.api("PUT", "/api/keys/" + key_id,
                              json={"name": renamed, "enabled": True}), "enable key")
        response = self.chat("m-chat", "enabled key", key=key_value)
        self.check("Key 重新启用后恢复", response.status_code == 200, response.text[:300])
        self.must_ok(self.api("DELETE", "/api/keys/" + key_id), "delete key")
        self.created_keys.discard(key_id)
        response = self.chat("m-chat", "deleted key", key=key_value)
        self.check("Key 删除后返回 401", response.status_code == 401, response.text[:300])

    def capabilities_and_smart(self):
        original = self.config()["routing"]
        routing = dict(original, long_context_threshold=20, dispatcher_timeout_sec=1,
                       strategy="round_robin")
        self.settings(routing=routing)
        self.check("路由 API 设置热生效", self.config()["routing"] == routing,
                   str(self.config()["routing"]))
        try:
            provider = self.create_provider("capabilities")
            short = self.create_model("short-context", provider["id"], "real-short",
                                      long_context=False, multimodal=False, max_context_tokens=1000)
            tiny = self.create_model("tiny-context", provider["id"], "real-tiny",
                                     long_context=True, multimodal=True, max_context_tokens=10)
            capable = self.create_model("long-vision", provider["id"], "real-long-vision",
                                        long_context=True, multimodal=True, max_context_tokens=5000)
            text_only = self.create_model("text-only", provider["id"], "real-text-only",
                                          long_context=True, multimodal=False, max_context_tokens=5000)
            long_group = self.create_group("long-context", [
                {"model": tiny["name"], "priority": 1}, {"model": short["name"], "priority": 2},
                {"model": capable["name"], "priority": 3}
            ])
            long_text = "long context " * 20
            response = self.chat(short["name"], long_text)
            self.check("direct 模型拒绝长上下文", response.status_code == 502
                       and "long context" in response.text, response.text[:300])
            response = self.chat(tiny["name"], long_text)
            self.check("max_context_tokens 限制 direct 模型", response.status_code == 502
                       and "context limit" in response.text, response.text[:300])
            self.mock_reset()
            response = self.chat(long_group["name"], long_text)
            self.check("长上下文与 max_context_tokens 联合过滤", response.status_code == 200
                       and "model=real-long-vision" in response_text(response), response.text[:300])
            models = [request_body(r).get("model") for r in self.mock_requests()
                      if r.get("path") == "/v1/chat/completions"]
            self.check("能力不足模型未发送真实上游请求", models == ["real-long-vision"], str(models))

            vision_group = self.create_group("multimodal", [
                {"model": text_only["name"], "priority": 1}, {"model": capable["name"], "priority": 2}
            ])
            content = [{"type": "text", "text": "look at image"},
                       {"type": "image_url", "image_url": {"url": f"data:image/png;base64,{TINY_PNG}"}}]
            response = self.client("/v1/chat/completions", {"model": text_only["name"],
                                   "messages": [{"role": "user", "content": content}]})
            self.check("direct 模型拒绝多模态", response.status_code == 502
                       and "multimodal" in response.text, response.text[:300])
            response = self.client("/v1/chat/completions", {"model": vision_group["name"],
                                   "messages": [{"role": "user", "content": content}]})
            self.check("分组多模态过滤选择视觉模型", response.status_code == 200
                       and "[image seen]" in response_text(response)
                       and "model=real-long-vision" in response_text(response), response.text[:300])

            smart = self.create_group("forced-smart", [], mode="smart", dispatcher_model="m-chat",
                                      advanced=[{"model": "m-anth", "priority": 1}],
                                      basic=[{"model": "m-chat", "priority": 1}])
            response = self.chat(smart["name"], "force basic",
                                 headers={"X-VibeRouter-Tier": "basic"})
            self.check("智能调度 header 强制 basic", response.status_code == 200
                       and "MOCK[chat]" in response_text(response), response.text[:300])

            failed = self.create_group("dispatcher-failure", [], mode="smart", dispatcher_model="m-broke",
                                       advanced=[{"model": "m-anth", "priority": 1}],
                                       basic=[{"model": "m-chat", "priority": 1}])
            response = self.chat(failed["name"], "dispatcher fails")
            self.check("调度器请求失败降级 basic", response.status_code == 200
                       and "MOCK[chat]" in response_text(response), response.text[:300])
            logs = self.api("GET", "/api/logs", params={"model": failed["name"], "limit": 10}).json()
            self.check("调度器失败记录 error 字段", bool(logs)
                       and "402" in logs[0].get("dispatcher_error", ""), str(logs[:1]))

            codeblock = self.create_model("codeblock-dispatcher", provider["id"], "real-codeblock")
            code_group = self.create_group("dispatcher-codeblock", [], mode="smart",
                                           dispatcher_model=codeblock["name"],
                                           advanced=[{"model": "m-anth", "priority": 1}],
                                           basic=[{"model": "m-chat", "priority": 1}])
            response = self.chat(code_group["name"], "codeblock-json advanced")
            self.check("调度器代码块 JSON 正常解析", response.status_code == 200
                       and "MOCK[anthropic]" in response_text(response), response.text[:300])

            slow = self.create_model("slow-dispatcher", provider["id"], "real-slow")
            slow_group = self.create_group("dispatcher-slow", [], mode="smart",
                                           dispatcher_model=slow["name"],
                                           advanced=[{"model": "m-anth", "priority": 1}],
                                           basic=[{"model": "m-chat", "priority": 1}])
            started = time.monotonic()
            response = self.chat(slow_group["name"], "dispatcher timeout")
            elapsed = time.monotonic() - started
            self.check("调度器超时降级 basic", response.status_code == 200 and elapsed < 8
                       and "MOCK[chat]" in response_text(response),
                       f"elapsed={elapsed:.2f}s HTTP {response.status_code}: {response.text[:300]}")
        finally:
            self.settings(routing=original)

    def select_preset(self):
        response = requests.get(self.mock + "/api.json", timeout=10)
        self.must_ok(response, "get mock models.dev catalog")
        catalog = response.json()
        supported = {"@ai-sdk/openai-compatible": "chat_completions",
                     "@ai-sdk/openai": "responses", "@ai-sdk/anthropic": "anthropic"}
        candidates = []
        for key, provider in catalog.items():
            if provider.get("npm") not in supported or not provider.get("api", "").startswith(self.mock):
                continue
            for model_key, model in provider.get("models", {}).items():
                if model.get("modalities", {}).get("input") is not None and \
                        model.get("limit", {}).get("context") is not None:
                    candidates.append((key, provider, model_key, model))
        if not candidates:
            raise RuntimeError("mock /api.json has no local preset with modality and context metadata")
        key, provider, model_key, model = candidates[0]
        preset_id = provider.get("id") or key
        protocol = supported[provider["npm"]]
        base_url = provider["api"].rstrip("/")
        for suffix in ("/chat/completions", "/responses", "/messages", "/v1"):
            if base_url.endswith(suffix):
                base_url = base_url[:-len(suffix)]
        upstream = model.get("id") or model_key
        multimodal = any(item == "image" or item.startswith("image/")
                         for item in model["modalities"]["input"])
        return {"id": preset_id, "protocol": protocol, "base_url": base_url,
                "upstream": upstream, "multimodal": multimodal,
                "max_context_tokens": model["limit"]["context"]}

    def presets(self):
        selected = self.select_preset()
        presets = self.must_ok(self.api("GET", "/api/presets"), "list presets").json()
        self.check("预设目录来自本地 models.dev", any(p.get("id") == selected["id"] for p in presets),
                   selected["id"])
        self.mock_reset()
        refreshed = self.must_ok(self.api("POST", "/api/presets/refresh"), "refresh presets").json()
        self.check("预设 refresh 返回本地目录", any(p.get("id") == selected["id"] for p in refreshed),
                   selected["id"])
        refreshes = [r for r in self.mock_requests() if r.get("path") == "/api.json"]
        self.check("预设 refresh 强制请求 models.dev", len(refreshes) == 1, str(refreshes))

        provider = self.create_provider("preset-bound", preset_id=selected["id"],
                                        protocol="chat_completions", base_url=self.mock,
                                        api_key=self.name("preset-secret"), name=self.name("preset-name"))
        model = self.create_model("preset-model", provider["id"], selected["upstream"],
                                  multimodal=not selected["multimodal"], max_context_tokens=1,
                                  long_context=False)
        group = self.create_group("preset-group", [{"model": model["name"], "priority": 1}])
        self.mock_reset()
        result = self.must_ok(self.api("POST", f"/api/providers/{provider['id']}/sync-preset"),
                              "sync preset").json()
        self.check("sync-preset 更新模型能力元数据", result.get("models_updated") == 1
                   and model["name"] not in (result.get("models_skipped") or []), str(result))
        current = self.config()
        stored_provider = next(p for p in current["providers"] if p["id"] == provider["id"])
        stored_model = next(m for m in current["models"] if m["name"] == model["name"])
        self.check("sync-preset 更新 protocol/base_url",
                   stored_provider.get("protocol") == selected["protocol"]
                   and stored_provider.get("base_url") == selected["base_url"], str(stored_provider))
        expected_long = selected["max_context_tokens"] > self.config()["routing"]["long_context_threshold"]
        self.check("sync-preset 更新 multimodal/max_context_tokens/long_context",
                   stored_model.get("multimodal") == selected["multimodal"]
                   and stored_model.get("max_context_tokens") == selected["max_context_tokens"]
                   and stored_model.get("long_context") == expected_long, str(stored_model))
        self.check("sync-preset 保留 Key/名称/绑定/模型与分组",
                   stored_provider.get("api_key") == provider["api_key"]
                   and stored_provider.get("name") == provider["name"]
                   and stored_provider.get("preset_id") == selected["id"]
                   and stored_model.get("name") == model["name"]
                   and stored_model.get("provider") == provider["id"]
                   and stored_model.get("upstream_model") == selected["upstream"]
                   and any(g["name"] == group["name"] for g in current["groups"]), str(stored_provider))
        sync_requests = [r for r in self.mock_requests() if r.get("path") == "/api.json"]
        self.check("sync-preset 强制刷新本地目录", len(sync_requests) == 1, str(sync_requests))

    def web_settings(self, search="deepseek", fetch="deepseek", enabled=True, mcp=True,
                     timeout=5, max_calls=4, wrong_key=False):
        keys = {"bocha": "web-bocha-key", "kimi": "web-kimi-key",
                "deepseek": "web-key", "glm": "web-glm-key"}
        if wrong_key:
            keys["deepseek"] = "wrong-web-key"
        providers = [{"id": provider, "enabled": True, "api_key": keys[provider],
                      "base_url": self.mock}
                     for provider in ("bocha", "kimi", "deepseek", "glm")]
        return {"enabled": enabled, "mcp_enabled": mcp, "search_provider": search,
                "fetch_provider": fetch, "timeout_sec": timeout, "max_calls": max_calls,
                "providers": providers}

    def mcp_call(self, method, params=None, request_id=100):
        headers = dict(self.client_headers)
        headers.update({"Accept": "application/json, text/event-stream",
                        "Content-Type": "application/json", "MCP-Protocol-Version": "2025-11-25"})
        return requests.post(self.router + "/mcp", headers=headers, timeout=20,
                             json={"jsonrpc": "2.0", "id": request_id,
                                   "method": method, "params": params or {}})

    def web_providers(self):
        original = self.config()["web_tools"]
        fetch_urls = {"kimi": "https://example.com/kimi/fetch", "deepseek": "https://example.com/article",
                      "glm": "https://example.com/glm/fetch"}
        try:
            for provider in ("bocha", "kimi", "deepseek", "glm"):
                self.settings(web_tools=self.web_settings(search=provider))
                result = self.mcp_call("tools/call", {"name": "web_search", "arguments": {
                    "query": f"{provider} adapter", "count": 1, "include_content": True
                }}).json().get("result", {})
                structured = result.get("structuredContent", {})
                self.check(f"Web {provider} 搜索适配", not result.get("isError")
                           and structured.get("provider") == provider
                           and structured.get("results"), str(result)[:500])
                if provider == "bocha":
                    self.settings(web_tools=self.web_settings(search="bocha", fetch="bocha"))
                    result = self.mcp_call("tools/call", {"name": "web_fetch", "arguments": {
                        "url": "https://example.com/article"
                    }}).json().get("result", {})
                    self.check("Bocha 明确拒绝抓取", result.get("isError") is True
                               and "does not support" in str(result.get("content")), str(result)[:500])
                    continue
                self.settings(web_tools=self.web_settings(fetch=provider))
                result = self.mcp_call("tools/call", {"name": "web_fetch", "arguments": {
                    "url": "https://example.com/article"
                }}).json().get("result", {})
                structured = result.get("structuredContent", {})
                self.check(f"Web {provider} 抓取适配", not result.get("isError")
                           and structured.get("provider") == provider
                           and structured.get("url") == fetch_urls[provider]
                           and structured.get("content"), str(result)[:500])
            self.settings(web_tools=self.web_settings(search="glm"))
            self.check("Web provider 设置修改后立即可用", self.config()["web_tools"]["search_provider"] == "glm",
                       str(self.config()["web_tools"]))
        finally:
            self.settings(web_tools=original)

    def mcp_and_hosted_controls(self):
        original = self.config()["web_tools"]
        try:
            self.settings(web_tools=self.web_settings(enabled=False, mcp=True))
            response = self.mcp_call("ping")
            self.check("MCP ping 独立可用", response.status_code == 200
                       and response.json().get("result") == {}, response.text[:300])
            headers = dict(self.client_headers)
            headers.update({"Accept": "application/json, text/event-stream",
                            "Content-Type": "application/json"})
            response = requests.post(self.router + "/mcp", headers=headers, timeout=10,
                                     json={"jsonrpc": "2.0", "method": "notifications/initialized"})
            self.check("MCP initialized 通知返回 202", response.status_code == 202,
                       f"HTTP {response.status_code}: {response.text[:200]}")
            response = self.mcp_call("tools/call", {"name": "web_search", "arguments": {
                "query": "mcp independent", "count": 1
            }})
            result = response.json().get("result", {})
            self.check("MCP 在 hosted Web 关闭时仍执行搜索", not result.get("isError")
                       and result.get("structuredContent", {}).get("provider") == "deepseek", response.text[:500])

            self.settings(web_tools=self.web_settings(enabled=True, mcp=False))
            response = self.mcp_call("ping")
            self.check("MCP 独立关闭返回 404", response.status_code == 404, response.text[:300])
            response = self.client("/v1/responses", {"model": "m-chat", "input": "hosted independent",
                                   "tools": [{"type": "web_search"}]})
            self.check("MCP 关闭不影响 hosted Web", response.status_code == 200
                       and "MOCK[chat]" in response_output_text(response), response.text[:500])

            self.settings(web_tools=self.web_settings())
            result = self.mcp_call("tools/call", {"name": "web_fetch", "arguments": {
                "url": "https://example.com/article", "allowed_domains": ["other.example"]
            }}).json().get("result", {})
            self.check("MCP fetch 域名白名单错误", result.get("isError") is True
                       and "not allowed" in str(result.get("content")), str(result)[:500])
            result = self.mcp_call("tools/call", {"name": "web_search", "arguments": {
                "query": "domain filter", "blocked_domains": ["example.com"], "count": 1
            }}).json().get("result", {})
            self.check("MCP search 域名黑名单过滤", not result.get("isError")
                       and result.get("structuredContent", {}).get("results") == [], str(result)[:500])
            self.settings(web_tools=self.web_settings(wrong_key=True))
            result = self.mcp_call("tools/call", {"name": "web_search", "arguments": {
                "query": "upstream error", "count": 1
            }}).json().get("result", {})
            self.check("MCP 上游错误结构化返回且不泄露凭证", result.get("isError") is True
                       and "wrong-web-key" not in str(result), str(result)[:500])

            self.settings(web_tools=self.web_settings())
            response = self.client("/v1/messages", {
                "model": "m-chat", "max_tokens": 500,
                "messages": [{"role": "user", "content": "hosted domain error"}],
                "tools": [{"type": "web_fetch_20250910", "name": "web_fetch", "max_uses": 1,
                           "allowed_domains": ["other.example"]}]
            })
            self.check("hosted max_uses 与域名配置传递到执行层", response.status_code == 200
                       and "web_fetch_tool_result_error" in response.text
                       and "unavailable" in response.text, response.text[:700])
            self.hosted_max_uses()
        finally:
            self.settings(web_tools=original)

    def hosted_max_uses(self):
        if not self.restart_router:
            self.skip("hosted max_uses 重复上游场景（仅本地进程模式）")
            return
        server = ThreadingHTTPServer(("127.0.0.1", 0), RepeatSearchHandler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            base_url = f"http://127.0.0.1:{server.server_port}"
            provider = self.create_provider("repeat-search", base_url=base_url)
            model = self.create_model("repeat-search", provider["id"], "real-repeat")
            response = self.client("/v1/messages", {
                "model": model["name"], "max_tokens": 500,
                "messages": [{"role": "user", "content": "hosted repeat search"}],
                "tools": [{"type": "web_search_20250305", "name": "web_search", "max_uses": 1}]
            })
            self.check("hosted 第二次调用触发 max_uses_exceeded", response.status_code == 200
                       and "max_uses_exceeded" in response.text
                       and "repeat upstream complete" in response.text, response.text[:1000])
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)

    def log_filters(self):
        provider = self.create_provider("logs")
        good = self.create_model("logs-success", provider["id"], "real-chat")
        bad = self.create_model("logs-error", provider["id"], "real-fail400")
        success = self.chat(good["name"], "log success")
        failure = self.chat(bad["name"], "log failure")
        self.check("日志过滤前置请求状态", success.status_code == 200 and failure.status_code == 400,
                   f"success={success.status_code}, failure={failure.status_code}")
        model_logs = self.api("GET", "/api/logs", params={"model": good["name"], "limit": 50}).json()
        self.check("日志 model 过滤", bool(model_logs)
                   and all(good["name"] in (entry.get("request_model", "") + entry.get("route_model", ""))
                           for entry in model_logs), str(model_logs[:2]))
        status_logs = self.api("GET", "/api/logs", params={"status": 400, "model": bad["name"],
                                                           "limit": 50}).json()
        self.check("日志 status 过滤及错误字段", bool(status_logs)
                   and all(entry.get("status") == 400 for entry in status_logs)
                   and "error" in status_logs[0] and status_logs[0]["error"], str(status_logs[:2]))
        limited = self.api("GET", "/api/logs", params={"limit": 1}).json()
        self.check("日志 limit 过滤", isinstance(limited, list) and len(limited) == 1, str(limited))
        key_logs = self.api("GET", "/api/logs", params={"key": "e2e", "model": good["name"],
                                                        "limit": 20}).json()
        self.check("日志 key/model 组合过滤", bool(key_logs)
                   and all(entry.get("key_name") == "e2e" for entry in key_logs), str(key_logs[:2]))

    def persistence(self):
        if not self.config_path or not self.restart_router:
            self.skip("配置写盘与重启恢复（仅本地进程模式）")
            return
        provider = self.create_provider("persistent", api_key=self.name("persistent-key"))
        model = self.create_model("persistent-model", provider["id"], "real-persistent")
        with open(self.config_path, "r", encoding="utf-8") as handle:
            disk = handle.read()
        self.check("管理变更真实写入 config.yaml", provider["id"] in disk and model["name"] in disk
                   and provider["api_key"] in disk, self.config_path)
        self.restart_router()
        current = self.config()
        self.check("重启后恢复供应商和模型",
                   any(p["id"] == provider["id"] for p in current["providers"])
                   and any(m["name"] == model["name"] for m in current["models"]), self.config_path)
        response = self.chat(model["name"], "after restart")
        self.check("重启恢复的配置可直接路由", response.status_code == 200
                   and "model=real-persistent" in response_text(response), response.text[:300])

    def cleanup(self):
        failures = []
        for key_id in list(self.created_keys):
            response = self.api("DELETE", "/api/keys/" + key_id)
            if response.status_code not in (200, 404):
                failures.append(f"key {key_id}: {response.status_code} {response.text[:100]}")
        for group in sorted(self.created_groups):
            response = self.api("DELETE", "/api/groups/" + group)
            if response.status_code not in (200, 404):
                failures.append(f"group {group}: {response.status_code} {response.text[:100]}")
        for model in sorted(self.created_models):
            response = self.api("DELETE", "/api/models/" + model)
            if response.status_code not in (200, 404):
                failures.append(f"model {model}: {response.status_code} {response.text[:100]}")
        for provider in sorted(self.created_providers):
            response = self.api("DELETE", "/api/providers/" + provider)
            if response.status_code not in (200, 404):
                failures.append(f"provider {provider}: {response.status_code} {response.text[:100]}")
        self.check("扩展场景资源清理", not failures, "; ".join(failures))


def run_extended(router, mock, key, check, skip, config_path=None, restart_router=None):
    suite = ExtendedTests(router, mock, key, check, skip, config_path, restart_router)
    suite.run()
