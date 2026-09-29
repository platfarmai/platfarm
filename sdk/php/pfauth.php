<?php
/**
 * Platfarm PHP 验签唯一实现。模板 require 本文件，不要再复制 JWT 解析。
 */

const PF_ISSUER = 'pf-auth';

function pf_b64url_decode(string $s): string|false
{
    return base64_decode(strtr($s, '-_', '+/'));
}

function pf_pub_for(string $token): mixed
{
    $parts = explode('.', $token);
    $header = json_decode(pf_b64url_decode($parts[0] ?? ''), true);
    $kid = is_array($header) ? ($header['kid'] ?? '') : '';
    $dir = getenv('JWT_EXTRA_PUB_DIR') ?: '';
    if ($kid !== '' && $dir !== '') {
        $extra = $dir . DIRECTORY_SEPARATOR . $kid . '.pub';
        if (is_file($extra)) {
            return openssl_pkey_get_public(file_get_contents($extra));
        }
    }
    $path = getenv('JWT_PUBLIC_KEY_FILE') ?: '/pf/jwt.pub';
    return openssl_pkey_get_public(file_get_contents($path));
}

function pf_verify(string $token): array
{
    $parts = explode('.', $token);
    if (count($parts) !== 3) {
        throw new RuntimeException('malformed token');
    }
    [$h, $p, $sig] = $parts;
    $pub = pf_pub_for($token);
    if ($pub === false || openssl_verify("$h.$p", pf_b64url_decode($sig), $pub, OPENSSL_ALGO_SHA256) !== 1) {
        throw new RuntimeException('invalid signature');
    }
    $claims = json_decode(pf_b64url_decode($p), true);
    if (!is_array($claims) || ($claims['iss'] ?? '') !== PF_ISSUER) {
        throw new RuntimeException('invalid issuer');
    }
    if (($claims['exp'] ?? 0) < time()) {
        throw new RuntimeException('token expired');
    }
    return $claims;
}

function pf_identity(array $headers): array
{
    $auth = $headers['Authorization'] ?? $headers['authorization'] ?? '';
    if (!str_starts_with($auth, 'Bearer ')) {
        throw new RuntimeException('missing bearer token');
    }
    $claims = pf_verify(substr($auth, 7));
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
            $user = pf_verify($userTok);
            if (($user['tokenType'] ?? '') !== 'access') {
                throw new RuntimeException('X-PF-User-Token must be an access token');
            }
            return $user;
        }
        return $claims;
    }
    throw new RuntimeException('access or service token required');
}
