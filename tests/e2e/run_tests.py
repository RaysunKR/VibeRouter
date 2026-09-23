#!/usr/bin/env python3
# VibeRouter SDK 兼容性端到端测试
# 用法: python run_tests.py <viberouter.exe> <mockupstream.exe>
import json
import os
import signal
import subprocess
import sys
import tempfile
import time

import requests
from openai import OpenAI
from anthropic import Anthropic

ROUTER = os.environ.get("VR_ROUTER", "http://127.0.0.1:18080")
MOCK = os.environ.get("VR_MOCK", "http://127.0.0.1:19000")
REMOTE = os.environ.get("VR_REMOTE") == "1"
KEY = "vr-e2ekey"

PASS, FAIL = [], []


def check(name, cond, detail=""):
    if cond:
        PASS.append(name)
        print(f"  PASS {name}")
    else:
        FAIL.append(name)
        print(f"  FAIL {name}  {detail}")


def wait_ready(url, timeout=10):
    t0 = time.time()
    while time.time() - t0 < timeout:
        try:
            requests.get(url, timeout=1)
            return True
        except Exception:
            time.sleep(0.2)
    return False


CONFIG = f"""
server: {{addr: "127.0.0.1:18080", disable_captcha: true}}
routing: {{long_context_threshold: 200000, dispatcher_timeout_sec: 5, strategy: round_robin}}
providers:
  - {{id: p-chat, name: MockChat, protocol: chat_completions, base_url: "{MOCK}", api_key: upkey, balance: {{type: deepseek}}}}
  - {{id: p-resp, name: MockResp, protocol: responses, base_url: "{MOCK}", api_key: upkey}}
  - {{id: p-anth, name: MockAnth, protocol: anthropic, base_url: "{MOCK}", api_key: upkey}}
  - {{id: p-broke, name: MockBroke, protocol: chat_completions, base_url: "{MOCK}", api_key: badkey}}
models:
  - {{name: m-chat, provider: p-chat, upstream_model: real-chat, multimodal: true, long_context: true, max_context_tokens: 1000000}}
  - {{name: m-resp, provider: p-resp, upstream_model: real-resp, multimodal: true, long_context: true, max_context_tokens: 1000000}}
  - {{name: m-anth, provider: p-anth, upstream_model: real-anth, multimodal: true, long_context: true, max_context_tokens: 1000000}}
  - {{name: m-broke, provider: p-broke, upstream_model: real-broke, multimodal: false, long_context: false, max_context_tokens: 1000}}
groups:
  - name: lb
    mode: balance
    members:
      - {{model: m-broke, priority: 1}}
      - {{model: m-chat, priority: 2}}
  - name: smart
    mode: smart
    dispatcher_model: m-chat
    advanced:
      - {{model: m-chat, priority: 1}}
    basic:
      - {{model: m-anth, priority: 1}}
keys:
  - {{id: k1, key: {KEY}, name: e2e, enabled: true, created_at: "2026-08-15T00:00:00Z"}}
logs: {{file: "logs/e2e.log"}}
"""

TINY_PNG = ("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk"
            "+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")


