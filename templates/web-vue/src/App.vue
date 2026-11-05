<script setup>
import { ref, onMounted } from "vue";

const err = ref("");
const me = ref(null);

async function load() {
  err.value = "";
  try {
    const r = await fetch("/auth/me", { credentials: "same-origin" });
    if (!r.ok) throw new Error(`HTTP ${r.status}（请先在 /platform/console/ 登录）`);
    me.value = await r.json();
  } catch (e) { err.value = e.message || String(e); }
}

onMounted(load);
</script>

<template>
  <div>
    <pf-topbar title="svc-hello 后台">
      <pf-theme-toggle></pf-theme-toggle>
      <pf-button variant="default" size="sm" @click="load">刷新</pf-button>
    </pf-topbar>

    <div class="pf-container">
      <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>

      <div class="pf-card flex flex-col gap-3">
        <h2 class="m-0 text-[16px] font-semibold">当前身份</h2>
        <table class="pf-table">
          <thead>
            <tr><th>字段</th><th>值</th></tr>
          </thead>
          <tbody>
            <tr v-for="(v, k) in me" :key="k">
              <td><span class="pf-code">{{ k }}</span></td>
              <td>{{ v }}</td>
            </tr>
            <tr v-if="!me">
              <td colspan="2"><span class="pf-muted">（未登录或加载中）</span></td>
            </tr>
          </tbody>
        </table>
        <p class="pf-muted m-0">
          示例页：pf-ui 组件 + tw 工具类 + 同源 Cookie 会话。业务接口统一走
          <span class="pf-code">src/api.js</span>。
        </p>
      </div>
    </div>
  </div>
</template>

<!-- 布局一律用 pf-ui 组件类 + tw 工具类，默认不写局部 <style>；
     确需状态选择器（如 .x[data-on]）才开一个最小 style 块。 -->
