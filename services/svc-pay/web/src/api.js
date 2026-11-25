const BASE = "/api/pay";

export async function api(method, path, body) {
  const opts = { method, credentials: "same-origin", headers: { "Content-Type": "application/json" } };
  if (body !== undefined) opts.body = JSON.stringify(body);
  const r = await fetch(BASE + path, opts);
  const data = await r.json().catch(() => ({}));
  if (!r.ok) {
    // 校验类错误附带 problems 明细，一并展示，否则只看到“资料不完整”。
    const detail = Array.isArray(data.problems) && data.problems.length ? "：" + data.problems.join("；") : "";
    throw new Error((data.error || `HTTP ${r.status}`) + detail);
  }
  return data;
}
