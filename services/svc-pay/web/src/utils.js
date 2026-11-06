export const yuan = (fen) => (Number(fen || 0) / 100).toFixed(2);
export const when = (s) => (s ? String(s).replace("T", " ").slice(0, 19) : "");

export const applymentStates = {
  draft: "草稿",
  reviewing: "审核中",
  approved: "已通过",
  submitted: "已提交渠道",
  channel_auditing: "渠道审核中",
  need_sign: "待签约",
  need_verify: "待验证",
  finished: "完成",
  rejected: "已驳回",
  canceled: "已取消",
};

export const stateTone = (s) =>
  ({ finished: "ok", success: "ok", rejected: "danger", failed: "danger", canceled: "neutral", closed: "neutral" })[s] || "info";
