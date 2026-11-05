export function escapeHtml(s) {
  return String(s ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

/** Minimal markdown: headings, bold, unordered lists. HTML escaped first. */
export function renderMarkdown(text) {
  if (!text) return "";
  const lines = String(text).replace(/\r\n/g, "\n").split("\n");
  const out = [];
  let inList = false;
  const flushList = () => {
    if (inList) {
      out.push("</ul>");
      inList = false;
    }
  };
  for (const raw of lines) {
    const line = escapeHtml(raw);
    const heading = /^(#{1,3})\s+(.+)$/.exec(raw);
    if (heading) {
      flushList();
      const level = heading[1].length;
      out.push(`<h${level}>${escapeHtml(heading[2])}</h${level}>`);
      continue;
    }
    const li = /^[-*]\s+(.+)$/.exec(raw);
    if (li) {
      if (!inList) {
        out.push("<ul>");
        inList = true;
      }
      out.push(`<li>${inlineMd(escapeHtml(li[1]))}</li>`);
      continue;
    }
    flushList();
    if (!raw.trim()) {
      out.push("<br>");
      continue;
    }
    out.push(`<p>${inlineMd(line)}</p>`);
  }
  flushList();
  return out.join("");
}

function inlineMd(escaped) {
  return escaped.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>");
}

export function humanizeHours(h) {
  if (h == null || Number.isNaN(Number(h))) return "—";
  const total = Math.max(0, Math.round(Number(h)));
  const days = Math.floor(total / 24);
  const hours = total % 24;
  if (days > 0) return `${days}天${hours}小时`;
  return `${hours}小时`;
}

export function humanizeSeconds(sec) {
  if (sec == null || Number.isNaN(Number(sec))) return "—";
  return humanizeHours(Number(sec) / 3600);
}

export function formatRate(rate) {
  if (rate == null || Number.isNaN(Number(rate))) return null;
  return Math.round(Number(rate) * 100);
}

export function rateTone(rate) {
  const pct = formatRate(rate);
  if (pct == null) return "neutral";
  if (pct >= 80) return "ok";
  if (pct >= 50) return "warn";
  return "danger";
}

export function shortSha(sha) {
  return String(sha || "").slice(0, 7);
}

export function firstLine(msg) {
  return String(msg || "").split("\n")[0] || "";
}

export function excerpt(text, n = 120) {
  const s = String(text || "").replace(/\s+/g, " ").trim();
  if (s.length <= n) return s;
  return s.slice(0, n) + "…";
}

export function formatAbs(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return String(iso);
  const pad = (x) => String(x).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function formatRel(iso) {
  if (!iso) return "";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "";
  const diff = Date.now() - t;
  const sec = Math.round(diff / 1000);
  if (sec < 60) return "刚刚";
  const min = Math.round(sec / 60);
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.round(min / 60);
  if (hr < 48) return `${hr} 小时前`;
  const day = Math.round(hr / 24);
  if (day < 30) return `${day} 天前`;
  return formatAbs(iso);
}

export const ISSUE_STATUS = {
  open: { label: "待处理", tone: "info" },
  ai_matched: { label: "AI已匹配", tone: "warn" },
  verified: { label: "已核验", tone: "ok" },
  closed: { label: "已关闭", tone: "neutral" },
};

export function statusLabel(s) {
  return ISSUE_STATUS[s]?.label || s || "—";
}

export function statusTone(s) {
  return ISSUE_STATUS[s]?.tone || "neutral";
}