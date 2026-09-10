//! __SVC_ID__ — Platfarm 业务服务（接入约定见 docs/architecture-v2.md §2.2 + 附录 H.5）。

use std::collections::HashSet;
use std::sync::OnceLock;

use axum::http::{HeaderMap, StatusCode};
use axum::response::Json;
use axum::routing::get;
use axum::Router;
use jsonwebtoken::{decode, Algorithm, DecodingKey, Validation};
use serde::Deserialize;
use serde_json::{json, Value};

const MOUNT: &str = "__MOUNT__";
const ISSUER: &str = "pf-auth";

/// Claims 契约：docs/architecture-v2.md §2.1
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Claims {
    token_type: String,
    #[serde(default)]
    user_id: i64,
    #[serde(default)]
    username: String,
    #[serde(default)]
    role: String,
    #[serde(default)]
    tenant_id: i64,
    #[serde(default)]
    svc: String,
}

static KEY: OnceLock<DecodingKey> = OnceLock::new();
static ACCEPT: OnceLock<HashSet<String>> = OnceLock::new();

fn decode_token(token: &str) -> Result<Claims, String> {
    let mut validation = Validation::new(Algorithm::RS256);
    validation.set_issuer(&[ISSUER]);
    validation.validate_aud = false;
    decode::<Claims>(token, KEY.get().expect("key loaded"), &validation)
        .map(|d| d.claims)
        .map_err(|e| format!("invalid token: {e}"))
}

/// 六步约定 + OBO：access 直接得身份；service 需在白名单，可携 X-PF-User-Token 代表用户。
fn identity(headers: &HeaderMap) -> Result<Claims, String> {
    let auth = headers
        .get("authorization")
        .and_then(|v| v.to_str().ok())
        .unwrap_or_default();
    let token = auth
        .strip_prefix("Bearer ")
        .ok_or_else(|| "missing bearer token".to_string())?;
    let claims = decode_token(token)?;
    match claims.token_type.as_str() {
        "access" => Ok(claims),
        "service" => {
            if !ACCEPT.get().expect("accept loaded").contains(&claims.svc) {
                return Err("service caller not allowed".into());
            }
            match headers.get("x-pf-user-token").and_then(|v| v.to_str().ok()) {
                Some(user_token) => {
                    let uc = decode_token(user_token)?;
                    if uc.token_type != "access" {
                        return Err("X-PF-User-Token must be an access token".into());
                    }
                    Ok(uc)
                }
                None => Ok(claims), // 后台任务上下文（无用户身份）
            }
        }
        _ => Err("access or service token required".into()),
    }
}

async fn me(headers: HeaderMap) -> (StatusCode, Json<Value>) {
    match identity(&headers) {
        Ok(c) => (
            StatusCode::OK,
            Json(json!({
                "service": "__SVC_ID__", "userId": c.user_id, "username": c.username,
                "role": c.role, "tenantId": c.tenant_id, "svc": c.svc,
            })),
        ),
        Err(e) => (StatusCode::UNAUTHORIZED, Json(json!({ "error": e }))),
    }
}

async fn ping() -> Json<Value> {
    Json(json!({ "pong": true, "service": "__SVC_ID__", "auth": "not required" }))
}

async fn healthz() -> Json<Value> {
    Json(json!({ "ok": true }))
}

#[tokio::main]
async fn main() {
    let key_path =
        std::env::var("JWT_PUBLIC_KEY_FILE").unwrap_or_else(|_| "/pf/jwt.pub".to_string());
    let pem = std::fs::read(&key_path).expect("read public key");
    KEY.set(DecodingKey::from_rsa_pem(&pem).expect("parse public key"))
        .ok();
    let accept: HashSet<String> = std::env::var("PF_ACCEPT_SERVICE_TOKENS")
        .unwrap_or_default()
        .split(',')
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .collect();
    ACCEPT.set(accept).ok();

    let app = Router::new()
        .route(&format!("{MOUNT}/public/ping"), get(ping))
        .route(&format!("{MOUNT}/me"), get(me))
        .route("/healthz", get(healthz));

    let listener = tokio::net::TcpListener::bind("0.0.0.0:8080")
        .await
        .expect("bind 8080");
    println!("__SVC_ID__ listening on :8080");
    axum::serve(listener, app).await.expect("server error");
}
