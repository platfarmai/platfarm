<?php
/**
 * __SVC_ID__ — Platfarm 业务服务（PHP 极简版，零 composer 依赖）。
 * 接入约定见 docs/architecture-v2.md §2.2 + 附录 H.5；运行：php -S 0.0.0.0:8080 router.php
 */

const MOUNT = '__MOUNT__';

$pfSdk = getenv('PF_SDK_PHP') ?: '';
if ($pfSdk === '' || !is_file($pfSdk . '/pfauth.php')) {
    foreach ([__DIR__ . '/sdk/pfauth.php', __DIR__ . '/../../sdk/php/pfauth.php'] as $candidate) {
        if (is_file($candidate)) {
            $pfSdk = dirname($candidate);
            break;
        }
    }
}
require $pfSdk . '/pfauth.php';

function identity(array $headers): array
{
    return pf_identity($headers);
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
    '/readyz' => respond(200, ['ok' => true]), // php -S 无 SIGTERM drain；stop_grace_period 到期可能切断在途请求（specs/005）
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
