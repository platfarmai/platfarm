(function () {
  "use strict";

  var PAY_CONSOLE = "/api/pay/console/";
  var PLATFORM_CONSOLE = "/platform/console/";
  var OPENAPI_CONSOLE = "/api/openapi/console/";

  var state = {
    me: null,
    targetAfterLogin: PAY_CONSOLE,
    needsTotp: false
  };

  function $(id) {
    return document.getElementById(id);
  }

  function alertBody(el) {
    return el.querySelector(".pf-alert-body") || el;
  }

  function setAlert(el, msg, tone) {
    if (!el) return;
    if (!msg) {
      el.hidden = true;
      alertBody(el).textContent = "";
      return;
    }
    el.hidden = false;
    el.setAttribute("tone", tone || "danger");
    alertBody(el).textContent = msg;
  }

  function setBusy(btn, busy) {
    if (!btn) return;
    if (busy) btn.setAttribute("loading", "");
    else btn.removeAttribute("loading");
  }

  function roleLabel(role) {
    if (role === "admin") return "系统管理员";
    return "业务操作员";
  }

  function renderSession() {
    var guestBar = $("guestActions");
    var userBar = $("userActions");
    var heroPrimary = $("btnHeroPrimary");
    var heroPrimaryLabel = heroPrimary && (heroPrimary.querySelector(".pf-btn-label") || heroPrimary);

    if (state.me) {
      guestBar.hidden = true;
      userBar.hidden = false;
      $("userName").textContent = state.me.username || "已登录";
      var roleEl = $("userRole");
      var pill = roleEl && (roleEl.querySelector(".pf-badge-pill") || roleEl);
      if (pill) pill.textContent = roleLabel(state.me.role);
      var isAdmin = state.me.role === "admin";
      $("btnPlatformConsole").hidden = !isAdmin;
      $("btnOpenapiConsole").hidden = !isAdmin;
      if (heroPrimaryLabel) heroPrimaryLabel.textContent = "进入支付中心";
    } else {
      guestBar.hidden = false;
      userBar.hidden = true;
      if (heroPrimaryLabel) heroPrimaryLabel.textContent = "登录并进入支付中心";
    }
  }

  async function checkSession() {
    try {
      var r = await fetch("/auth/me", { credentials: "same-origin" });
      if (r.ok) {
        state.me = await r.json();
      } else {
        state.me = null;
      }
    } catch (_e) {
      state.me = null;
    }
    renderSession();
  }

  async function checkGatewayStatus() {
    var dot = $("gatewayDot");
    if (!dot) return;
    try {
      var r = await fetch("/platform/info", { credentials: "same-origin" });
      if (r.ok) {
        dot.setAttribute("data-tone", "ok");
        dot.textContent = "网关在线";
      } else {
        dot.setAttribute("data-tone", "warn");
        dot.textContent = "网关响应异常 (" + r.status + ")";
      }
    } catch (_e) {
      dot.setAttribute("data-tone", "neutral");
      dot.textContent = "离线预览";
    }
  }

  function openLogin(target) {
    state.targetAfterLogin = target || PAY_CONSOLE;
    state.needsTotp = false;
    $("totpWrap").hidden = true;
    $("loginTotp").value = "";
    setAlert($("loginAlert"), "");
    var dlg = $("loginDialog");
    if (dlg && typeof dlg.show === "function") {
      dlg.show();
    } else if (dlg) {
      dlg.setAttribute("open", "");
    }
    setTimeout(function () {
      var u = $("loginUsername");
      if (u) u.focus();
    }, 30);
  }

  async function submitLogin(ev) {
    if (ev) ev.preventDefault();
    var username = ($("loginUsername").value || "").trim();
    var password = $("loginPassword").value || "";
    var totp = ($("loginTotp").value || "").trim();
    var targetSel = $("loginTarget");
    var dest = (targetSel && targetSel.value) || state.targetAfterLogin || PAY_CONSOLE;

    if (!username || !password) {
      setAlert($("loginAlert"), "请填写用户名和密码", "danger");
      return;
    }
    if (state.needsTotp && !totp) {
      setAlert($("loginAlert"), "该账号已开启两步验证，请输入 6 位动态验证码", "warn");
      $("loginTotp").focus();
      return;
    }

    var btn = $("btnSubmitLogin");
    setBusy(btn, true);
    setAlert($("loginAlert"), "");

    try {
      var r = await fetch("/auth/login", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ Username: username, Password: password, Totp: totp })
      });
      var body = {};
      try { body = await r.json(); } catch (_e) {}

      if (!r.ok) {
        if (body && body.mfaRequired) {
          state.needsTotp = true;
          $("totpWrap").hidden = false;
          setAlert($("loginAlert"), "密码验证通过，请输入验证器中的 6 位两步验证码", "info");
          $("loginTotp").focus();
          return;
        }
        var msg = (body && body.error) || ("登录失败 (" + r.status + ")");
        if (msg === "invalid credentials") msg = "用户名或密码错误，或账号已被停用";
        if (msg === "invalid totp code") msg = "两步验证码不正确或已过期，请重试";
        setAlert($("loginAlert"), msg, "danger");
        return;
      }

      await checkSession();
      if (dest === PLATFORM_CONSOLE && state.me && state.me.role !== "admin") {
        // 非系统管理员无权访问平台容器控制台，自动导向支付中心工作台
        dest = PAY_CONSOLE;
      }
      window.location.href = dest;
    } catch (e) {
      setAlert($("loginAlert"), "网络请求失败：" + (e.message || String(e)), "danger");
    } finally {
      setBusy(btn, false);
    }
  }

  async function doLogout() {
    try {
      await fetch("/auth/logout", { method: "POST", credentials: "same-origin" });
    } catch (_e) {}
    state.me = null;
    renderSession();
    if (typeof window.pfToast === "function") {
      window.pfToast("已退出登录", "info");
    }
  }

  function populateOrigins() {
    var origin = window.location.origin && window.location.origin !== "null"
      ? window.location.origin
      : "https://apipay.sanrun.fun";
    var nodes = document.querySelectorAll(".js-origin");
    for (var i = 0; i < nodes.length; i++) {
      nodes[i].textContent = origin;
    }
  }

  function bindEvents() {
    $("btnOpenLogin").addEventListener("click", function () {
      $("loginTarget").value = PAY_CONSOLE;
      openLogin(PAY_CONSOLE);
    });
    $("btnHeroPrimary").addEventListener("click", function () {
      if (state.me) {
        window.location.href = PAY_CONSOLE;
      } else {
        $("loginTarget").value = PAY_CONSOLE;
        openLogin(PAY_CONSOLE);
      }
    });
    $("btnEnterPay").addEventListener("click", function () {
      window.location.href = PAY_CONSOLE;
    });
    $("btnPlatformConsole").addEventListener("click", function () {
      window.location.href = PLATFORM_CONSOLE;
    });
    $("btnOpenapiConsole").addEventListener("click", function () {
      window.location.href = OPENAPI_CONSOLE;
    });
    $("btnLogout").addEventListener("click", doLogout);

    $("entryPayConsole").addEventListener("click", function (ev) {
      if (!state.me) {
        ev.preventDefault();
        $("loginTarget").value = PAY_CONSOLE;
        openLogin(PAY_CONSOLE);
      }
    });

    $("formLogin").addEventListener("submit", submitLogin);
    $("btnCancelLogin").addEventListener("click", function () {
      var dlg = $("loginDialog");
      if (dlg && typeof dlg.close === "function") dlg.close();
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    populateOrigins();
    bindEvents();
    checkSession();
    checkGatewayStatus();
  });
})();
