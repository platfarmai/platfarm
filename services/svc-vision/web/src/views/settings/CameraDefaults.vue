<template>
  <div class="settings-page">
    <el-card :shadow="never">
      <template #header>
        <h3>{{ t('settingsDefaults.defaultRecordingSettings') }}</h3>
</template>
      <el-form :model="cameraDefaultForm" label-width="160">
        <el-form-item :label="t('settingsDefaults.defaultRecordEnabled')">
          <el-switch v-model="cameraDefaultForm.record_enabled" />
        </el-form-item>
        <el-form-item :label="t('settingsDefaults.defaultRecordType')">
          <el-select v-model="cameraDefaultForm.record_type" :placeholder="t('settingsDefaults.selectType')" style="width: 200px">
            <el-option :label="t('settingsDefaults.continuous')" value="continuous" />
            <el-option :label="t('settingsDefaults.motion')" value="motion" />
            <el-option :label="t('settingsDefaults.schedule')" value="schedule" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="saveCameraDefaults"><el-icon><Check /></el-icon> {{ t('settingsDefaults.save') }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card :shadow="never" class="mt-16">
      <template #header>
        <h3>{{ t('settingsDefaults.defaultVideoSettings') }}</h3>
</template>
      <el-form :model="cameraDefaultForm" label-width="160">
        <el-form-item :label="t('settingsDefaults.defaultResolution')">
          <el-row :gutter="12">
            <el-col :span="11">
              <el-input-number v-model="cameraDefaultForm.width" :min="320" :max="8192" :controls="false" :placeholder="t('settingsDefaults.width')" style="width: 100%" />
            </el-col>
            <el-col :span="2"><span class="text-center">×</span></el-col>
            <el-col :span="11">
              <el-input-number v-model="cameraDefaultForm.height" :min="240" :max="8192" :controls="false" :placeholder="t('settingsDefaults.height')" style="width: 100%" />
            </el-col>
          </el-row>
        </el-form-item>
        <el-form-item :label="t('settingsDefaults.defaultFps')">
          <el-input-number v-model="cameraDefaultForm.fps" :min="1" :max="60" :controls="false" style="width: 120px" />
        </el-form-item>
        <el-form-item :label="t('settingsDefaults.defaultCodec')">
          <el-select v-model="cameraDefaultForm.codec" :placeholder="t('settingsDefaults.selectCodec')" style="width: 200px">
            <el-option label="H.264" value="h264" />
            <el-option label="H.265" value="h265" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('settingsDefaults.defaultBitrate')">
          <el-input-number v-model="cameraDefaultForm.bitrate" :min="512" :max="20480" :step="512" :controls="false" style="width: 120px" /> kbps
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="saveCameraDefaults"><el-icon><Check /></el-icon> {{ t('settingsDefaults.save') }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { reactive, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { Check } from '@element-plus/icons-vue'
import { api } from '@/api'

const { t } = useI18n()

const cameraDefaultForm = reactive({
  record_enabled: true,
  record_type: 'continuous',
  width: 1920,
  height: 1080,
  fps: 25,
  codec: 'h264',
  bitrate: 4096,
})

const loadCameraDefaults = async () => {
  try {
    const res: any = await api.settings.getDefaults()
    if (res) Object.assign(cameraDefaultForm, res)
  } catch (e) { console.error(e) }
}

const saveCameraDefaults = async () => {
  try {
    await api.settings.updateDefaults({ ...cameraDefaultForm })
    ElMessage.success(t('settingsDefaults.saveSuccess'))
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error || e?.message || 'save failed')
  }
}

onMounted(() => {
  loadCameraDefaults()
})
</script>

<style scoped lang="scss">
.settings-page {
  .text-center { text-align: center; }
}
</style>
