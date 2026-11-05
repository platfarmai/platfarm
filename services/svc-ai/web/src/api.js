const BASE = "/api/ai";

export async function api(method, path, body) {
  const opts = {
    method,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
  };
  if (body !== undefined) opts.body = JSON.stringify(body);
  const r = await fetch(BASE + path, opts);
  if (r.status === 401) {
    // 登录失效：跳平台登录页（iframe 内跳顶层窗口）
    const login = "/platform/console/";
    try {
      if (window.top !== window) window.top.location.href = login;
      else window.location.href = login;
    } catch {
      window.location.href = login;
    }
    throw new Error("登录已失效，正在跳转登录页…");
  }
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
  return data;
}

export function toastOk(msg) {
  if (typeof pfToast === "function") pfToast(msg, "ok");
}

export function toastErr(e) {
  const msg = e?.message || String(e);
  if (typeof pfToast === "function") pfToast(msg, "danger");
  else console.error(msg);
}

export async function confirmDanger(msg) {
  if (typeof pfConfirm === "function") return pfConfirm(msg, { danger: true });
  return window.confirm(msg);
}
