// 同源 Cookie 会话：不碰 token，Kong 在网关层补 Bearer（docs/ui-frameworks.md 约束 1）
const BASE = "/api/svc-hello"; // 改成你的服务挂载路径

export async function api(method, path, body) {
  const opts = { method, credentials: "same-origin", headers: { "Content-Type": "application/json" } };
  if (body !== undefined) opts.body = JSON.stringify(body);
  const r = await fetch(BASE + path, opts);
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
  return data;
}
