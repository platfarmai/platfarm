"""__SVC_ID__ — Platfarm 业务服务（接入约定见 docs/architecture-v2.md §2.2 + 附录 H.5）。"""

import os
import signal
import sys
from contextlib import asynccontextmanager

from fastapi import FastAPI, Header, HTTPException

for path in (
    os.environ.get("PF_SDK_PY", ""),
    os.path.join(os.path.dirname(__file__), "sdk"),
    os.path.join(os.path.dirname(__file__), "..", "..", "sdk", "py"),
):
    if path and os.path.isdir(path):
        sys.path.insert(0, path)
        break
import pfauth

_draining = False


def _begin_drain(*_args):
    global _draining
    _draining = True


signal.signal(signal.SIGTERM, _begin_drain)
signal.signal(signal.SIGINT, _begin_drain)


@asynccontextmanager
async def lifespan(_app: FastAPI):
    yield
    _begin_drain()

MOUNT = "__MOUNT__"
pfauth.load()

app = FastAPI(lifespan=lifespan)


def table(name: str) -> str:
    """可选表前缀（specs/007）：默认空 → 原样；设 PF_TABLE_PREFIX=cms_ → cms_<name>。"""
    return os.environ.get("PF_TABLE_PREFIX", "") + name


def identity(authorization: str | None, x_user_token: str | None = None) -> dict:
    try:
        return pfauth.identity(authorization, x_user_token)
    except (PermissionError, jwt_error()) as e:
        raise HTTPException(401, str(e)) from e


def jwt_error():
    import jwt
    return jwt.PyJWTError


@app.get(f"{MOUNT}/public/ping")
def ping():
    return {"pong": True, "service": "__SVC_ID__", "auth": "not required"}


@app.get(f"{MOUNT}/me")
def me(
    authorization: str | None = Header(None),
    x_pf_user_token: str | None = Header(None),
):
    c = identity(authorization, x_pf_user_token)
    return {
        "service": "__SVC_ID__",
        "userId": c.get("userId"),
        "username": c.get("username"),
        "role": c.get("role"),
        "tenantId": c.get("tenantId"),
        "svc": c.get("svc"),
    }


@app.get("/healthz")
def healthz():
    return {"ok": True}


@app.get("/readyz")
def readyz():
    if _draining:
        raise HTTPException(503, "draining")
    return {"ok": True}
