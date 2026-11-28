import { ref } from "vue";

const BASE = "/api/pay";
let redirecting401 = false;

export async function api(method, path, body) {
  const opts = { method, credentials: "same-origin", headers: { "Content-Type": "application/json" } };
  if (body !== undefined) opts.body = JSON.stringify(body);
  const r = await fetch(BASE + path, opts);
  const data = await r.json().catch(() => ({}));
  if (r.status === 401) {
    if (!redirecting401) {
      redirecting401 = true;
      setTimeout(() => {
        try {
          if (window.self !== window.top) {
            window.top.location.href = "/";
            return;
          }
        } catch {
          // 跨域嵌套时回退当前窗口跳转
        }
        window.location.href = "/";
      }, 1200);
    }
    throw new Error("登录已过期，即将返回首页重新登录");
  }
  if (!r.ok) {
    // 校验类错误附带 problems 明细，一并展示，否则只看到“资料不完整”。
    const detail = Array.isArray(data.problems) && data.problems.length ? "：" + data.problems.join("；") : "";
    const err = new Error((data.error || `HTTP ${r.status}`) + detail);
    err.status = r.status;
    err.code = data.code || "";
    throw err;
  }
  return data;
}

// 运营替某个商户看商户中心时的商户号；商户本人登录时为空（后端按登录账号的绑定定范围）。
export const viewAs = ref("");

// portal 商户中心接口。运营代看时带上 mchNo，后端会再校验 operator 角色与租户。
export function portal(method, path, body) {
  const q = viewAs.value ? (path.includes("?") ? "&" : "?") + "mchNo=" + encodeURIComponent(viewAs.value) : "";
  return api(method, "/portal" + path + q, body);
}

export async function copyText(text, what = "内容") {
  try {
    await navigator.clipboard.writeText(text);
    window.pfToast?.(what + "已复制", "ok");
  } catch {
    window.pfToast?.("复制失败，请手动选中复制", "danger");
  }
}
