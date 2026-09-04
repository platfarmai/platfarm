"""Platfarm 契约测试三件套：无 token 401 / 有效 token 200 且身份正确 / 篡改 token 401。

测的是平台契约不是业务；在服务容器内执行（platctl check --e2e），经网关内网地址访问。
"""

import json
import os
import sys
import urllib.error
import urllib.request

GATEWAY = os.environ.get("GATEWAY_URL", "http://gateway:8000")
TARGET = "__MOUNT__/me"
SEED_USER = {"Username": "admin", "Password": "admin123"}  # 开发种子账号


def request(path: str, token: str | None = None, method: str = "GET", body: dict | None = None):
    req = urllib.request.Request(GATEWAY + path, method=method)
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    data = None
    if body is not None:
        req.add_header("Content-Type", "application/json")
        data = json.dumps(body).encode()
    try:
        with urllib.request.urlopen(req, data) as resp:
            return resp.status, json.loads(resp.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, {}


def main() -> int:
    failures: list[str] = []

    status, _ = request(TARGET)
    if status != 401:
        failures.append(f"无 token 应 401，得到 {status}")

    status, login = request("/auth/login", method="POST", body=SEED_USER)
    if status != 200:
        print(f"FAIL: 种子账号登录失败 ({status})，无法继续")
        return 1
    token = login["accessToken"]

    status, me = request(TARGET, token=token)
    if status != 200:
        failures.append(f"有效 token 应 200，得到 {status}")
    elif me.get("username") != "admin":
        failures.append(f"身份不匹配：期望 admin，得到 {me.get('username')}")

    status, _ = request(TARGET, token=token[:-2] + "xx")
    if status != 401:
        failures.append(f"篡改 token 应 401，得到 {status}")

    if failures:
        print("FAIL:\n  - " + "\n  - ".join(failures))
        return 1
    print("PASS: 契约三件套全部通过")
    return 0


if __name__ == "__main__":
    sys.exit(main())
