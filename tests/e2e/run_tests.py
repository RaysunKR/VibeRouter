#!/usr/bin/env python3
# VibeRouter SDK 兼容性端到端测试
# 用法: python run_tests.py <viberouter.exe> <mockupstream.exe>
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time

import requests
from openai import OpenAI
from anthropic import Anthropic

from extended_tests import run_extended

ROUTER_PORT = int(os.environ.get("VR_ROUTER_PORT", "18080"))
MOCK_PORT = int(os.environ.get("VR_MOCK_PORT", "19000"))
ROUTER = os.environ.get("VR_ROUTER", f"http://127.0.0.1:{ROUTER_PORT}")
MOCK = os.environ.get("VR_MOCK", f"http://127.0.0.1:{MOCK_PORT}")
REMOTE = os.environ.get("VR_REMOTE") == "1"
UI_PORT = int(os.environ.get("VR_UI_PORT", "18181"))
KEY = "vr-e2ekey"
ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))

PASS, FAIL, SKIPPED = [], [], []
PROCESS_LOGS = []


def check(name, cond, detail=""):
    if cond:
        PASS.append(name)
        print(f"  PASS {name}")
    else:
        FAIL.append(name)
        print(f"  FAIL {name}  {detail}")


def skip(name):
    SKIPPED.append(name)
    print(f"  SKIP {name}")


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
server: {{addr: "127.0.0.1:{ROUTER_PORT}", disable_captcha: true}}
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
  - name: smart-basic-only
    mode: smart
    dispatcher_model: m-broke
    advanced: []
    basic:
      - {{model: m-anth, priority: 1}}
  - name: smart-vision
    mode: smart
    dispatcher_model: m-broke
    advanced:
      - {{model: m-chat, priority: 1}}
    basic:
      - {{model: m-broke, priority: 1}}
web_tools:
  enabled: true
  mcp_enabled: true
  search_provider: deepseek
  fetch_provider: deepseek
  timeout_sec: 5
  max_calls: 4
  providers:
    - {{id: deepseek, enabled: true, api_key: web-key, base_url: "{MOCK}"}}
keys:
  - {{id: k1, key: {KEY}, name: e2e, enabled: true, created_at: "2026-08-15T00:00:00Z"}}
