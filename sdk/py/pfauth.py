"""Platfarm Python 验签唯一实现。模板应 import 本模块，不要再复制 JWT 解析。"""

import os

import jwt

ISSUER = "pf-auth"
_PUB = None
_EXTRA = {}
_ACCEPT = set()


def load():
    global _PUB
    path = os.environ.get("JWT_PUBLIC_KEY_FILE", "/pf/jwt.pub")
    _PUB = open(path, encoding="utf-8").read()
    extra = os.environ.get("JWT_EXTRA_PUB_DIR", "")
    if extra and os.path.isdir(extra):
        for name in os.listdir(extra):
            if name.endswith(".pub"):
                _EXTRA[name[:-4]] = open(os.path.join(extra, name), encoding="utf-8").read()
    _ACCEPT.clear()
    for item in os.environ.get("PF_ACCEPT_SERVICE_TOKENS", "").split(","):
        item = item.strip()
        if item:
            _ACCEPT.add(item)


def _key_for(token: str) -> str:
    header = jwt.get_unverified_header(token)
    kid = header.get("kid") or ""
    return _EXTRA.get(kid, _PUB)


def verify(token: str) -> dict:
    return jwt.decode(token, _key_for(token), algorithms=["RS256"], issuer=ISSUER)


def identity(authorization: str | None, user_token: str | None = None) -> dict:
    """access 直接得身份；service 需在白名单，可携用户 token 代表用户。"""
    if not authorization or not authorization.startswith("Bearer "):
        raise PermissionError("missing bearer token")
    claims = verify(authorization[7:])
    kind = claims.get("tokenType")
    if kind == "access":
        return claims
    if kind == "service":
        if claims.get("svc") not in _ACCEPT:
            raise PermissionError("service caller not allowed")
        if user_token:
            user = verify(user_token)
            if user.get("tokenType") != "access":
                raise PermissionError("X-PF-User-Token must be an access token")
            return user
        return claims
    raise PermissionError("access or service token required")
