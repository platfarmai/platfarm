<?php
/**
 * __SVC_ID__ — Platfarm 业务服务（PHP 极简版，零 composer 依赖）。
 * 接入约定见 docs/architecture-v2.md §2.2 + 附录 H.5；运行：php -S 0.0.0.0:8080 router.php
 */

const MOUNT = '__MOUNT__';
const ISSUER = 'pf-auth';

function b64url_decode(string $s): string|false
{
    return base64_decode(strtr($s, '-_', '+/'));
}

/** RS256 验签 + iss/exp 校验，返回 claims（openssl 内建，无外部依赖）。 */
function decode_jwt(string $token): array
{
    $parts = explode('.', $token);
    if (count($parts) !== 3) {
        throw new RuntimeException('malformed token');
    }
    [$h, $p, $sig] = $parts;
    $pubPath = getenv('JWT_PUBLIC_KEY_FILE') ?: '/pf/jwt.pub';
    $pub = openssl_pkey_get_public(file_get_contents($pubPath));
    if ($pub === false || openssl_verify("$h.$p", b64url_decode($sig), $pub, OPENSSL_ALGO_SHA256) !== 1) {
        throw new RuntimeException('invalid signature');
    }
    $claims = json_decode(b64url_decode($p), true);
    if (!is_array($claims) || ($claims['iss'] ?? '') !== ISSUER) {
        throw new RuntimeException('invalid issuer');
    }
    if (($claims['exp'] ?? 0) < time()) {
        throw new RuntimeException('token expired');
    }
    return $claims;
}

/** 六步约定 + OBO：access 直接得身份；service 需在白名单，可携 X-PF-User-Token 代表用户。 */
function identity(array $headers): array
{
    $auth = $headers['Authorization'] ?? $headers['authorization'] ?? '';
    if (!str_starts_with($auth, 'Bearer ')) {
        throw new RuntimeException('missing bearer token');
    }
    $claims = decode_jwt(substr($auth, 7));
    $type = $claims['tokenType'] ?? '';
    if ($type === 'access') {
        return $claims;
    }
    if ($type === 'service') {
        $accept = array_filter(array_map('trim', explode(',', getenv('PF_ACCEPT_SERVICE_TOKENS') ?: '')));
        if (!in_array($claims['svc'] ?? '', $accept, true)) {
            throw new RuntimeException('service caller not allowed');
        }
        $userTok = $headers['X-PF-User-Token'] ?? $headers['x-pf-user-token'] ?? '';
        if ($userTok !== '') {
            $uc = decode_jwt($userTok);
            if (($uc['tokenType'] ?? '') !== 'access') {
                throw new RuntimeException('X-PF-User-Token must be an access token');
            }
            return $uc;
        }
        return $claims; // 后台任务上下文（无用户身份）
    }
    throw new RuntimeException('access or service token required');
}

function respond(int $code, array $body): never
{
    http_response_code($code);
    header('Content-Type: application/json');
    echo json_encode($body, JSON_UNESCAPED_UNICODE);
    exit;
}

$path = parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH);

match ($path) {
    MOUNT . '/public/ping' => respond(200, ['pong' => true, 'service' => '__SVC_ID__', 'auth' => 'not required']),
    '/healthz' => respond(200, ['ok' => true]),
    MOUNT . '/me' => (function () {
        try {
            $c = identity(getallheaders());
        } catch (RuntimeException $e) {
            respond(401, ['error' => $e->getMessage()]);
        }
        respond(200, [
            'service' => '__SVC_ID__',
            'userId' => $c['userId'] ?? 0,
            'username' => $c['username'] ?? '',
            'role' => $c['role'] ?? '',
            'tenantId' => $c['tenantId'] ?? 0,
            'svc' => $c['svc'] ?? '',
        ]);
    })(),
    default => respond(404, ['error' => 'not found']),
};