def main():
    if REMOTE:
        assert wait_ready(MOCK + "/user/balance"), "mock not ready"
        assert wait_ready(ROUTER + "/"), "router not ready"
        run_all()
        print(f"\n===== {len(PASS)} passed, {len(FAIL)} failed =====")
        if FAIL:
            print("failed:", *FAIL, sep="\n  - ")
            sys.exit(1)
        return

    router_exe, mock_exe = sys.argv[1], sys.argv[2]
    workdir = tempfile.mkdtemp(prefix="viberouter-e2e-")
    with open(os.path.join(workdir, "config.yaml"), "w", encoding="utf-8") as f:
        f.write(CONFIG)
    env_path = os.path.join(workdir, ".env")
    open(env_path, "w").write("")

    mock = subprocess.Popen([mock_exe, "-port", "19000"],
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    router = subprocess.Popen([router_exe, "-config", os.path.join(workdir, "config.yaml"),
                               "-env", env_path],
                              cwd=workdir, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        assert wait_ready(MOCK + "/user/balance"), "mock not ready"
        assert wait_ready(ROUTER + "/"), "router not ready"
        run_all()
    finally:
        for p in (router, mock):
            p.terminate()
            try:
                p.wait(timeout=5)
            except Exception:
                p.kill()

    print(f"\n===== {len(PASS)} passed, {len(FAIL)} failed =====")
    if FAIL:
        print("failed:", *FAIL, sep="\n  - ")
        sys.exit(1)


def run_all():
    oai = OpenAI(base_url=ROUTER + "/v1", api_key=KEY)
    ant = Anthropic(base_url=ROUTER, api_key=KEY)

    print("\n--- A. OpenAI chat.completions SDK ---")
    # A1 same-protocol non-stream
    r = oai.chat.completions.create(model="m-chat",
                                    messages=[{"role": "user", "content": "hello"}])
    t = r.choices[0].message.content
    check("chat->chat echo", "MOCK[chat]" in t and "model=real-chat" in t, t)
    check("chat->chat usage", r.usage and r.usage.prompt_tokens == 10, str(r.usage))
    check("chat->chat finish", r.choices[0].finish_reason == "stop")

    # A2 same-protocol stream
    text = "".join(c.choices[0].delta.content or ""
                   for c in oai.chat.completions.create(
                       model="m-chat", stream=True,
                       messages=[{"role": "user", "content": "hi"}]) if c.choices)
    check("chat->chat stream", "MOCK[chat]" in text, text[:100])

    # A3 cross to anthropic, non-stream + stream
    r = oai.chat.completions.create(model="m-anth",
                                    messages=[{"role": "user", "content": "cross"}])
    check("chat->anthropic echo", "MOCK[anthropic]" in r.choices[0].message.content,
          r.choices[0].message.content)
    text = "".join(c.choices[0].delta.content or ""
                   for c in oai.chat.completions.create(
                       model="m-anth", stream=True,
                       messages=[{"role": "user", "content": "cross"}]) if c.choices)
    check("chat->anthropic stream", "MOCK[anthropic]" in text, text[:100])

    # A4 cross to responses
    r = oai.chat.completions.create(model="m-resp",
                                    messages=[{"role": "user", "content": "cross"}])
    check("chat->responses echo", "MOCK[responses]" in r.choices[0].message.content,
          r.choices[0].message.content)

    # A5 tool call (chat client, anthropic upstream)
    r = oai.chat.completions.create(
        model="m-anth", messages=[{"role": "user", "content": "please toolcall now"}],
        tools=[{"type": "function", "function": {
            "name": "get_weather", "description": "w",
            "parameters": {"type": "object", "properties": {"city": {"type": "string"}}}}}])
    msg = r.choices[0].message
    check("chat tool_call from anthropic",
          r.choices[0].finish_reason == "tool_calls" and msg.tool_calls
          and msg.tool_calls[0].function.name == "get_weather"
          and json.loads(msg.tool_calls[0].function.arguments)["city"] == "beijing",
          str(msg))

    # A6 multimodal
    r = oai.chat.completions.create(model="m-anth", messages=[{"role": "user", "content": [
        {"type": "text", "text": "see this"},
        {"type": "image_url", "image_url": {"url": f"data:image/png;base64,{TINY_PNG}"}}]}])
    check("chat multimodal", "[image seen]" in r.choices[0].message.content,
          r.choices[0].message.content)

    # A7 multi-turn
    r = oai.chat.completions.create(model="m-chat", messages=[
        {"role": "system", "content": "you are helpful"},
        {"role": "user", "content": "first"},
        {"role": "assistant", "content": "second"},
        {"role": "user", "content": "third turn"}])
    check("chat multi-turn", "third turn" in r.choices[0].message.content,
          r.choices[0].message.content)

    print("\n--- B. OpenAI responses SDK ---")
    r = oai.responses.create(model="m-resp", input="hello resp")
    text = "".join(c.text for item in r.output if item.type == "message"
                   for c in item.content if c.type == "output_text")
    check("responses->responses echo", "MOCK[responses]" in text, text)
    check("responses usage", r.usage and r.usage.input_tokens == 10, str(r.usage))

    text = "".join(getattr(e, "delta", "") for e in oai.responses.create(
        model="m-resp", input="hello", stream=True)
        if getattr(e, "type", "") == "response.output_text.delta")
    check("responses stream", "MOCK[responses]" in text, text[:100])

    r = oai.responses.create(model="m-chat", input="cross to chat")
    text = "".join(c.text for item in r.output if item.type == "message"
                   for c in item.content if c.type == "output_text")
    check("responses->chat echo", "MOCK[chat]" in text, text)

    r = oai.responses.create(model="m-anth", input="cross to anthropic")
    text = "".join(c.text for item in r.output if item.type == "message"
                   for c in item.content if c.type == "output_text")
    check("responses->anthropic echo", "MOCK[anthropic]" in text, text)

    r = oai.responses.create(
        model="m-chat", input="please toolcall",
        tools=[{"type": "function", "name": "get_weather", "description": "w",
                "parameters": {"type": "object", "properties": {"city": {"type": "string"}}}}])
    fc = [i for i in r.output if i.type == "function_call"]
    check("responses tool_call from chat",
          fc and fc[0].name == "get_weather" and json.loads(fc[0].arguments)["city"] == "beijing",
          str(r.output))

    print("\n--- C. Anthropic SDK ---")
    r = ant.messages.create(model="m-anth", max_tokens=100,
                            messages=[{"role": "user", "content": "hello ant"}])
    check("anthropic->anthropic echo", "MOCK[anthropic]" in r.content[0].text, r.content[0].text)
    check("anthropic stop_reason", r.stop_reason == "end_turn", str(r.stop_reason))
    check("anthropic usage", r.usage.input_tokens == 10 and r.usage.output_tokens == 5,
          str(r.usage))

    with ant.messages.stream(model="m-anth", max_tokens=100,
                             messages=[{"role": "user", "content": "hi"}]) as s:
        text = s.get_final_text()
    check("anthropic stream", "MOCK[anthropic]" in text, text[:100])

    r = ant.messages.create(model="m-chat", max_tokens=100,
                            messages=[{"role": "user", "content": "cross"}])
    check("anthropic->chat echo", "MOCK[chat]" in r.content[0].text, r.content[0].text)

    with ant.messages.stream(model="m-resp", max_tokens=100,
                             messages=[{"role": "user", "content": "cross"}]) as s:
        text = s.get_final_text()
    check("anthropic->responses stream", "MOCK[responses]" in text, text[:100])

    r = ant.messages.create(
        model="m-chat", max_tokens=100,
        messages=[{"role": "user", "content": "please toolcall"}],
        tools=[{"name": "get_weather", "description": "w",
                "input_schema": {"type": "object", "properties": {"city": {"type": "string"}}}}])
    tu = [b for b in r.content if b.type == "tool_use"]
    check("anthropic tool_use from chat",
          r.stop_reason == "tool_use" and tu and tu[0].name == "get_weather"
          and tu[0].input["city"] == "beijing", str(r.content))

    r = ant.messages.create(model="m-chat", max_tokens=100, messages=[{"role": "user", "content": [
        {"type": "text", "text": "see"},
        {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": TINY_PNG}}]}])
    check("anthropic multimodal", "[image seen]" in r.content[0].text, r.content[0].text)

    print("\n--- D. 路由策略 ---")
    r = oai.chat.completions.create(model="lb", messages=[{"role": "user", "content": "x"}])
    t = r.choices[0].message.content
    check("负载均衡: 402 故障转移到健康模型", "model=real-chat" in t, t)

    r = oai.chat.completions.create(model="smart", messages=[{"role": "user", "content": "basic 问题"}])
    check("智能调度: basic → 普通组(anthropic)",
          "MOCK[anthropic]" in r.choices[0].message.content, r.choices[0].message.content)

    r = oai.chat.completions.create(model="smart",
                                    messages=[{"role": "user", "content": "多步推理 advanced"}])
    check("智能调度: advanced → 强组(chat)",
          "MOCK[chat]" in r.choices[0].message.content, r.choices[0].message.content)

    r = requests.post(ROUTER + "/v1/chat/completions",
                      headers={"Authorization": f"Bearer {KEY}", "X-VibeRouter-Tier": "advanced"},
                      json={"model": "smart", "messages": [{"role": "user", "content": "basic"}]})
    check("智能调度: header 强制 advanced",
          "MOCK[chat]" in r.json()["choices"][0]["message"]["content"], r.text[:200])

    print("\n--- E. 管理端 ---")
    tok = requests.post(ROUTER + "/api/login",
                        json={"username": "admin", "password": "admin"}).json()["token"]
    H = {"Authorization": f"Bearer {tok}"}

    bal = requests.post(ROUTER + "/api/providers/p-chat/balance", headers=H).json()
    check("余额查询", bal.get("ok") and abs(bal.get("remaining", 0) - 88.5) < 0.01
          and bal.get("unit") == "CNY", str(bal))

    k = requests.post(ROUTER + "/api/keys", headers=H, json={"name": "temp"}).json()
    check("创建 Key", k.get("key", "").startswith("vr-"), str(k))
    bad = requests.post(ROUTER + "/v1/chat/completions",
                        headers={"Authorization": f"Bearer {k['key']}"},
                        json={"model": "m-chat", "messages": [{"role": "user", "content": "hi"}]})
    check("新 Key 可用", bad.status_code == 200, bad.text[:100])
    requests.put(ROUTER + f"/api/keys/{k['id']}", headers=H, json={"name": "temp", "enabled": False})
    bad = requests.post(ROUTER + "/v1/chat/completions",
                        headers={"Authorization": f"Bearer {k['key']}"},
                        json={"model": "m-chat", "messages": [{"role": "user", "content": "hi"}]})
    check("禁用 Key 生效", bad.status_code == 401, str(bad.status_code))

    logs = requests.get(ROUTER + "/api/logs?limit=100", headers=H).json()
    lb_log = [l for l in logs if l.get("request_model") == "lb"]
    check("日志记录故障转移 attempts=2",
          lb_log and lb_log[0].get("attempts") == 2, str(lb_log[:1]))
    smart_log = [l for l in logs if l.get("request_model") == "smart" and l.get("tier")]
    check("日志记录 tier", bool(smart_log), str(logs[:1]))

    models = requests.get(ROUTER + "/v1/models", headers={"Authorization": f"Bearer {KEY}"}).json()
    ids = [m["id"] for m in models["data"]]
    check("/v1/models 列表", "m-chat" in ids and "lb" in ids and "smart" in ids, str(ids))

    # 设置：日志保存时长可配置并热生效
    r = requests.put(ROUTER + "/api/settings", headers=H,
                     json={"logs": {"file": "logs/e2e.log", "retention_days": 3}})
    check("设置: 修改日志保存时长", r.status_code == 200, r.text[:100])
    cfg = requests.get(ROUTER + "/api/config", headers=H).json()
    check("设置: retention_days 已持久化", cfg.get("logs", {}).get("retention_days") == 3,
          str(cfg.get("logs")))
    requests.put(ROUTER + "/api/settings", headers=H,
                 json={"logs": {"file": "logs/e2e.log", "retention_days": 7}})
    cfg = requests.get(ROUTER + "/api/config", headers=H).json()
    check("设置: 恢复为 7 天", cfg.get("logs", {}).get("retention_days") == 7)

    # key 过滤
    logs_k = requests.get(ROUTER + "/api/logs?key=e2e&limit=50", headers=H).json()
    check("日志: key 过滤", logs_k and all(l.get("key_name") == "e2e" for l in logs_k),
          str(logs_k[:1]))


if __name__ == "__main__":
    main()
