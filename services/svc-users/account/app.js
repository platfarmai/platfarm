(function () {
  "use strict";

  var TOKEN_KEY = "pf_account_access";
  var TABS = ["login", "register", "forgot", "security"];
  var MSG_TONE = { error: "danger", ok: "ok", info: "info" };

  var state = {
    token: null,
    me: null,
    loginNeedsTotp: false
  };

  function $(id) {
    return document.getElementById(id);
  }

  function qs(sel, root) {
    return (root || document).querySelector(sel);
  }

  function qsa(sel, root) {
    return Array.prototype.slice.call((root || document).querySelectorAll(sel));
  }

  function loadToken() {
    try {
      return sessionStorage.getItem(TOKEN_KEY) || null;
    } catch (e) {
      return null;
    }
  }

  function saveToken(token) {
    state.token = token || null;
    try {
      if (token) sessionStorage.setItem(TOKEN_KEY, token);
      else sessionStorage.removeItem(TOKEN_KEY);
    } catch (e) { /* ignore quota / private mode */ }
  }

  function clearAuth() {
    saveToken(null);
    state.me = null;
    state.loginNeedsTotp = false;
  }

  function alertBody(el) {
    return el.querySelector(".pf-alert-body") || el;
  }

  function setMsg(el, text, kind) {
    if (!el) return;
    if (!text) {
      el.hidden = true;
      alertBody(el).textContent = "";
      return;
    }
    el.hidden = false;
    if (kind && MSG_TONE[kind]) el.setAttribute("tone", MSG_TONE[kind]);
    else el.removeAttribute("tone");
    alertBody(el).textContent = text;
  }

  function buttonLabelEl(btn) {
    return btn.querySelector(".pf-btn-label") || btn;
  }

  function setBusy(btn, busy, label) {
    if (!btn) return;
    if (btn.tagName === "PF-BUTTON") {
      if (busy) {
        if (!btn.dataset.label) btn.dataset.label = buttonLabelEl(btn).textContent;
        btn.setAttribute("loading", "");
        if (label) buttonLabelEl(btn).textContent = label;
      } else {
        btn.removeAttribute("loading");
        if (btn.dataset.label) buttonLabelEl(btn).textContent = btn.dataset.label;
        delete btn.dataset.label;
      }
      return;
    }
    if (busy) {
      if (!btn.dataset.label) btn.dataset.label = btn.textContent;
      btn.disabled = true;
      btn.textContent = label || "处理中…";
    } else {
      btn.disabled = false;
      btn.textContent = btn.dataset.label || btn.textContent;
      delete btn.dataset.label;
    }
  }

  function parseJsonSafe(text) {
    if (!text) return {};
    try {
      return JSON.parse(text);
    } catch (e) {
      return {};
    }
  }

  function errText(data, fallback) {
    if (data && typeof data.error === "string" && data.error) return data.error;
    if (data && typeof data.note === "string" && data.note) return data.note;
    return fallback || "请求失败";
  }

  function api(path, options) {
    options = options || {};
    var headers = { Accept: "application/json" };
    if (options.body != null) headers["Content-Type"] = "application/json";
    if (state.token) headers.Authorization = "Bearer " + state.token;
    if (options.headers) {
      Object.keys(options.headers).forEach(function (k) {
        headers[k] = options.headers[k];
      });
    }
    return fetch(path, {
      method: options.method || "GET",
      headers: headers,
      credentials: "same-origin",
      body: options.body != null ? JSON.stringify(options.body) : undefined
    }).then(function (res) {
      return res.text().then(function (text) {
        var data = parseJsonSafe(text);
        return { ok: res.ok, status: res.status, data: data, raw: text };
      });
    });
  }

  function currentTab() {
    var hash = (location.hash || "").replace(/^#/, "").toLowerCase();
    if (TABS.indexOf(hash) >= 0) return hash;
    return "login";
  }

  function showTab(name) {
    if (TABS.indexOf(name) < 0) name = "login";
    qsa(".tab").forEach(function (a) {
      var on = a.getAttribute("data-tab") === name;
      a.classList.toggle("active", on);
      a.setAttribute("aria-selected", on ? "true" : "false");
    });
    qsa(".view").forEach(function (sec) {
      var on = sec.getAttribute("data-view") === name;
      sec.hidden = !on;
    });
    if (name === "security") refreshSecurity();
  }

  function go(tab) {
    if (location.hash !== "#" + tab) location.hash = tab;
    else showTab(tab);
  }

  function updateSessionBar() {
    var bar = $("sessionBar");
    var label = $("sessionLabel");
    if (state.me) {
      bar.hidden = false;
      label.textContent = state.me.username + " · " + (state.me.role || "user");
    } else if (state.token) {
      bar.hidden = false;
      label.textContent = "已登录";
    } else {
      bar.hidden = true;
      label.textContent = "";
    }
  }

  function badge(ok, yes, no, offTone) {
    return '<pf-badge tone="' + (ok ? "ok" : (offTone || "neutral")) + '">' +
      (ok ? yes : no) + "</pf-badge>";
  }

  function renderIdentity() {
    var dl = $("identityMeta");
    var me = state.me;
    if (!me) {
      dl.innerHTML = "";
      return;
    }
    var email = me.email || "（未绑定）";
    var rows = [
      ["用户 ID", String(me.userId != null ? me.userId : "—")],
      ["用户名", me.username || "—"],
      ["角色", me.role || "—"],
      ["租户 ID", String(me.tenantId != null ? me.tenantId : 0)],
      ["邮箱", email + " " + badge(!!me.emailVerified, "已验证", "未验证", "warn")],
      ["两步验证", badge(!!me.totpEnabled, "已开启", "未开启", "neutral")]
    ];
    dl.innerHTML = rows.map(function (r) {
      return "<dt>" + r[0] + "</dt><dd>" + r[1] + "</dd>";
    }).join("");

    var hint = $("totpStatusHint");
    var setup = $("totpSetupBlock");
    var disableForm = $("formTotpDisable");
    if (me.totpEnabled) {
      hint.textContent = "两步验证已开启。关闭时需提供当前验证器中的有效动态码。";
      setup.hidden = true;
      disableForm.hidden = false;
    } else {
      hint.textContent = "尚未开启。点击「生成密钥」，将 secret / otpauth URL 录入验证器，再提交一次动态码完成启用。";
      setup.hidden = false;
      disableForm.hidden = true;
    }
  }

  function setSecurityVisible(loggedIn) {
    $("securityGate").hidden = !!loggedIn;
    $("securityBody").hidden = !loggedIn;
  }

  function ensureBearer(msgEl) {
    if (state.token) return true;
    setMsg(msgEl, "当前仅有会话 Cookie，邮箱验证与改密等操作需要本页登录令牌，请重新登录", "info");
    return false;
  }

  function applyLoggedIn(pair) {
    if (pair && pair.accessToken) saveToken(pair.accessToken);
    state.loginNeedsTotp = false;
    $("loginTotpWrap").hidden = true;
    var totpInput = qs('#formLogin input[name="totp"]');
    if (totpInput) totpInput.value = "";
    return fetchMe().then(function (ok) {
      updateSessionBar();
      if (ok) go("security");
      return ok;
    });
  }

  function fetchMe() {
    return api("/auth/me").then(function (res) {
      if (!res.ok) {
        state.me = null;
        if (res.status === 401) {
          // Cookie may still work later; keep token if present but mark unauthenticated UI
          if (!state.token) clearAuth();
        }
        setSecurityVisible(false);
        updateSessionBar();
        return false;
      }
      state.me = res.data;
      setSecurityVisible(true);
      renderIdentity();
      updateSessionBar();
      return true;
    }).catch(function () {
      state.me = null;
      setSecurityVisible(false);
      updateSessionBar();
      return false;
    });
  }

  function refreshSecurity() {
    fetchMe();
  }

  /* ── Login ─────────────────────────────────────────── */

  function onLogin(e) {
    e.preventDefault();
    var form = e.target;
    var msg = $("loginMsg");
    var btn = $("btnLogin");
    var username = (form.username.value || "").trim();
    var password = form.password.value || "";
    var totp = (form.totp && form.totp.value || "").trim();

    if (!username || !password) {
      setMsg(msg, "请填写用户名和密码", "error");
      return;
    }
    if (state.loginNeedsTotp && !totp) {
      setMsg(msg, "请填写两步验证码", "error");
      return;
    }

    setBusy(btn, true, "登录中…");
    setMsg(msg, "");

    var body = { Username: username, Password: password };
    if (totp) body.Totp = totp;

    api("/auth/login", { method: "POST", body: body })
      .then(function (res) {
        if (res.ok && res.data && res.data.accessToken) {
          setMsg(msg, "登录成功", "ok");
          return applyLoggedIn(res.data);
        }
        if (res.status === 401 && res.data && res.data.mfaRequired) {
          state.loginNeedsTotp = true;
          $("loginTotpWrap").hidden = false;
          setMsg(msg, "需要两步验证码，请继续填写后重试", "info");
          var totpEl = qs('#formLogin input[name="totp"]');
          if (totpEl) totpEl.focus();
          return;
        }
        setMsg(msg, friendlyAuthError(res), "error");
      })
      .catch(function () {
        setMsg(msg, "网络异常，请稍后重试", "error");
      })
      .then(function () {
        setBusy(btn, false);
      });
  }

  function friendlyAuthError(res) {
    var e = errText(res.data, "");
    if (res.status === 401) {
      if (/totp/i.test(e)) return "两步验证码无效，请重试";
      if (/credential/i.test(e)) return "用户名或密码错误";
      return e || "认证失败";
    }
    if (res.status === 400) return e || "请求参数无效";
    return e || ("登录失败（" + res.status + "）");
  }

  /* ── Register ──────────────────────────────────────── */

  function onRegister(e) {
    e.preventDefault();
    var form = e.target;
    var msg = $("registerMsg");
    var btn = $("btnRegister");
    var username = (form.username.value || "").trim();
    var password = form.password.value || "";
    var email = (form.email.value || "").trim();

    if (username.length < 3) {
      setMsg(msg, "用户名至少 3 个字符", "error");
      return;
    }
    if (password.length < 8) {
      setMsg(msg, "密码至少 8 个字符", "error");
      return;
    }

    setBusy(btn, true, "注册中…");
    setMsg(msg, "");

    api("/auth/register", {
      method: "POST",
      body: { Username: username, Password: password, Email: email }
    })
      .then(function (res) {
        if (res.ok && res.data && res.data.accessToken) {
          setMsg(msg, "注册成功，已自动登录", "ok");
          return applyLoggedIn(res.data);
        }
        if (res.status === 403) {
          setMsg(msg, "平台未开放自助注册。请联系管理员开通账号。", "error");
          return;
        }
        if (res.status === 409) {
          setMsg(msg, "用户名或邮箱已被占用", "error");
          return;
        }
        if (res.status === 400) {
          setMsg(msg, errText(res.data, "请检查用户名、密码或邮箱格式"), "error");
          return;
        }
        setMsg(msg, errText(res.data, "注册失败（" + res.status + "）"), "error");
      })
      .catch(function () {
        setMsg(msg, "网络异常，请稍后重试", "error");
      })
      .then(function () {
        setBusy(btn, false);
      });
  }

  /* ── Forgot password ───────────────────────────────── */

  function onForgotSend(e) {
    e.preventDefault();
    var form = e.target;
    var msg = $("forgotSendMsg");
    var btn = $("btnForgotSend");
    var email = (form.email.value || "").trim();
    if (!email) {
      setMsg(msg, "请填写邮箱", "error");
      return;
    }

    setBusy(btn, true, "发送中…");
    setMsg(msg, "");

    api("/api/users/forgot", { method: "POST", body: { email: email } })
      .then(function (res) {
        if (res.ok) {
          setMsg(msg, errText(res.data, "若该邮箱已注册，验证码已发送"), "ok");
          var resetEmail = qs('#formForgotReset input[name="email"]');
          if (resetEmail && !resetEmail.value) resetEmail.value = email;
          return;
        }
        if (res.status === 400) {
          setMsg(msg, errText(res.data, "邮箱格式无效"), "error");
          return;
        }
        setMsg(msg, errText(res.data, "发送失败（" + res.status + "）"), "error");
      })
      .catch(function () {
        setMsg(msg, "网络异常，请稍后重试", "error");
      })
      .then(function () {
        setBusy(btn, false);
      });
  }

  function onForgotReset(e) {
    e.preventDefault();
    var form = e.target;
    var msg = $("forgotResetMsg");
    var btn = $("btnForgotReset");
    var email = (form.email.value || "").trim();
    var code = (form.code.value || "").trim();
    var newPassword = form.newPassword.value || "";

    if (!email || !code) {
      setMsg(msg, "请填写邮箱和验证码", "error");
      return;
    }
    if (newPassword.length < 8) {
      setMsg(msg, "新密码至少 8 个字符", "error");
      return;
    }

    setBusy(btn, true, "重置中…");
    setMsg(msg, "");

    api("/api/users/reset", {
      method: "POST",
      body: { email: email, code: code, newPassword: newPassword }
    })
      .then(function (res) {
        if (res.ok) {
          setMsg(msg, errText(res.data, "密码已更新，请登录"), "ok");
          clearAuth();
          updateSessionBar();
          setTimeout(function () { go("login"); }, 600);
          return;
        }
        if (res.status === 401) {
          setMsg(msg, "验证码无效或已过期", "error");
          return;
        }
        if (res.status === 400) {
          setMsg(msg, errText(res.data, "请检查邮箱、验证码与新密码"), "error");
          return;
        }
        setMsg(msg, errText(res.data, "重置失败（" + res.status + "）"), "error");
      })
      .catch(function () {
        setMsg(msg, "网络异常，请稍后重试", "error");
      })
      .then(function () {
        setBusy(btn, false);
      });
  }

  /* ── Security: email verify ────────────────────────── */

  function onVerifySend() {
    var msg = $("verifyMsg");
    var btn = $("btnVerifySend");
    if (!ensureBearer(msg)) return;
    setBusy(btn, true, "发送中…");
    setMsg(msg, "");

    api("/api/users/verify-email/send", { method: "POST", body: {} })
      .then(function (res) {
        if (res.ok) {
          setMsg(msg, errText(res.data, "验证码已发送"), "ok");
          return;
        }
        if (res.status === 400) {
          setMsg(msg, "当前账号未绑定邮箱", "error");
          return;
        }
        if (res.status === 429) {
          setMsg(msg, "发送过于频繁，请稍后再试", "error");
          return;
        }
        if (res.status === 401) {
          setMsg(msg, "登录已失效，请重新登录", "error");
          clearAuth();
          updateSessionBar();
          setSecurityVisible(false);
          return;
        }
        setMsg(msg, errText(res.data, "发送失败（" + res.status + "）"), "error");
      })
      .catch(function () {
        setMsg(msg, "网络异常，请稍后重试", "error");
      })
      .then(function () {
        setBusy(btn, false);
      });
  }

  function onVerifyConfirm(e) {
    e.preventDefault();
    var form = e.target;
    var msg = $("verifyMsg");
    var btn = $("btnVerifyConfirm");
    if (!ensureBearer(msg)) return;
    var code = (form.code.value || "").trim();
    if (!code) {
      setMsg(msg, "请填写验证码", "error");
      return;
    }

    setBusy(btn, true, "验证中…");
    setMsg(msg, "");

    api("/api/users/verify-email/confirm", { method: "POST", body: { code: code } })
      .then(function (res) {
        if (res.ok) {
          setMsg(msg, errText(res.data, "邮箱已验证"), "ok");
          form.reset();
          return fetchMe();
        }
        if (res.status === 401) {
          setMsg(msg, errText(res.data, "验证码无效或已过期"), "error");
          return;
        }
        setMsg(msg, errText(res.data, "验证失败（" + res.status + "）"), "error");
      })
      .catch(function () {
        setMsg(msg, "网络异常，请稍后重试", "error");
      })
      .then(function () {
        setBusy(btn, false);
      });
  }

  /* ── Change password ───────────────────────────────── */

  function onChangePassword(e) {
    e.preventDefault();
    var form = e.target;
    var msg = $("changePwdMsg");
    var btn = $("btnChangePwd");
    if (!ensureBearer(msg)) return;
    var oldPassword = form.oldPassword.value || "";
    var newPassword = form.newPassword.value || "";

    if (!oldPassword || !newPassword) {
      setMsg(msg, "请填写当前密码与新密码", "error");
      return;
    }
    if (newPassword.length < 8) {
      setMsg(msg, "新密码至少 8 个字符", "error");
      return;
    }

    setBusy(btn, true, "更新中…");
    setMsg(msg, "");

    api("/api/users/change-password", {
      method: "POST",
      body: { oldPassword: oldPassword, newPassword: newPassword }
    })
      .then(function (res) {
        if (res.ok) {
          setMsg(msg, "密码已更新，请重新登录", "ok");
          clearAuth();
          updateSessionBar();
          setSecurityVisible(false);
          form.reset();
          setTimeout(function () { go("login"); }, 500);
          return;
        }
        if (res.status === 401) {
          setMsg(msg, "当前密码不正确或登录已失效", "error");
          return;
        }
        setMsg(msg, errText(res.data, "修改失败（" + res.status + "）"), "error");
      })
      .catch(function () {
        setMsg(msg, "网络异常，请稍后重试", "error");
      })
      .then(function () {
        setBusy(btn, false);
      });
  }

  /* ── TOTP ──────────────────────────────────────────── */

  function onTotpSetup() {
    var msg = $("totpMsg");
    var btn = $("btnTotpSetup");
    setBusy(btn, true, "生成中…");
    setMsg(msg, "");

    api("/auth/totp/setup", { method: "POST", body: {} })
      .then(function (res) {
        if (res.ok && res.data) {
          $("totpSecretBox").hidden = false;
          $("totpSecret").value = res.data.secret || "";
          $("totpOtpauth").value = res.data.otpauthUrl || "";
          setMsg(msg, res.data.note || "请录入密钥后提交验证码以启用", "info");
          return;
        }
        if (res.status === 401) {
          setMsg(msg, "登录已失效，请重新登录", "error");
          return;
        }
        setMsg(msg, errText(res.data, "生成失败（" + res.status + "）"), "error");
      })
      .catch(function () {
        setMsg(msg, "网络异常，请稍后重试", "error");
      })
      .then(function () {
        setBusy(btn, false);
      });
  }

  function onTotpEnable(e) {
    e.preventDefault();
    var form = e.target;
    var msg = $("totpMsg");
    var btn = $("btnTotpEnable");
    var code = (form.code.value || "").trim();
    if (!code) {
      setMsg(msg, "请填写验证码", "error");
      return;
    }

    setBusy(btn, true, "启用中…");
    setMsg(msg, "");

    api("/auth/totp/enable", { method: "POST", body: { code: code } })
      .then(function (res) {
        if (res.ok) {
          setMsg(msg, "两步验证已启用", "ok");
          $("totpSecretBox").hidden = true;
          form.reset();
          return fetchMe();
        }
        if (res.status === 401) {
          setMsg(msg, "验证码无效", "error");
          return;
        }
        setMsg(msg, errText(res.data, "启用失败（" + res.status + "）"), "error");
      })
      .catch(function () {
        setMsg(msg, "网络异常，请稍后重试", "error");
      })
      .then(function () {
        setBusy(btn, false);
      });
  }

  function onTotpDisable(e) {
    e.preventDefault();
    var form = e.target;
    var msg = $("totpMsg");
    var btn = $("btnTotpDisable");
    var code = (form.code.value || "").trim();
    if (!code) {
      setMsg(msg, "请填写验证码", "error");
      return;
    }

    setBusy(btn, true, "关闭中…");
    setMsg(msg, "");

    api("/auth/totp/disable", { method: "POST", body: { code: code } })
      .then(function (res) {
        if (res.ok) {
          setMsg(msg, "两步验证已关闭", "ok");
          form.reset();
          return fetchMe();
        }
        if (res.status === 401) {
          setMsg(msg, "验证码无效", "error");
          return;
        }
        setMsg(msg, errText(res.data, "关闭失败（" + res.status + "）"), "error");
      })
      .catch(function () {
        setMsg(msg, "网络异常，请稍后重试", "error");
      })
      .then(function () {
        setBusy(btn, false);
      });
  }

  /* ── Logout / copy ─────────────────────────────────── */

  function onLogout() {
    var btn = $("btnLogout");
    setBusy(btn, true, "退出中…");
    api("/auth/logout", { method: "POST", body: {} })
      .catch(function () { /* still clear local state */ })
      .then(function () {
        clearAuth();
        updateSessionBar();
        setSecurityVisible(false);
        setBusy(btn, false);
        go("login");
      });
  }

  function onCopy(e) {
    var btn = e.target.closest("[data-copy]");
    if (!btn) return;
    var id = btn.getAttribute("data-copy");
    var input = $(id);
    if (!input || !input.value) return;
    var text = input.value;
    var labelEl = buttonLabelEl(btn);
    var done = function () {
      var prev = labelEl.textContent;
      labelEl.textContent = "已复制";
      setTimeout(function () { labelEl.textContent = prev; }, 1200);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done).catch(function () {
        input.select();
        try { document.execCommand("copy"); } catch (err) { /* ignore */ }
        done();
      });
    } else {
      input.select();
      try { document.execCommand("copy"); } catch (err) { /* ignore */ }
      done();
    }
  }

  function wireSubmitButtons() {
    qsa("pf-button[data-submit]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var form = btn.closest("form");
        if (!form) return;
        if (typeof form.requestSubmit === "function") form.requestSubmit();
        else form.dispatchEvent(new Event("submit", { cancelable: true, bubbles: true }));
      });
    });
  }

  /* ── Boot ──────────────────────────────────────────── */

  function bind() {
    $("formLogin").addEventListener("submit", onLogin);
    $("formRegister").addEventListener("submit", onRegister);
    $("formForgotSend").addEventListener("submit", onForgotSend);
    $("formForgotReset").addEventListener("submit", onForgotReset);
    $("btnVerifySend").addEventListener("click", onVerifySend);
    $("formVerifyConfirm").addEventListener("submit", onVerifyConfirm);
    $("formChangePassword").addEventListener("submit", onChangePassword);
    $("btnTotpSetup").addEventListener("click", onTotpSetup);
    $("formTotpEnable").addEventListener("submit", onTotpEnable);
    $("formTotpDisable").addEventListener("submit", onTotpDisable);
    $("btnLogout").addEventListener("click", onLogout);
    $("btnRefreshMe").addEventListener("click", function () {
      setMsg($("totpMsg"), "");
      refreshSecurity();
    });
    var gateBtn = $("btnGateLogin");
    if (gateBtn) gateBtn.addEventListener("click", function () { go("login"); });
    wireSubmitButtons();
    document.addEventListener("click", onCopy);
    window.addEventListener("hashchange", function () {
      showTab(currentTab());
    });
  }

  function boot() {
    state.token = loadToken();
    bind();
    showTab(currentTab());
    fetchMe().then(function (ok) {
      if (ok && currentTab() !== "security" && !location.hash) {
        go("security");
      }
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
