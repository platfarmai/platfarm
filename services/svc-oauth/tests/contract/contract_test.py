"""svc-oauth 契约测试：mock provider 全流程 + 兑换码一次性 + state 防伪。"""

import json
import os
import sys
import urllib.error
import urllib.request

GATEWAY = os.environ.get("GATEWAY_URL", "http://gateway:8000")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    """禁用自动跟随：302 的 Location 指向外部 SELF_URL，容器内需重写为网关地址。"""

    def redirect_request(self, *args, **kwargs):
        return None


urllib.request.install_opener(urllib.request.build_opener(NoRedirect))


def get(path: str, follow: bool = True):
    url = GATEWAY + path
    while True:
        req = urllib.request.Request(url)
        try:
            with urllib.request.urlopen(req) as resp:
                return resp.status, json.loads(resp.read().decode())
        except urllib.error.HTTPError as e:
            if follow and e.code in (301, 302):
                loc = e.headers["Location"]
                url = loc.replace(os.environ.get("SELF_URL", "http://localhost:18000"), GATEWAY)
                continue
            return e.code, {}


def post(path: str, body: dict, token: str | None = None):
    req = urllib.request.Request(
        GATEWAY + path, json.dumps(body).encode(), {"Content-Type": "application/json"}
    )
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, json.loads(resp.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, {}


def main() -> int:
    failures: list[str] = []

    # 1. mock 全流程：authorize → (302) callback → 兑换码 → token 对 → /auth/me
    status, cb = get("/api/oauth/mock/authorize")
    if status != 200 or "exchangeCode" not in cb:
        print(f"FAIL: mock authorize/callback 流程失败 ({status}) {cb}")
        return 1
    status, tokens = post("/api/oauth/exchange", {"code": cb["exchangeCode"]})
    if status != 200 or "accessToken" not in tokens:
        failures.append(f"兑换码换 token 失败 ({status})")
    else:
        req = urllib.request.Request(
            GATEWAY + "/auth/me", headers={"Authorization": f"Bearer {tokens['accessToken']}"}
        )
        with urllib.request.urlopen(req) as resp:
            me = json.loads(resp.read().decode())
        if not str(me.get("username", "")).startswith("mock_"):
            failures.append(f"外部身份用户名异常: {me.get('username')}")

        # 2. 兑换码只能用一次
        status, _ = post("/api/oauth/exchange", {"code": cb["exchangeCode"]})
        if status != 401:
            failures.append(f"兑换码复用应 401，得到 {status}")

    # 3. 伪造 state 应被拒
    status, _ = get("/api/oauth/mock/callback?code=x&state=forged", follow=False)
    if status != 401:
        failures.append(f"伪造 state 应 401，得到 {status}")

    if failures:
        print("FAIL:\n  - " + "\n  - ".join(failures))
        return 1
    print("PASS: svc-oauth 契约测试全部通过")
    return 0


if __name__ == "__main__":
    sys.exit(main())
