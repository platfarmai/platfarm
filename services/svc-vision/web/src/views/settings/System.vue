<template>
  <div class="settings-page">
    <el-card :shadow="never">
      <template #header>
        <h3>{{ t('settingsSystem.basicSettings') }}</h3>
</template>
      <el-form :model="systemForm" label-width="140">
        <el-form-item :label="t('settingsSystem.systemName')">
          <el-input v-model="systemForm.name" :placeholder="t('settingsSystem.defaultSystemName')" style="width: 400px" />
        </el-form-item>
        <el-form-item :label="t('settingsSystem.httpPort')">
          <el-input-number v-model="systemForm.http_port" :min="1" :max="65535" :controls="false" style="width: 120px" />
        </el-form-item>
        <el-form-item :label="t('settingsSystem.wsPort')">
          <el-input-number v-model="systemForm.ws_port" :min="1" :max="65535" :controls="false" style="width: 120px" />
        </el-form-item>
        <el-form-item :label="t('settingsSystem.runMode')">
          <el-select v-model="systemForm.mode" :placeholder="t('settingsSystem.selectMode')" style="width: 200px">
            <el-option :label="t('settingsSystem.debugMode')" value="debug" />
            <el-option :label="t('settingsSystem.releaseMode')" value="release" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('settingsSystem.logLevel')">
          <el-select v-model="systemForm.log_level" :placeholder="t('settingsSystem.selectLevel')" style="width: 200px">
            <el-option label="Debug" value="debug" />
            <el-option label="Info" value="info" />
            <el-option label="Warn" value="warn" />
            <el-option label="Error" value="error" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="saveSystemConfig"><el-icon><Check /></el-icon> {{ t('settingsSystem.save') }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 数据库由 PlatFarm 平台托管（pf_svc_vision @ PostgreSQL），非业务可配置项 -->
  </div>
</template>

<script setup lang="ts">
import { reactive, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { Check } from '@element-plus/icons-vue'
import { api } from '@/api'

const { t } = useI18n()

const systemForm = reactive({
  name: t('settingsSystem.defaultSystemName'),
  http_port: 8080,
  ws_port: 8081,
  mode: 'release',
  log_level: 'info',
})

const loadSystemConfig = async () => {
  try {
    const res: any = await api.system.config()
    const srv = res.server || {}
    if (srv.name === undefined || srv.name === '') {
      systemForm.name = t('settingsSystem.defaultSystemName')
    }
    Object.assign(systemForm, srv)
    if (!systemForm.name) systemForm.name = t('settingsSystem.defaultSystemName')
  } catch (e) { console.error(e) }
}

const saveSystemConfig = async () => {
  try {
    const res: any = await api.system.updateConfig({ server: systemForm })
    if (res?.restart_required) {
      ElMessage.warning(t('settingsSystem.restartRequired'))
    } else {
      ElMessage.success(t('settingsSystem.saveSuccess'))
    }
  } catch (e) { ElMessage.error(t('settingsSystem.saveFailed')) }
}
onMounted(() => {
  loadSystemConfig()
})
</script>

<style scoped lang="scss">
.settings-page {
  .form-hint {
    font-size: 12px;
    color: #909399;
    margin-left: 12px;
    line-height: 1.4;
  }
}
</style>