logs: {{file: "logs/e2e.log"}}
"""

TINY_PNG = ("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk"
            "+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")


def write_file(path, content=""):
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(content)


def process_environment():
    return dict(os.environ, VR_MODELS_DEV_URL=MOCK + "/api.json")


def launch(name, args, cwd, log_path):
    os.makedirs(os.path.dirname(log_path), exist_ok=True)
    handle = open(log_path, "a", encoding="utf-8")
    if log_path not in PROCESS_LOGS:
        PROCESS_LOGS.append(log_path)
    try:
        process = subprocess.Popen(args, cwd=cwd, env=process_environment(),
                                   stdout=handle, stderr=subprocess.STDOUT)
    except Exception:
        handle.close()
        raise
    process.e2e_log_handle = handle
    print(f"  START {name}: pid={process.pid}, log={log_path}")
    return process


def stop_process(process):
    if process is None:
        return
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
    handle = getattr(process, "e2e_log_handle", None)
    if handle:
        handle.close()


def run_backend(config_path=None, restart_router=None):
    try:
        run_all()
    except Exception as exc:
        check("现有 52 项套件执行", False, f"{type(exc).__name__}: {exc}")
    try:
        run_extended(ROUTER, MOCK, KEY, check, skip,
                     config_path=config_path, restart_router=restart_router)
    except Exception as exc:
        check("扩展后端套件执行", False, f"{type(exc).__name__}: {exc}")


def playwright_command():
    if os.name == "nt":
        corepack = shutil.which("corepack.cmd") or shutil.which("corepack")
    else:
        corepack = shutil.which("corepack")
    if not corepack:
        raise RuntimeError("corepack/corepack.cmd not found")
    return [corepack, "pnpm", "exec", "playwright", "test"]


def run_playwright(base_url, log_path):
    os.makedirs(os.path.dirname(log_path), exist_ok=True)
    if log_path not in PROCESS_LOGS:
        PROCESS_LOGS.append(log_path)
    env = dict(os.environ, VR_E2E_BASE_URL=base_url, VR_E2E_MOCK_URL=MOCK,
               VR_MODELS_DEV_URL=MOCK + "/api.json")
    print(f"\n--- UI Playwright: {base_url} ---")
    print(f"  UI log: {log_path}")
    try:
        with open(log_path, "w", encoding="utf-8") as handle:
            result = subprocess.run(playwright_command(), cwd=os.path.join(ROOT, "web"), env=env,
                                    stdout=handle, stderr=subprocess.STDOUT, check=False)
        passed = result.returncode == 0
    except Exception as exc:
        with open(log_path, "a", encoding="utf-8") as handle:
            handle.write(f"\nrunner error: {type(exc).__name__}: {exc}\n")
        passed = False
    print(f"  UI {'PASS' if passed else 'FAIL'}")
    return passed


def print_summary(ui_passed):
    print(f"\n===== Backend: {len(PASS)} passed, {len(FAIL)} failed, "
          f"{len(SKIPPED)} skipped =====")
    if ui_passed is not None:
        print(f"===== UI: {'PASS' if ui_passed else 'FAIL'} =====")
    if FAIL:
        print("failed:", *FAIL, sep="\n  - ")
    if SKIPPED:
        print("skipped:", *SKIPPED, sep="\n  - ")
    if FAIL or ui_passed is False:
        print("process/test logs:", *PROCESS_LOGS, sep="\n  - ")


def main():
    ui_passed = None
    if REMOTE:
        if not wait_ready(MOCK + "/user/balance"):
            check("REMOTE mock ready", False, MOCK)
        elif not wait_ready(ROUTER + "/"):
            check("REMOTE router ready", False, ROUTER)
        else:
            run_backend()
        base_url = os.environ.get("VR_E2E_BASE_URL")
        if base_url:
            log_dir = tempfile.mkdtemp(prefix="viberouter-ui-e2e-")
            ui_passed = run_playwright(base_url, os.path.join(log_dir, "playwright.log"))
        else:
            skip("UI Playwright（REMOTE 未显式设置 VR_E2E_BASE_URL）")
        print_summary(ui_passed)
        return 1 if FAIL or ui_passed is False else 0

    if len(sys.argv) != 3:
        print("usage: python run_tests.py <viberouter.exe> <mockupstream.exe>", file=sys.stderr)
        return 2
    router_exe = os.path.abspath(sys.argv[1])
    mock_exe = os.path.abspath(sys.argv[2])
    workdir = tempfile.mkdtemp(prefix="viberouter-e2e-")
    log_dir = os.path.join(workdir, "process-logs")
    config_path = os.path.join(workdir, "config.yaml")
    env_path = os.path.join(workdir, ".env")
    write_file(config_path, CONFIG)
    write_file(env_path)
    print(f"E2E workdir: {workdir}")
    state = {"mock": None, "router": None, "ui": None}

    def start_router():
        return launch("router", [router_exe, "-config", config_path, "-env", env_path],
                      workdir, os.path.join(log_dir, "router.log"))

    def restart_router():
        stop_process(state["router"])
        state["router"] = start_router()
        if not wait_ready(ROUTER + "/"):
            raise RuntimeError("router did not become ready after restart; see " +
                               os.path.join(log_dir, "router.log"))

    try:
        state["mock"] = launch("mock", [mock_exe, "-port", str(MOCK_PORT)], workdir,
                               os.path.join(log_dir, "mock.log"))
        if not wait_ready(MOCK + "/user/balance"):
            raise RuntimeError("mock not ready; see " + os.path.join(log_dir, "mock.log"))
        state["router"] = start_router()
        if not wait_ready(ROUTER + "/"):
            raise RuntimeError("router not ready; see " + os.path.join(log_dir, "router.log"))
        run_backend(config_path=config_path, restart_router=restart_router)
    except Exception as exc:
        check("本地后端启动/执行", False, f"{type(exc).__name__}: {exc}")
    finally:
        stop_process(state["router"])
        state["router"] = None

    try:
        ui_workdir = os.path.join(workdir, "ui")
        os.makedirs(ui_workdir, exist_ok=True)
        ui_config = os.path.join(ui_workdir, "config.yaml")
        ui_env = os.path.join(ui_workdir, ".env")
        write_file(ui_config, f"""
server: {{addr: "127.0.0.1:{UI_PORT}", disable_captcha: true}}
routing: {{long_context_threshold: 200000, dispatcher_timeout_sec: 5, strategy: round_robin}}
providers: []
models: []
groups: []
web_tools:
  enabled: false
  mcp_enabled: false
  search_provider: deepseek
  fetch_provider: deepseek
  timeout_sec: 5
  max_calls: 4
  providers: []
keys:
  - {{id: ui-k1, key: {KEY}, name: e2e, enabled: true, created_at: "2026-08-15T00:00:00Z"}}
