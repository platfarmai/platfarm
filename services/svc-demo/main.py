"""svc-demo — Platfarm 业务服务（接入约定见 docs/architecture-v2.md §2.2 + 附录 H.5）。"""

import os

import jwt
from fastapi import FastAPI, Header, HTTPException

# RS256 公钥由平台挂载（pctl sync 生成的 compose 注入；公钥非密）
PUB = open(os.environ.get("JWT_PUBLIC_KEY_FILE", "/pf/jwt.pub")).read()
ISSUER = "pf-auth"
MOUNT = "/api/demo"
# 允许用 service token 调本服务的调用方白名单（service.yaml accept_service_tokens 注入）
ACCEPT_SERVICES = {s for s in os.environ.get("PF_ACCEPT_SERVICE_TOKENS", "").split(",") if s}

app = FastAPI()


def decode(token: str) -> dict:
    try:
        return jwt.decode(token, PUB, algorithms=["RS256"], issuer=ISSUER)
    except jwt.PyJWTError as e:
        raise HTTPException(401, f"invalid token: {e}")


def identity(authorization: str | None, x_user_token: str | None = None) -> dict:
    """六步约定 + OBO：access 直接得身份；service 需在白名单，可携 X-PF-User-Token 代表用户。"""
    if not authorization or not authorization.startswith("Bearer "):
        raise HTTPException(401, "missing bearer token")
    claims = decode(authorization[7:])
    kind = claims.get("tokenType")
    if kind == "access":
        return claims
    if kind == "service":
        if claims.get("svc") not in ACCEPT_SERVICES:
            raise HTTPException(401, "service caller not allowed")
        if x_user_token:  # On-Behalf-Of：代表用户
            uc = decode(x_user_token)
            if uc.get("tokenType") != "access":
                raise HTTPException(401, "X-PF-User-Token must be an access token")
            return uc
        return claims  # 后台任务上下文（无用户身份）
    raise HTTPException(401, "access or service token required")


@app.get(f"{MOUNT}/public/ping")
def ping():
    return {"pong": True, "service": "svc-demo", "auth": "not required"}


@app.get(f"{MOUNT}/me")
def me(
    authorization: str | None = Header(None),
    x_pf_user_token: str | None = Header(None),
):
    c = identity(authorization, x_pf_user_token)
    return {
        "service": "svc-demo",
        "userId": c.get("userId"),
        "username": c.get("username"),
        "role": c.get("role"),
        "tenantId": c.get("tenantId"),
        "svc": c.get("svc"),
    }


@app.get("/healthz")
def healthz():
    return {"ok": True}

