//! Platfarm 契约测试三件套：无 token 401 / 有效 token 200 且身份正确 / 篡改 token 401。
//! 编译为 /contract-test，由 pctl check --e2e 在服务容器内执行。

use serde_json::{json, Value};

const TARGET: &str = "__MOUNT__/me";

fn gateway() -> String {
    std::env::var("GATEWAY_URL").unwrap_or_else(|_| "http://gateway:8000".to_string())
}

fn request(path: &str, token: Option<&str>) -> (u16, Value) {
    let mut req = ureq::get(&format!("{}{}", gateway(), path));
    if let Some(t) = token {
        req = req.set("Authorization", &format!("Bearer {t}"));
    }
    match req.call() {
        Ok(resp) => {
            let status = resp.status();
            let body: Value = resp.into_json().unwrap_or(Value::Null);
            (status, body)
        }
        Err(ureq::Error::Status(status, resp)) => {
            let body: Value = resp.into_json().unwrap_or(Value::Null);
            (status, body)
        }
        Err(e) => {
            eprintln!("request error: {e}");
            std::process::exit(1);
        }
    }
}

fn login() -> String {
    let resp = ureq::post(&format!("{}/auth/login", gateway()))
        .send_json(json!({ "Username": "admin", "Password": "admin123" }));
    let body: Value = match resp {
        Ok(r) => r.into_json().unwrap_or(Value::Null),
        Err(e) => {
            eprintln!("FAIL: 种子账号登录失败，无法继续: {e}");
            std::process::exit(1);
        }
    };
    match body["accessToken"].as_str() {
        Some(t) => t.to_string(),
        None => {
            eprintln!("FAIL: 登录响应缺少 accessToken");
            std::process::exit(1);
        }
    }
}

fn main() {
    let mut failures: Vec<String> = Vec::new();

    let (status, _) = request(TARGET, None);
    if status != 401 {
        failures.push(format!("无 token 应 401，得到 {status}"));
    }

    let token = login();
    let (status, body) = request(TARGET, Some(&token));
    if status != 200 {
        failures.push(format!("有效 token 应 200，得到 {status}"));
    } else if body["username"] != "admin" {
        failures.push(format!("身份不匹配：期望 admin，得到 {}", body["username"]));
    }

    let tampered = format!("{}xx", &token[..token.len() - 2]);
    let (status, _) = request(TARGET, Some(&tampered));
    if status != 401 {
        failures.push(format!("篡改 token 应 401，得到 {status}"));
    }

    if !failures.is_empty() {
        println!("FAIL:");
        for f in &failures {
            println!("  - {f}");
        }
        std::process::exit(1);
    }
    println!("PASS: 契约三件套全部通过");
}