logs: {{file: "logs/ui-e2e.log", retention_days: 7}}
""")
        write_file(ui_env)
        state["ui"] = launch("ui-router", [router_exe, "-config", ui_config, "-env", ui_env],
                             ui_workdir, os.path.join(log_dir, "ui-router.log"))
        ui_url = f"http://127.0.0.1:{UI_PORT}"
        if not wait_ready(ui_url + "/"):
            raise RuntimeError("UI router not ready; see " + os.path.join(log_dir, "ui-router.log"))
        ui_passed = run_playwright(ui_url, os.path.join(log_dir, "playwright.log"))
    except Exception as exc:
        ui_passed = False
        print(f"  UI FAIL {type(exc).__name__}: {exc}")
    finally:
        stop_process(state["ui"])
        state["ui"] = None
        stop_process(state["mock"])
        state["mock"] = None

    print_summary(ui_passed)
    return 1 if FAIL or ui_passed is False else 0


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

    r = oai.chat.completions.create(model="smart-basic-only",
                                    messages=[{"role": "user", "content": "hello"}])
    check("智能调度: 仅普通组可用时直接路由",
          "MOCK[anthropic]" in r.choices[0].message.content, r.choices[0].message.content)

    r = oai.chat.completions.create(model="smart-vision", messages=[{"role": "user", "content": [
        {"type": "text", "text": "describe image"},
        {"type": "image_url", "image_url": {"url": f"data:image/png;base64,{TINY_PNG}"}}]}])
    check("智能调度: 多模态能力过滤直接选择强组",
          "MOCK[chat]" in r.choices[0].message.content, r.choices[0].message.content)

    r = oai.chat.completions.create(model="smart",
                                    messages=[{"role": "user", "content": "invalid-json"}])
    check("智能调度: 非法 JSON 降级普通组",
          "MOCK[anthropic]" in r.choices[0].message.content, r.choices[0].message.content)

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
    basic_only_log = [l for l in logs if l.get("request_model") == "smart-basic-only"]
    check("仅普通组可用时不调用调度器",
          basic_only_log and not basic_only_log[0].get("dispatcher_error"), str(basic_only_log[:1]))
    vision_log = [l for l in logs if l.get("request_model") == "smart-vision"]
    check("能力直达强组时不调用调度器",
          vision_log and vision_log[0].get("tier") == "advanced"
          and not vision_log[0].get("dispatcher_error"), str(vision_log[:1]))
    check("非法调度输出记录降级原因",
          any("unparseable" in l.get("dispatcher_error", "") for l in smart_log), str(smart_log[:1]))

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

    print("\n--- F. Web 工具与 MCP ---")
    r = oai.responses.create(model="m-chat", input="hosted search",
                             tools=[{"type": "web_search"}])
    check("Responses 原生 web_search",
          any(getattr(item, "type", "") == "web_search_call" for item in r.output)
          and "MOCK[chat]" in r.output_text, str(r.output))

    with oai.responses.stream(model="m-chat", input="hosted search stream",
                              tools=[{"type": "web_search"}]) as stream:
        r = stream.get_final_response()
    check("Responses web_search SSE",
          any(getattr(item, "type", "") == "web_search_call" for item in r.output)
          and "MOCK[chat]" in r.output_text, str(r.output))

    r = ant.messages.create(model="m-chat", max_tokens=500,
                            messages=[{"role": "user", "content": "hosted search"}],
                            tools=[{"type": "web_search_20250305", "name": "web_search"}])
    check("Anthropic 原生 web_search",
          any(getattr(block, "type", "") == "web_search_tool_result" for block in r.content), str(r.content))

    with ant.messages.stream(model="m-chat", max_tokens=500,
                             messages=[{"role": "user", "content": "hosted fetch stream"}],
                             tools=[{"type": "web_fetch_20250910", "name": "web_fetch"}]) as stream:
        r = stream.get_final_message()
    check("Anthropic web_fetch SSE",
          any(getattr(block, "type", "") == "web_fetch_tool_result" for block in r.content), str(r.content))

    mcp_headers = {"Authorization": f"Bearer {KEY}",
                   "Accept": "application/json, text/event-stream",
                   "Content-Type": "application/json"}
    r = requests.post(ROUTER + "/mcp", headers=mcp_headers,
                      json={"jsonrpc": "2.0", "id": 1, "method": "initialize",
                            "params": {"protocolVersion": "2025-11-25", "capabilities": {},
                                       "clientInfo": {"name": "e2e", "version": "1"}}})
    check("MCP initialize", r.status_code == 200
          and r.json().get("result", {}).get("serverInfo", {}).get("name") == "viberouter", r.text[:200])
    mcp_headers["MCP-Protocol-Version"] = "2025-11-25"
    r = requests.post(ROUTER + "/mcp", headers=mcp_headers,
                      json={"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}})
    check("MCP tools/list", {tool.get("name") for tool in r.json().get("result", {}).get("tools", [])}
          == {"web_search", "web_fetch"}, r.text[:200])
    r = requests.post(ROUTER + "/mcp", headers=mcp_headers,
                      json={"jsonrpc": "2.0", "id": 3, "method": "tools/call",
                            "params": {"name": "web_search", "arguments": {"query": "latest news", "count": 1}}})
    result = r.json().get("result", {})
    check("MCP 统一搜索调用", result.get("structuredContent", {}).get("provider") == "deepseek"
          and result.get("structuredContent", {}).get("results", [])[0].get("url") == "https://example.com/search",
          r.text[:300])


if __name__ == "__main__":
    sys.exit(main())
