//! __SVC_ID__ — Platfarm 业务服务（接入约定见 docs/architecture-v2.md §2.2 + 附录 H.5）。

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::OnceLock;

use axum::http::{HeaderMap, StatusCode};
use axum::response::Json;
use axum::routing::get;
use axum::Router;
use pfauth::Verifier;
use serde_json::{json, Value};

const MOUNT: &str = "__MOUNT__";

static AUTH: OnceLock<Verifier> = OnceLock::new();
static DRAINING: AtomicBool = AtomicBool::new(false);

fn identity(headers: &HeaderMap) -> Result<pfauth::Claims, String> {
    let auth = headers
        .get("authorization")
        .and_then(|v| v.to_str().ok())
        .unwrap_or_default();
    let user = headers
        .get("x-pf-user-token")
        .and_then(|v| v.to_str().ok());
    AUTH.get().expect("auth loaded").identity(auth, user)
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

async fn readyz() -> (StatusCode, Json<Value>) {
    if DRAINING.load(Ordering::SeqCst) {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({ "error": "draining" })),
        );
    }
    (StatusCode::OK, Json(json!({ "ok": true })))
}

#[tokio::main]
async fn main() {
    AUTH.set(Verifier::from_env()).ok();

    let app = Router::new()
        .route(&format!("{MOUNT}/public/ping"), get(ping))
        .route(&format!("{MOUNT}/me"), get(me))
        .route("/healthz", get(healthz))
        .route("/readyz", get(readyz));

    let listener = tokio::net::TcpListener::bind("0.0.0.0:8080")
        .await
        .expect("bind 8080");
    println!("__SVC_ID__ listening on :8080");
    axum::serve(listener, app)
        .with_graceful_shutdown(shutdown_signal())
        .await
        .expect("server error");
}

async fn shutdown_signal() {
    #[cfg(unix)]
    {
        let mut sigterm = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
            .expect("sigterm");
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {}
            _ = sigterm.recv() => {}
        }
    }
    #[cfg(not(unix))]
    {
        let _ = tokio::signal::ctrl_c().await;
    }
    DRAINING.store(true, Ordering::SeqCst);
}
