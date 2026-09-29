//! Platfarm Rust 验签唯一实现。模板依赖本 crate，不要再复制 JWT 解析。

use std::collections::HashMap;
use std::fs;
use std::path::Path;

use jsonwebtoken::{decode, decode_header, Algorithm, DecodingKey, Validation};
use serde::Deserialize;

const ISSUER: &str = "pf-auth";

#[derive(Debug, Deserialize, Clone)]
#[serde(rename_all = "camelCase")]
pub struct Claims {
    pub token_type: String,
    #[serde(default)]
    pub user_id: i64,
    #[serde(default)]
    pub username: String,
    #[serde(default)]
    pub role: String,
    #[serde(default)]
    pub tenant_id: i64,
    #[serde(default)]
    pub svc: String,
}

pub struct Verifier {
    current: DecodingKey,
    extra: HashMap<String, DecodingKey>,
    accept: std::collections::HashSet<String>,
}

impl Verifier {
    pub fn from_env() -> Self {
        let path = std::env::var("JWT_PUBLIC_KEY_FILE").unwrap_or_else(|_| "/pf/jwt.pub".into());
        let pem = fs::read(&path).unwrap_or_else(|e| panic!("read public key: {e}"));
        let mut extra = HashMap::new();
        if let Ok(dir) = std::env::var("JWT_EXTRA_PUB_DIR") {
            if let Ok(entries) = fs::read_dir(&dir) {
                for entry in entries.flatten() {
                    let name = entry.file_name().to_string_lossy().to_string();
                    if let Some(kid) = name.strip_suffix(".pub") {
                        if let Ok(raw) = fs::read(entry.path()) {
                            if let Ok(key) = DecodingKey::from_rsa_pem(&raw) {
                                extra.insert(kid.to_string(), key);
                            }
                        }
                    }
                }
            }
        }
        let accept = std::env::var("PF_ACCEPT_SERVICE_TOKENS")
            .unwrap_or_default()
            .split(',')
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect();
        Self {
            current: DecodingKey::from_rsa_pem(&pem).expect("parse public key"),
            extra,
            accept,
        }
    }

    fn key_for(&self, token: &str) -> &DecodingKey {
        decode_header(token)
            .ok()
            .and_then(|h| h.kid)
            .and_then(|kid| self.extra.get(&kid))
            .unwrap_or(&self.current)
    }

    pub fn verify(&self, token: &str) -> Result<Claims, String> {
        let mut validation = Validation::new(Algorithm::RS256);
        validation.set_issuer(&[ISSUER]);
        validation.validate_aud = false;
        decode::<Claims>(token, self.key_for(token), &validation)
            .map(|d| d.claims)
            .map_err(|e| format!("invalid token: {e}"))
    }

    pub fn identity(&self, authorization: &str, user_token: Option<&str>) -> Result<Claims, String> {
        let token = authorization
            .strip_prefix("Bearer ")
            .ok_or_else(|| "missing bearer token".to_string())?;
        let claims = self.verify(token)?;
        match claims.token_type.as_str() {
            "access" => Ok(claims),
            "service" => {
                if !self.accept.contains(&claims.svc) {
                    return Err("service caller not allowed".into());
                }
                if let Some(user_token) = user_token {
                    let user = self.verify(user_token)?;
                    if user.token_type != "access" {
                        return Err("X-PF-User-Token must be an access token".into());
                    }
                    return Ok(user);
                }
                Ok(claims)
            }
            _ => Err("access or service token required".into()),
        }
    }
}

pub fn extra_dir_exists(dir: &str) -> bool {
    Path::new(dir).is_dir()
}
