"""svc-oauth — 第三方登录能力服务（specs/changes/001，附录 G）。

OAuth 舞步全在这里；auth 只做兑换。provider 模块化：mock（开发/E2E）、github。
"""

import json
import os
import secrets
import time
import urllib.parse
import urllib.request

from fastapi import FastAPI, HTTPException
from fastapi.responses import RedirectResponse

AUTH_URL = os.environ.get("AUTH_URL", "http://auth:8080")
CLIENT_ID = os.environ.get("PF_CLIENT_ID", "svc-oauth")
CLIENT_SECRET = os.environ.get("PF_CLIENT_SECRET", "")
SELF_URL = os.environ.get("SELF_URL", "http://localhost:18000")  # 回调外部地址

app = FastAPI()

# 内存 TTL 存储（单实例；多副本按无状态铁律换 Redis，附录 H.7）
_states: dict[str, float] = {}  # state -> 过期时间戳
_codes: dict[str, tuple[float, dict]] = {}  # 一次性兑换码 -> (过期, token对)
STATE_TTL, CODE_TTL = 600, 30


def _sweep() -> None:
    now = time.time()
    for d in (_states, _codes):
        for k in [k for k, v in d.items() if (v if isinstance(v, float) else v[0]) < now]:
            d.pop(k, None)


def _post_json(url: str, body: dict, headers: dict | None = None) -> dict:
    req = urllib.request.Request(
        url, json.dumps(body).encode(), {"Content-Type": "application/json", **(headers or {})}
    )
    with urllib.request.urlopen(req, timeout=5) as resp:
        return json.loads(resp.read().decode())


def service_token() -> str:
    out = _post_json(
        f"{AUTH_URL}/auth/service-token",
        {"ClientId": CLIENT_ID, "ClientSecret": CLIENT_SECRET},
    )
    return out["accessToken"]


def exchange_external(provider: str, external_id: str, display: str) -> dict:
    """已验证的外部身份 → 平台 token 对（auth 内网端点，service token 鉴权）。"""
    return _post_json(
        f"{AUTH_URL}/internal/auth/external-login",
        {"Provider": provider, "ExternalId": external_id, "DisplayName": display},
        {"Authorization": f"Bearer {service_token()}"},
    )


# ── provider 模块 ─────────────────────────────────────────────

def github_authorize(state: str) -> str:
    q = urllib.parse.urlencode({
        "client_id": os.environ.get("GITHUB_CLIENT_ID", ""),
        "redirect_uri": f"{SELF_URL}/api/oauth/github/callback",
        "state": state,
    })
    return f"https://github.com/login/oauth/authorize?{q}"


def github_profile(code: str) -> tuple[str, str]:
    tok = _post_json(
        "https://github.com/login/oauth/access_token",
        {
            "client_id": os.environ.get("GITHUB_CLIENT_ID", ""),
            "client_secret": os.environ.get("GITHUB_CLIENT_SECRET", ""),
            "code": code,
        },
        {"Accept": "application/json"},
    )
    req = urllib.request.Request(
        "https://api.github.com/user",
        headers={"Authorization": f"Bearer {tok['access_token']}"},
    )
    with urllib.request.urlopen(req, timeout=5) as resp:
        u = json.loads(resp.read().decode())
    return str(u["id"]), u.get("login", "")


def mock_authorize(state: str) -> str:
    """开发/E2E 用：直接跳回 callback，externalId 固定。"""
    return f"{SELF_URL}/api/oauth/mock/callback?code=mock-code&state={state}"


def mock_profile(_code: str) -> tuple[str, str]:
    return "mock-user-1", "Mock User"


PROVIDERS = {
    "github": (github_authorize, github_profile),
    "mock": (mock_authorize, mock_profile),
}


# ── 路由 ──────────────────────────────────────────────────────

@app.get("/api/oauth/{provider}/authorize")
def authorize(provider: str):
    if provider not in PROVIDERS:
        raise HTTPException(404, "unknown provider")
    _sweep()
    state = secrets.token_urlsafe(24)
    _states[state] = time.time() + STATE_TTL
    return RedirectResponse(PROVIDERS[provider][0](state), status_code=302)


@app.get("/api/oauth/{provider}/callback")
def callback(provider: str, code: str, state: str):
    if provider not in PROVIDERS:
        raise HTTPException(404, "unknown provider")
    if _states.pop(state, 0) < time.time():
        raise HTTPException(401, "invalid or expired state")  # 防 CSRF
    external_id, display = PROVIDERS[provider][1](code)
    tokens = exchange_external(provider, external_id, display)
    # 禁止 JWT 进重定向 URL：发 30s 一次性兑换码（附录 G）
    once = secrets.token_urlsafe(24)
    _codes[once] = (time.time() + CODE_TTL, tokens)
    return {"exchangeCode": once, "expiresIn": CODE_TTL}


@app.post("/api/oauth/exchange")
def exchange(body: dict):
    _sweep()
    entry = _codes.pop(body.get("code", ""), None)  # pop = 只能用一次
    if entry is None or entry[0] < time.time():
        raise HTTPException(401, "invalid or expired exchange code")
    return entry[1]


@app.get("/healthz")
def healthz():
    return {"ok": True}
