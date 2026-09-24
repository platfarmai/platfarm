/* Platfarm UI kit v1 — framework-agnostic Web Components (light DOM, zero deps) */
(function () {
  "use strict";

  function attrBool(el, name) {
    return el.hasAttribute(name);
  }

  function setAttrBool(el, name, on) {
    if (on) el.setAttribute(name, "");
    else el.removeAttribute(name);
  }

  function bindOnce(el, type, handler, key) {
    var storeKey = "_pf_" + key;
    if (el[storeKey]) el.removeEventListener(type, el[storeKey]);
    el[storeKey] = handler;
    el.addEventListener(type, handler);
  }

  function unbind(el, type, key) {
    var storeKey = "_pf_" + key;
    if (el[storeKey]) {
      el.removeEventListener(type, el[storeKey]);
      el[storeKey] = null;
    }
  }

  function clearChildren(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  function collectText(el, skip) {
    var parts = [];
    Array.prototype.forEach.call(el.childNodes, function (n) {
      if (skip && n === skip) return;
      if (n.nodeType === 3) {
        var t = n.textContent;
        if (t && t.trim()) parts.push(t);
      }
    });
    return parts.join("").trim();
  }

  /* ---------- pf-button ---------- */
  class PfButton extends HTMLElement {
    static get observedAttributes() {
      return ["variant", "size", "loading", "disabled", "type"];
    }

    connectedCallback() {
      this._ensure();
      this._observe();
      this._harvestLabel();
      this._render();
    }

    disconnectedCallback() {
      if (this._mo) {
        this._mo.disconnect();
        this._mo = null;
      }
    }

    attributeChangedCallback() {
      if (this._btn) this._render();
    }

    _ensure() {
      if (this._btn && this.contains(this._btn)) return;
      var existing = collectText(this, this._btn);
      clearChildren(this);
      this._btn = document.createElement("button");
      this._btn.type = this.getAttribute("type") || "button";
      this._label = document.createElement("span");
      this._label.className = "pf-btn-label";
      this._spin = document.createElement("span");
      this._spin.className = "pf-btn-spin";
      this._spin.hidden = true;
      this._btn.appendChild(this._spin);
      this._btn.appendChild(this._label);
      this.appendChild(this._btn);
      if (existing) this._label.textContent = existing;
      bindOnce(this._btn, "click", this._onClick.bind(this), "click");
    }

    _observe() {
      if (this._mo) return;
      var self = this;
      this._mo = new MutationObserver(function () {
        if (!self._btn || !self.contains(self._btn)) {
          self._ensure();
          self._render();
          return;
        }
        self._harvestLabel();
      });
      this._mo.observe(this, { childList: true, characterData: true, subtree: true });
    }

    _harvestLabel() {
      if (!this._label) return;
      var extra = collectText(this, this._btn);
      if (extra) {
        this._label.textContent = extra;
        Array.prototype.slice.call(this.childNodes).forEach(function (n) {
          if (n !== this._btn && n.nodeType === 3) this.removeChild(n);
        }.bind(this));
      }
    }

    _onClick(e) {
      if (attrBool(this, "disabled") || attrBool(this, "loading")) {
        e.preventDefault();
        e.stopPropagation();
        e.stopImmediatePropagation();
      }
    }

    _render() {
      if (!this._btn) return;
      var loading = attrBool(this, "loading");
      this._btn.disabled = attrBool(this, "disabled") || loading;
      this._btn.type = this.getAttribute("type") || "button";
      this._spin.hidden = !loading;
    }

    get disabled() { return attrBool(this, "disabled"); }
    set disabled(v) { setAttrBool(this, "disabled", !!v); }
    get loading() { return attrBool(this, "loading"); }
    set loading(v) { setAttrBool(this, "loading", !!v); }
  }

  /* ---------- pf-badge ---------- */
  class PfBadge extends HTMLElement {
    static get observedAttributes() {
      return ["tone"];
    }

    connectedCallback() {
      if (this._pill && this.contains(this._pill)) return;
      var text = collectText(this) || (this._pill && this._pill.textContent) || "";
      clearChildren(this);
      this._pill = document.createElement("span");
      this._pill.className = "pf-badge-pill";
      this._pill.textContent = text;
      this.appendChild(this._pill);
    }
  }

  /* ---------- pf-alert ---------- */
  class PfAlert extends HTMLElement {
    static get observedAttributes() {
      return ["tone", "closable"];
    }

    connectedCallback() {
      if (!this._box) {
        var content = Array.prototype.slice.call(this.childNodes);
        clearChildren(this);
        this._box = document.createElement("div");
        this._box.className = "pf-alert-box";
        this._body = document.createElement("div");
        this._body.className = "pf-alert-body";
        content.forEach(function (n) { this._body.appendChild(n); }.bind(this));
        this._box.appendChild(this._body);
        this.appendChild(this._box);
      }
      this._renderClose();
    }

    attributeChangedCallback(name) {
      if (name === "closable" && this._box) this._renderClose();
    }

    _renderClose() {
      var want = attrBool(this, "closable");
      if (want && !this._close) {
        this._close = document.createElement("button");
        this._close.type = "button";
        this._close.className = "pf-alert-close";
        this._close.setAttribute("aria-label", "关闭");
        this._close.textContent = "×";
        bindOnce(this._close, "click", function () { this.remove(); }.bind(this), "close");
        this._box.appendChild(this._close);
      } else if (!want && this._close) {
        this._close.remove();
        this._close = null;
      }
    }
  }

  /* ---------- pf-tabs / pf-tab ---------- */
  class PfTab extends HTMLElement {}

  class PfTabs extends HTMLElement {
    static get observedAttributes() {
      return ["active", "hash"];
    }

    connectedCallback() {
      if (!this._bar) {
        this._bar = document.createElement("div");
        this._bar.className = "pf-tab-bar";
        this._bar.setAttribute("role", "tablist");
        this.insertBefore(this._bar, this.firstChild);
      } else if (this._bar.parentNode !== this) {
        this.insertBefore(this._bar, this.firstChild);
      }
      this._rebuild();
      this._bindHash();
    }

    disconnectedCallback() {
      unbind(window, "hashchange", this._hashKey());
    }

    attributeChangedCallback(name) {
      if (!this._bar) return;
      if (name === "active") this._sync();
      if (name === "hash") this._bindHash();
    }

    _hashKey() {
      if (!this._pfHashKey) this._pfHashKey = "tabsHash";
      return this._pfHashKey;
    }

    _bindHash() {
      unbind(window, "hashchange", this._hashKey());
      if (!attrBool(this, "hash")) return;
      bindOnce(window, "hashchange", this._applyHash.bind(this), this._hashKey());
      this._applyHash();
    }

    _tabs() {
      return Array.prototype.filter.call(this.children, function (el) {
        return el.tagName === "PF-TAB";
      });
    }

    _rebuild() {
      clearChildren(this._bar);
      var tabs = this._tabs();
      var active = this.getAttribute("active") || (tabs[0] && tabs[0].getAttribute("name")) || "";
      if (!this.getAttribute("active") && active) this.setAttribute("active", active);
      tabs.forEach(function (tab) {
        var name = tab.getAttribute("name") || "";
        var label = tab.getAttribute("label") || name;
        var btn = document.createElement("button");
        btn.type = "button";
        btn.className = "pf-tab-btn";
        btn.setAttribute("role", "tab");
        btn.setAttribute("data-tab", name);
        btn.textContent = label;
        bindOnce(btn, "click", function () {
          this._activate(name, true);
        }.bind(this), "tab_" + name);
        this._bar.appendChild(btn);
      }.bind(this));
      this._sync();
    }

    _activate(name, fromUser) {
      var prev = this.getAttribute("active");
      this.setAttribute("active", name);
      this._sync();
      if (prev !== name) {
        this.dispatchEvent(new CustomEvent("pf-change", {
          bubbles: true,
          detail: { name: name }
        }));
      }
      if (fromUser && attrBool(this, "hash") && location.hash !== "#" + name) {
        location.hash = name;
      }
    }

    _sync() {
      var active = this.getAttribute("active") || "";
      Array.prototype.forEach.call(this._bar.querySelectorAll(".pf-tab-btn"), function (btn) {
        btn.setAttribute("aria-selected", btn.getAttribute("data-tab") === active ? "true" : "false");
      });
      this._tabs().forEach(function (tab) {
        var on = tab.getAttribute("name") === active;
        if (on) {
          tab.setAttribute("active", "");
          tab.setAttribute("data-active", "true");
        } else {
          tab.removeAttribute("active");
          tab.removeAttribute("data-active");
        }
      });
    }

    _applyHash() {
      var h = (location.hash || "").replace(/^#/, "");
      if (!h) return;
      var hit = this._tabs().some(function (t) { return t.getAttribute("name") === h; });
      if (hit) {
        this.setAttribute("active", h);
        this._sync();
      }
    }
  }

  /* ---------- pf-dialog ---------- */
  class PfDialog extends HTMLElement {
    static get observedAttributes() {
      return ["title", "open"];
    }

    connectedCallback() {
      this._ensure();
      this._titleEl.textContent = this.getAttribute("title") || "";
      bindOnce(document, "keydown", this._onKey.bind(this), this._keyId());
      if (attrBool(this, "open")) this.show();
    }

    disconnectedCallback() {
      unbind(document, "keydown", this._keyId());
    }

    _keyId() {
      if (!this._pfKeyId) this._pfKeyId = "dlgKey";
      return this._pfKeyId;
    }

    _ensure() {
      if (this._panel) return;
      var kids = Array.prototype.slice.call(this.childNodes);
      clearChildren(this);
      this._overlay = document.createElement("div");
      this._overlay.className = "pf-dialog-overlay";
      this._panel = document.createElement("div");
      this._panel.className = "pf-dialog-panel";
      this._panel.setAttribute("role", "dialog");
      this._panel.setAttribute("aria-modal", "true");
      this._head = document.createElement("div");
      this._head.className = "pf-dialog-head";
      this._titleEl = document.createElement("h3");
      this._titleEl.className = "pf-dialog-title";
      this._x = document.createElement("button");
      this._x.type = "button";
      this._x.className = "pf-dialog-x";
      this._x.setAttribute("aria-label", "关闭");
      this._x.textContent = "×";
      this._head.appendChild(this._titleEl);
      this._head.appendChild(this._x);
      this._body = document.createElement("div");
      this._body.className = "pf-dialog-body";
      kids.forEach(function (n) { this._body.appendChild(n); }.bind(this));
      this._panel.appendChild(this._head);
      this._panel.appendChild(this._body);
      this.appendChild(this._overlay);
      this.appendChild(this._panel);
      bindOnce(this._overlay, "click", function () { this.close(); }.bind(this), "overlay");
      bindOnce(this._x, "click", function () { this.close(); }.bind(this), "x");
    }

    attributeChangedCallback(name, _o, _n) {
      if (name === "title" && this._titleEl) {
        this._titleEl.textContent = this.getAttribute("title") || "";
      }
      if (name === "open" && this._panel) {
        if (attrBool(this, "open")) {
          /* already open via attr */
        } else if (this._wasOpen) {
          this._wasOpen = false;
        }
      }
    }

    _onKey(e) {
      if (!attrBool(this, "open")) return;
      if (e.key === "Escape") this.close();
    }

    show() {
      this._ensure();
      this._wasOpen = true;
      setAttrBool(this, "open", true);
    }

    close(silent) {
      var was = attrBool(this, "open");
      setAttrBool(this, "open", false);
      this._wasOpen = false;
      if (was && !silent) {
        this.dispatchEvent(new CustomEvent("pf-close", { bubbles: true }));
      }
    }
  }

  /* ---------- pf-secret ---------- */
  class PfSecret extends HTMLElement {
    static get observedAttributes() {
      return ["label"];
    }

    connectedCallback() {
      if (!this._box) {
        var value = (this.textContent || "").trim();
        clearChildren(this);
        this._valueText = value;
        this._box = document.createElement("div");
        this._box.className = "pf-secret-box";
        this._lab = document.createElement("div");
        this._lab.className = "pf-secret-label";
        this._row = document.createElement("div");
        this._row.className = "pf-secret-row";
        this._val = document.createElement("div");
        this._val.className = "pf-secret-value";
        this._copy = document.createElement("button");
        this._copy.type = "button";
        this._copy.className = "pf-secret-copy";
        this._copy.textContent = "复制";
        this._row.appendChild(this._val);
        this._row.appendChild(this._copy);
        this._box.appendChild(this._lab);
        this._box.appendChild(this._row);
        this.appendChild(this._box);
        bindOnce(this._copy, "click", this._doCopy.bind(this), "copy");
        bindOnce(this._copy, "focus", function () {}, "focusnoop");
      }
      this._lab.textContent = this.getAttribute("label") || "密钥";
      this._val.textContent = this._valueText || "";
    }

    attributeChangedCallback() {
      if (this._lab) this._lab.textContent = this.getAttribute("label") || "密钥";
    }

    _doCopy() {
      var text = this._valueText || "";
      var done = function () {
        var prev = this._copy.textContent;
        this._copy.textContent = "已复制";
        setTimeout(function () { this._copy.textContent = prev; }.bind(this), 1200);
      }.bind(this);
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(done).catch(function () {
          fallbackCopy(text);
          done();
        });
      } else {
        fallbackCopy(text);
        done();
      }
    }
  }

  function fallbackCopy(text) {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.left = "-9999px";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); } catch (e) { /* ignore */ }
    document.body.removeChild(ta);
  }

  /* ---------- pf-pager ---------- */
  class PfPager extends HTMLElement {
    static get observedAttributes() {
      return ["total", "limit", "offset"];
    }

    connectedCallback() {
      if (!this._info) {
        this._info = document.createElement("span");
        this._actions = document.createElement("div");
        this._actions.className = "pf-pager-actions";
        this._prev = document.createElement("button");
        this._prev.type = "button";
        this._prev.textContent = "上一页";
        this._next = document.createElement("button");
        this._next.type = "button";
        this._next.textContent = "下一页";
        this._actions.appendChild(this._prev);
        this._actions.appendChild(this._next);
        this.appendChild(this._info);
        this.appendChild(this._actions);
        bindOnce(this._prev, "click", function () {
          var limit = this._num("limit", 20);
          var offset = Math.max(0, this._num("offset", 0) - limit);
          this.setAttribute("offset", String(offset));
          this.dispatchEvent(new CustomEvent("pf-change", { bubbles: true, detail: { offset: offset } }));
        }.bind(this), "prev");
        bindOnce(this._next, "click", function () {
          var limit = this._num("limit", 20);
          var total = this._num("total", 0);
          var offset = this._num("offset", 0) + limit;
          if (offset >= total) return;
          this.setAttribute("offset", String(offset));
          this.dispatchEvent(new CustomEvent("pf-change", { bubbles: true, detail: { offset: offset } }));
        }.bind(this), "next");
      }
      this._render();
    }

    attributeChangedCallback() {
      if (this._info) this._render();
    }

    _num(name, fallback) {
      var n = parseInt(this.getAttribute(name), 10);
      return isNaN(n) ? fallback : n;
    }

    _render() {
      var total = Math.max(0, this._num("total", 0));
      var limit = Math.max(1, this._num("limit", 20));
      var offset = Math.max(0, this._num("offset", 0));
      var from = total === 0 ? 0 : offset + 1;
      var to = Math.min(total, offset + limit);
      this._info.textContent = "共 " + total + " 条 · " + from + "–" + to;
      this._prev.disabled = offset <= 0;
      this._next.disabled = offset + limit >= total;
    }
  }

  /* ---------- pf-empty ---------- */
  class PfEmpty extends HTMLElement {
    connectedCallback() {
      if (this._icon) return;
      var text = this.textContent;
      clearChildren(this);
      this._icon = document.createElement("div");
      this._icon.className = "pf-empty-icon";
      this._icon.setAttribute("aria-hidden", "true");
      var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
      svg.setAttribute("viewBox", "0 0 40 40");
      svg.setAttribute("fill", "none");
      var rect = document.createElementNS("http://www.w3.org/2000/svg", "rect");
      rect.setAttribute("x", "6");
      rect.setAttribute("y", "10");
      rect.setAttribute("width", "28");
      rect.setAttribute("height", "20");
      rect.setAttribute("rx", "3");
      rect.setAttribute("stroke", "currentColor");
      rect.setAttribute("stroke-width", "1.5");
      var path = document.createElementNS("http://www.w3.org/2000/svg", "path");
      path.setAttribute("d", "M12 18h16M12 23h10");
      path.setAttribute("stroke", "currentColor");
      path.setAttribute("stroke-width", "1.5");
      path.setAttribute("stroke-linecap", "round");
      svg.appendChild(rect);
      svg.appendChild(path);
      this._icon.appendChild(svg);
      this._msg = document.createElement("div");
      this._msg.textContent = text;
      this.appendChild(this._icon);
      this.appendChild(this._msg);
    }
  }

  /* ---------- pf-spinner ---------- */
  class PfSpinner extends HTMLElement {
    static get observedAttributes() {
      return ["size"];
    }

    connectedCallback() {
      if (this._spin) return;
      this._spin = document.createElement("span");
      this._spin.className = "pf-spin";
      this._spin.setAttribute("aria-hidden", "true");
      this.appendChild(this._spin);
      this.setAttribute("role", "status");
      this.setAttribute("aria-label", "加载中");
    }
  }

  /* ---------- pf-topbar ---------- */
  class PfTopbar extends HTMLElement {
    static get observedAttributes() {
      return ["title"];
    }

    connectedCallback() {
      if (!this._inner) {
        var actions = Array.prototype.slice.call(this.childNodes);
        clearChildren(this);
        this._inner = document.createElement("div");
        this._inner.className = "pf-topbar-inner";
        this._titleEl = document.createElement("h1");
        this._titleEl.className = "pf-topbar-title";
        this._actions = document.createElement("div");
        this._actions.className = "pf-topbar-actions";
        actions.forEach(function (n) {
          if (n.nodeType === 3 && !(n.textContent || "").trim()) return;
          this._actions.appendChild(n);
        }.bind(this));
        this._inner.appendChild(this._titleEl);
        this._inner.appendChild(this._actions);
        this.appendChild(this._inner);
      }
      this._titleEl.textContent = this.getAttribute("title") || "";
    }

    attributeChangedCallback() {
      if (this._titleEl) this._titleEl.textContent = this.getAttribute("title") || "";
    }
  }

  /* ---------- toast ---------- */
  function toastHost() {
    var host = document.querySelector(".pf-toast-host");
    if (!host) {
      host = document.createElement("div");
      host.className = "pf-toast-host";
      (document.body || document.documentElement).appendChild(host);
    }
    return host;
  }

  function pfToast(message, tone, ms) {
    tone = tone || "ok";
    ms = ms == null ? 3000 : ms;
    var el = document.createElement("div");
    el.className = "pf-toast";
    el.setAttribute("data-tone", tone);
    el.textContent = String(message == null ? "" : message);
    toastHost().appendChild(el);
    setTimeout(function () {
      if (el.parentNode) el.parentNode.removeChild(el);
    }, ms);
    return el;
  }

  /* ---------- confirm ---------- */
  function pfConfirm(message, opts) {
    opts = opts || {};
    return new Promise(function (resolve) {
      var dlg = document.createElement("pf-dialog");
      dlg.setAttribute("title", opts.title || "确认");

      var body = document.createElement("div");
      body.textContent = String(message == null ? "" : message);

      var actions = document.createElement("div");
      actions.className = "pf-dialog-actions";

      var cancel = document.createElement("pf-button");
      cancel.setAttribute("variant", "ghost");
      cancel.appendChild(document.createTextNode("取消"));

      var ok = document.createElement("pf-button");
      ok.setAttribute("variant", opts.danger ? "danger" : "primary");
      ok.appendChild(document.createTextNode("确认"));

      actions.appendChild(cancel);
      actions.appendChild(ok);
      dlg.appendChild(body);
      dlg.appendChild(actions);
      document.body.appendChild(dlg);

      var settled = false;
      function finish(v) {
        if (settled) return;
        settled = true;
        if (typeof dlg.close === "function") dlg.close(true);
        else setAttrBool(dlg, "open", false);
        if (dlg.parentNode) dlg.parentNode.removeChild(dlg);
        resolve(v);
      }

      dlg.addEventListener("pf-close", function () { finish(false); });
      cancel.addEventListener("click", function () { finish(false); });
      ok.addEventListener("click", function () { finish(true); });

      customElements.whenDefined("pf-dialog").then(function () {
        dlg.show();
      });
    });
  }

  function define(name, ctor) {
    if (!customElements.get(name)) customElements.define(name, ctor);
  }

  define("pf-button", PfButton);
  define("pf-badge", PfBadge);
  define("pf-alert", PfAlert);
  define("pf-tab", PfTab);
  define("pf-tabs", PfTabs);
  define("pf-dialog", PfDialog);
  define("pf-secret", PfSecret);
  define("pf-pager", PfPager);
  define("pf-empty", PfEmpty);
  define("pf-spinner", PfSpinner);
  define("pf-topbar", PfTopbar);

  window.pfToast = pfToast;
  window.pfConfirm = pfConfirm;
})();

