<script setup>
// 表单字段：标题 + 控件 + 填写说明。说明固定放在控件下方，写多长都不会挤占标题行。
// 说明文字较短用 hint 属性，需要换行或强调时用 #hint 插槽；标题里要带额外标记时用 #label 插槽。
defineProps({
  label: { type: String, default: "" },
  hint: { type: String, default: "" },
  required: { type: Boolean, default: false },
});
</script>

<template>
  <label class="flex flex-col gap-1 text-[13px]">
    <span><slot name="label">{{ label }}</slot><span v-if="required" class="text-danger" title="必填"> *</span></span>
    <slot />
    <span v-if="hint || $slots.hint" class="pf-muted"><slot name="hint">{{ hint }}</slot></span>
  </label>
</template>
