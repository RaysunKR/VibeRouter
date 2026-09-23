#!/usr/bin/env python3
# 部署到 Linux 测试主机并做跨主机验收：
#   192.168.102.129 跑 mock 上游，192.168.102.128 跑 VibeRouter（跨网络调用）
# 本地 Windows 作为客户端跑 SDK 测试套件。
import os
import subprocess
import sys
import time

import paramiko

HOST_ROUTER = os.environ.get("VR_HOST_ROUTER", "192.168.102.128")
# 默认 mock 与 router 同机（单主机模式）；需要双主机时单独设置 VR_HOST_MOCK
HOST_MOCK = os.environ.get("VR_HOST_MOCK", HOST_ROUTER)
USER = os.environ.get("VR_USER", "root")
PASSWORD = os.environ.get("VR_PASS", "")
REMOTE_DIR = os.environ.get("VR_REMOTE_DIR", "/opt/viberouter-test")

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))

CONFIG = """
server: {addr: "0.0.0.0:18321", disable_captcha: true}
routing: {long_context_threshold: 200000, dispatcher_timeout_sec: 5, strategy: round_robin}
providers:
  - {id: p-chat, name: MockChat, protocol: chat_completions, base_url: "http://MOCKHOST:19322", api_key: upkey, balance: {type: deepseek}}
  - {id: p-resp, name: MockResp, protocol: responses, base_url: "http://MOCKHOST:19322", api_key: upkey}
  - {id: p-anth, name: MockAnth, protocol: anthropic, base_url: "http://MOCKHOST:19322", api_key: upkey}
  - {id: p-broke, name: MockBroke, protocol: chat_completions, base_url: "http://MOCKHOST:19322", api_key: badkey}
models:
  - {name: m-chat, provider: p-chat, upstream_model: real-chat, multimodal: true, long_context: true, max_context_tokens: 1000000}
  - {name: m-resp, provider: p-resp, upstream_model: real-resp, multimodal: true, long_context: true, max_context_tokens: 1000000}
  - {name: m-anth, provider: p-anth, upstream_model: real-anth, multimodal: true, long_context: true, max_context_tokens: 1000000}
  - {name: m-broke, provider: p-broke, upstream_model: real-broke, multimodal: false, long_context: false, max_context_tokens: 1000}
groups:
  - name: lb
    mode: balance
    members:
      - {model: m-broke, priority: 1}
      - {model: m-chat, priority: 2}
  - name: smart
    mode: smart
    dispatcher_model: m-chat
    advanced:
      - {model: m-chat, priority: 1}
    basic:
      - {model: m-anth, priority: 1}
keys:
  - {id: k1, key: vr-e2ekey, name: e2e, enabled: true, created_at: "2026-08-15T00:00:00Z"}
logs: {file: "logs/e2e.log"}
""".replace("MOCKHOST", HOST_MOCK)


def connect(host):
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, username=USER, password=PASSWORD, timeout=10)
    return c


def run(c, cmd, check=True):
    _, out, err = c.exec_command(cmd)
    code = out.channel.recv_exit_status()
    stdout = out.read().decode(errors="replace")
    stderr = err.read().decode(errors="replace")
    if check and code != 0:
        raise RuntimeError(f"{cmd!r} -> {code}\n{stdout}\n{stderr}")
    return code, stdout, stderr


def upload(c, local, remote_name):
    sftp = c.open_sftp()
    sftp.put(local, f"{REMOTE_DIR}/{remote_name}")
    sftp.close()
    run(c, f"chmod +x {REMOTE_DIR}/{remote_name}")


def main():
    cr = connect(HOST_ROUTER)
    cm = connect(HOST_MOCK)
    try:
        for c in (cr, cm):
            run(c, f"mkdir -p {REMOTE_DIR}/logs")
            # 清理残留进程（pkill -x 按进程名精确匹配；-f 会自匹配本 shell 导致清理中断）
            run(c, "pkill -x viberouter; pkill -x mockupstream; sleep 1", check=False)

        print("上传二进制...")
        upload(cr, os.path.join(ROOT, "dist", "viberouter-linux-amd64"), "viberouter")
        upload(cm, os.path.join(ROOT, "dist", "mockupstream-linux-amd64"), "mockupstream")

        print("写配置并启动...")
        sftp = cr.open_sftp()
        with sftp.open(f"{REMOTE_DIR}/config.yaml", "w") as f:
            f.write(CONFIG)
        with sftp.open(f"{REMOTE_DIR}/.env", "w") as f:
            f.write("")
        sftp.close()

        # (setsid ... &) 双层脱离：后台进程不得持有 SSH 通道管道，否则 exec 通道永不关闭
        run(cm, f"cd {REMOTE_DIR} && (setsid nohup ./mockupstream -port 19322 > mock.log 2>&1 < /dev/null &); sleep 1; pgrep -f mockupstream")
        run(cr, f"cd {REMOTE_DIR} && (setsid nohup ./viberouter -config config.yaml -env .env > router.log 2>&1 < /dev/null &); sleep 1; pgrep -f 'viberouter -config'")
        time.sleep(1)

        _, out, _ = run(cm, f"cat {REMOTE_DIR}/mock.log")
        print("mock:", out.strip())
        _, out, _ = run(cr, f"cat {REMOTE_DIR}/router.log")
        print("router:", out.strip())

        print("\n在本地跑 SDK 测试套件（客户端=Windows, 路由=128, 上游=129）...")
        env = dict(os.environ, VR_REMOTE="1",
                   VR_ROUTER=f"http://{HOST_ROUTER}:18321",
                   VR_MOCK=f"http://{HOST_MOCK}:19322")
        r = subprocess.run([sys.executable, os.path.join(HERE, "run_tests.py")],
                           env=env, capture_output=False)
        code = r.returncode
    finally:
        print("\n清理远程进程...")
        for c, host in ((cr, HOST_ROUTER), (cm, HOST_MOCK)):
            try:
                run(c, "pkill -x viberouter; pkill -x mockupstream", check=False)
                run(c, f"rm -rf {REMOTE_DIR}", check=False)
                c.close()
            except Exception as e:
                print(f"cleanup {host}: {e}")
    sys.exit(code)


if __name__ == "__main__":
    main()
