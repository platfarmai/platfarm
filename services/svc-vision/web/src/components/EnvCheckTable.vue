<template>
  <div v-loading="loading" class="env-check">
    <el-empty v-if="!report && !loading" :description="t('envCheck.emptyHint')" :image-size="60" />
    <template v-else-if="report">
      <el-alert
        v-if="!report.critical_ok"
        :title="t('envCheck.criticalFail', { n: failCount })"
        type="error" :closable="false" show-icon class="mb-12"
      />
      <el-alert
        v-else-if="!report.ok"
        :title="t('envCheck.warnHint', { n: warnCount })"
        type="warning" :closable="false" show-icon class="mb-12"
      />
      <el-alert
        v-else
        :title="t('envCheck.allPassed')"
        type="success" :closable="false" show-icon class="mb-12"
      />

      <el-table :data="report.checks" size="small" :show-header="false">
        <el-table-column width="44">
          <template #default="{ row }">
            <el-icon v-if="row.status === 'ok'" :size="20" color="#67c23a"><CircleCheck /></el-icon>
            <el-icon v-else-if="row.status === 'warn'" :size="20" color="#e6a23c"><Warning /></el-icon>
            <el-icon v-else :size="20" color="#f56c6c"><CircleClose /></el-icon>
</template>
        </el-table-column>
        <el-table-column :label="t('envCheck.checkItem')">
          <template #default="{ row }">
            <div>
              <span :class="['env-name', row.status]">{{ row.name }}</span>
              <el-tag v-if="row.required && row.status !== 'ok'" size="small" type="danger" effect="plain" class="ml-6">{{ t('envCheck.criticalTag') }}</el-tag>
            </div>
            <div class="env-detail">{{ row.detail }}</div>
            <div v-if="row.fix && row.status !== 'ok'" class="env-fix">
              <el-icon class="mr-4"><Tools /></el-icon>{{ row.fix }}
            </div>
</template>
        </el-table-column>
      </el-table>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { CircleCheck, CircleClose, Warning, Tools } from '@element-plus/icons-vue'

const { t } = useI18n()

interface CheckItem {
  name: string
  status: 'ok' | 'warn' | 'fail'
  detail: string
  fix?: string
  required?: boolean
}
interface EnvReport {
  ok: boolean
  critical_ok: boolean
  checks: CheckItem[]
}

const props = defineProps<{
  report: EnvReport | null
  loading?: boolean
}>()

const failCount = computed(() => props.report?.checks.filter(c => c.status === 'fail').length || 0)
const warnCount = computed(() => props.report?.checks.filter(c => c.status === 'warn').length || 0)
</script>

<style scoped>
.env-check {
  width: 100%;
}
.mb-12 {
  margin-bottom: 12px;
}
.env-name {
  font-weight: 600;
  font-size: 14px;
}
.env-name.ok {
  color: #303133;
}
.env-name.warn {
  color: #e6a23c;
}
.env-name.fail {
  color: #f56c6c;
}
.env-detail {
  color: #909399;
  font-size: 12px;
  margin-top: 2px;
  word-break: break-all;
}
.env-fix {
  color: #606266;
  font-size: 12px;
  margin-top: 4px;
  background: #f5f7fa;
  border-left: 3px solid #409eff;
  padding: 6px 8px;
  border-radius: 4px;
  line-height: 1.6;
}
.ml-6 {
  margin-left: 6px;
}
.mr-4 {
  margin-right: 4px;
  vertical-align: -2px;
}
</style>
