// 文档页与平台接口索引页共用的小脚本：填网关地址、展示程序拿到的 JSON。
(function () {
  "use strict";

  function populateOrigins() {
    var origin = window.location.origin && window.location.origin !== "null"
      ? window.location.origin
      : "https://apipay.sanrun.fun";
    var nodes = document.querySelectorAll(".js-origin");
    for (var i = 0; i < nodes.length; i++) {
      nodes[i].textContent = origin;
    }
  }

  // fetch 默认的 Accept 是 */*，不含 text/html，网关会把它交给平台返回 JSON（而不是本页）。
  async function showInfoJson() {
    var el = document.getElementById("infoJson");
    if (!el) return;
    try {
      var r = await fetch("/platform/info", { headers: { Accept: "application/json" } });
      if (!r.ok) throw new Error("HTTP " + r.status);
      el.textContent = JSON.stringify(await r.json(), null, 2);
    } catch (e) {
      el.textContent = "暂时读取不到（" + (e.message || String(e)) + "），请稍后刷新重试。";
    }
  }

  document.addEventListener("DOMContentLoaded", function () {
    populateOrigins();
    showInfoJson();
  });
})();
