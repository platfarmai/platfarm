<?php
/**
 * Platfarm 契约测试三件套：无 token 401 / 有效 token 200 且身份正确 / 篡改 token 401。
 * 由 pctl check --e2e 在服务容器内执行（纯 streams，零依赖）。
 */

const TARGET = '__MOUNT__/me';

function gateway(): string
{
    return getenv('GATEWAY_URL') ?: 'http://gateway:8000';
}

/** @return array{0:int,1:array} */
function request(string $path, ?string $token = null, ?array $jsonBody = null): array
{
    $headers = [];
    if ($token !== null) {
        $headers[] = "Authorization: Bearer $token";
    }
    $opts = ['http' => ['ignore_errors' => true, 'timeout' => 5, 'method' => 'GET']];
    if ($jsonBody !== null) {
        $opts['http']['method'] = 'POST';
        $headers[] = 'Content-Type: application/json';
        $opts['http']['content'] = json_encode($jsonBody);
    }
    $opts['http']['header'] = implode("\r\n", $headers);
    $raw = file_get_contents(gateway() . $path, false, stream_context_create($opts));
    preg_match('#HTTP/\S+\s(\d{3})#', $http_response_header[0] ?? '', $m);
    return [(int)($m[1] ?? 0), json_decode((string)$raw, true) ?: []];
}

$failures = [];

[$status] = request(TARGET);
if ($status !== 401) {
    $failures[] = "无 token 应 401，得到 $status";
}

[$status, $login] = request('/auth/login', null, ['Username' => 'admin', 'Password' => 'admin123']);
if ($status !== 200 || empty($login['accessToken'])) {
    fwrite(STDERR, "FAIL: 种子账号登录失败 ($status)，无法继续\n");
    exit(1);
}
$token = $login['accessToken'];

[$status, $me] = request(TARGET, $token);
if ($status !== 200) {
    $failures[] = "有效 token 应 200，得到 $status";
} elseif (($me['username'] ?? '') !== 'admin') {
    $failures[] = '身份不匹配：期望 admin，得到 ' . ($me['username'] ?? 'null');
}

[$status] = request(TARGET, substr($token, 0, -2) . 'xx');
if ($status !== 401) {
    $failures[] = "篡改 token 应 401，得到 $status";
}

if ($failures !== []) {
    echo "FAIL:\n  - " . implode("\n  - ", $failures) . "\n";
    exit(1);
}
echo "PASS: 契约三件套全部通过\n";
