<template>
  <div class="settings-page">
    <el-card :shadow="never">
      <template #header>
        <h3>{{ t('settingsStorage.title') }}</h3>
      </template>
      <el-form :model="storageForm" label-width="160">
        <el-form-item :label="t('settingsStorage.segmentDuration')">
          <el-input-number v-model="storageForm.segment_duration" :min="30" :max="86400" :step="30" :controls="false" style="width: 140px" />
          <span class="form-hint">{{ t('settingsStorage.segmentDurationHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.maxDays')">
          <el-input-number v-model="storageForm.max_days" :min="1" :max="365" :controls="false" style="width: 140px" />
          <span class="form-hint">{{ t('settingsStorage.maxDaysHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.maxStorageGb')">
          <el-input-number v-model="storageForm.max_storage_gb" :min="0" :max="100000" :precision="1" :step="10" :controls="false" style="width: 140px" />
          <span class="form-hint">{{ t('settingsStorage.maxStorageGbHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.rootPath')">
          <el-input v-model="storageForm.root_path" placeholder="./recordings" style="width: 360px" />
          <span class="form-hint">{{ t('settingsStorage.rootPathHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.cleanupInterval')">
          <el-input-number v-model="storageForm.cleanup_interval" :min="300" :max="86400" :step="300" :controls="false" style="width: 140px" />
          <span class="form-hint">{{ t('settingsStorage.cleanupIntervalHint') }}</span>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="saveStorageSettings" :loading="storageSaving">
            <el-icon><Check /></el-icon> {{ t('settingsStorage.saveStorage') }}
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card :shadow="never" class="mt-16">
      <template #header>
        <h3>{{ t('settingsStorage.snapshotTitle') }}</h3>
      </template>
      <el-form :model="snapshotForm" label-width="160">
        <el-form-item :label="t('settingsStorage.snapshotEnabled')">
          <el-switch v-model="snapshotForm.enabled" />
          <span class="form-hint">{{ t('settingsStorage.snapshotEnabledHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.snapshotInterval')">
          <el-input-number v-model="snapshotForm.interval" :min="30" :max="86400" :step="30" :controls="false" style="width: 140px" />
          <span class="form-hint">{{ t('settingsStorage.snapshotIntervalHint') }}</span>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="saveSnapshotSettings" :loading="snapshotSaving">
            <el-icon><Check /></el-icon> {{ t('settingsStorage.saveSnapshot') }}
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card :shadow="never" class="mt-16">
      <template #header>
        <h3>{{ t('settingsStorage.webdavTitle') }}</h3>
      </template>
      <el-form :model="storageForm.webdav" label-width="160">
        <el-form-item :label="t('settingsStorage.webdavEnabled')">
          <el-switch v-model="storageForm.webdav.enabled" />
          <span class="form-hint">{{ t('settingsStorage.webdavEnabledHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.webdavOnly')">
          <el-switch v-model="storageForm.webdav.only" :disabled="!storageForm.webdav.enabled" />
          <span class="form-hint">{{ t('settingsStorage.webdavOnlyHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.webdavUrl')">
          <el-input v-model="storageForm.webdav.url" :placeholder="t('settingsStorage.webdavUrlPlaceholder')" style="width: 400px" />
        </el-form-item>
        <el-form-item :label="t('settingsStorage.webdavUsername')">
          <el-input v-model="storageForm.webdav.username" :placeholder="t('settingsStorage.webdavUsernamePlaceholder')" style="width: 300px" />
        </el-form-item>
        <el-form-item :label="t('settingsStorage.webdavPassword')">
          <el-input v-model="storageForm.webdav.password" type="password" show-password :placeholder="t('settingsStorage.webdavPasswordPlaceholder')" style="width: 300px" />
        </el-form-item>
        <el-form-item :label="t('settingsStorage.webdavBasePath')">
          <el-input v-model="storageForm.webdav.base_path" :placeholder="t('settingsStorage.webdavBasePathPlaceholder')" style="width: 300px" />
          <span class="form-hint">{{ t('settingsStorage.webdavBasePathHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.webdavMaxDays')">
          <el-input-number v-model="storageForm.webdav.max_days" :min="0" :max="3650" :controls="false" style="width: 140px" />
          <span class="form-hint">{{ t('settingsStorage.webdavMaxDaysHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.webdavMaxStorageGb')">
          <el-input-number v-model="storageForm.webdav.max_storage_gb" :min="0" :max="100000" :step="0.5" :precision="1" :controls="false" style="width: 140px" />
          <span class="form-hint">{{ t('settingsStorage.webdavMaxStorageGbHint') }}</span>
        </el-form-item>
        <el-form-item>
          <el-button @click="testWebdavConnection" :loading="webdavTesting">
            <el-icon><Connection /></el-icon> {{ t('settingsStorage.testConnection') }}
          </el-button>
          <el-button type="primary" @click="saveStorageSettings" :loading="storageSaving">
            <el-icon><Check /></el-icon> {{ t('settingsStorage.save') }}
          </el-button>
          <span v-if="webdavTestResult" :class="['ml-12', webdavTestResult.ok ? 'text-success' : 'text-danger']">
            {{ webdavTestResult.message || webdavTestResult.error }}
          </span>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card :shadow="never" class="mt-16">
      <template #header>
        <h3>{{ t('settingsStorage.minioTitle') }}</h3>
      </template>
      <el-form :model="storageForm.minio" label-width="160">
        <el-form-item :label="t('settingsStorage.minioEnabled')">
          <el-switch v-model="storageForm.minio.enabled" />
          <span class="form-hint">{{ t('settingsStorage.minioEnabledHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.minioOnly')">
          <el-switch v-model="storageForm.minio.only" :disabled="!storageForm.minio.enabled" />
          <span class="form-hint">{{ t('settingsStorage.minioOnlyHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.minioEndpoint')">
          <el-input v-model="storageForm.minio.endpoint" :placeholder="t('settingsStorage.minioEndpointPlaceholder')" style="width: 400px" />
          <span class="form-hint">{{ t('settingsStorage.minioEndpointHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.minioAccessKey')">
          <el-input v-model="storageForm.minio.access_key" :placeholder="t('settingsStorage.minioAccessKeyPlaceholder')" style="width: 300px" />
        </el-form-item>
        <el-form-item :label="t('settingsStorage.minioSecretKey')">
          <el-input v-model="storageForm.minio.secret_key" type="password" show-password :placeholder="t('settingsStorage.minioSecretKeyPlaceholder')" style="width: 300px" />
        </el-form-item>
        <el-form-item :label="t('settingsStorage.minioBucket')">
          <el-input v-model="storageForm.minio.bucket" :placeholder="t('settingsStorage.minioBucketPlaceholder')" style="width: 300px" />
          <span class="form-hint">{{ t('settingsStorage.minioBucketHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.minioUseSsl')">
          <el-switch v-model="storageForm.minio.use_ssl" />
          <span class="form-hint">{{ t('settingsStorage.minioUseSslHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.minioBasePath')">
          <el-input v-model="storageForm.minio.base_path" :placeholder="t('settingsStorage.minioBasePathPlaceholder')" style="width: 300px" />
          <span class="form-hint">{{ t('settingsStorage.minioBasePathHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.minioMaxDays')">
          <el-input-number v-model="storageForm.minio.max_days" :min="0" :max="3650" :controls="false" style="width: 140px" />
          <span class="form-hint">{{ t('settingsStorage.minioMaxDaysHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsStorage.minioMaxStorageGb')">
          <el-input-number v-model="storageForm.minio.max_storage_gb" :min="0" :max="100000" :step="0.5" :precision="1" :controls="false" style="width: 140px" />
          <span class="form-hint">{{ t('settingsStorage.minioMaxStorageGbHint') }}</span>
        </el-form-item>
        <el-form-item>
          <el-button @click="testMinioConnection" :loading="minioTesting">
            <el-icon><Connection /></el-icon> {{ t('settingsStorage.testConnection') }}
          </el-button>
          <el-button type="primary" @click="saveStorageSettings" :loading="storageSaving">
            <el-icon><Check /></el-icon> {{ t('settingsStorage.save') }}
          </el-button>
          <span v-if="minioTestResult" :class="['ml-12', minioTestResult.ok ? 'text-success' : 'text-danger']">
            {{ minioTestResult.message || minioTestResult.error }}
          </span>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Check, Connection } from '@element-plus/icons-vue'
import { api } from '@/api'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const storageSaving = ref(false)
const webdavTesting = ref(false)
const webdavTestResult = ref<{ ok: boolean; message?: string; error?: string } | null>(null)
const minioTesting = ref(false)
const minioTestResult = ref<{ ok: boolean; message?: string; error?: string } | null>(null)
const storageForm = reactive({
  root_path: './recordings',
  segment_duration: 180,
  max_days: 7,
  max_storage_gb: 0,
  cleanup_interval: 3600,
  webdav: {
    enabled: false,
    url: '',
    username: '',
    password: '',
    base_path: 'surveillance',
    max_days: 30,
    max_storage_gb: 0,
    only: false,
  },
  minio: {
    enabled: false,
    endpoint: '',
    access_key: '',
    secret_key: '',
    bucket: 'surveillance',
    use_ssl: false,
    base_path: 'surveillance',
    max_days: 30,
    max_storage_gb: 0,
    only: false,
  },
})

const snapshotSaving = ref(false)
const snapshotForm = reactive({
  enabled: true,
  interval: 300,
})

const loadSnapshotSettings = async () => {
  try {
    const res: any = await api.settings.getCamera()
    if (res) {
      snapshotForm.enabled = res.snapshot_enabled !== false
      snapshotForm.interval = res.snapshot_interval || 300
    }
  } catch (e) { console.error(e) }
}

const saveSnapshotSettings = async () => {
  snapshotSaving.value = true
  try {
    const res: any = await api.settings.updateCamera({
      snapshot_enabled: snapshotForm.enabled,
      snapshot_interval: snapshotForm.interval,
    })
    ElMessage.success(res?.message || t('settingsStorage.snapshotSaveSuccess'))
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error || t('settingsStorage.snapshotSaveFailed'))
  } finally {
    snapshotSaving.value = false
  }
}

const loadStorageSettings = async () => {
  try {
    const res: any = await api.settings.getStorage()
    if (res && res.root_path) {
      storageForm.root_path = res.root_path
      storageForm.segment_duration = res.segment_duration || 180
      storageForm.max_days = res.max_days || 7
      storageForm.max_storage_gb = res.max_storage_gb ?? 0
      storageForm.cleanup_interval = res.cleanup_interval || 3600
      if (res.webdav) {
        storageForm.webdav.enabled = !!res.webdav.enabled
        storageForm.webdav.url = res.webdav.url || ''
        storageForm.webdav.username = res.webdav.username || ''
        storageForm.webdav.password = ''
        storageForm.webdav.base_path = res.webdav.base_path || 'surveillance'
        storageForm.webdav.max_days = res.webdav.max_days ?? 30
        storageForm.webdav.max_storage_gb = res.webdav.max_storage_gb ?? 0
        storageForm.webdav.only = !!res.webdav.only
      }
      if (res.minio) {
        storageForm.minio.enabled = !!res.minio.enabled
        storageForm.minio.endpoint = res.minio.endpoint || ''
        storageForm.minio.access_key = res.minio.access_key || ''
        storageForm.minio.secret_key = ''
        storageForm.minio.bucket = res.minio.bucket || 'surveillance'
        storageForm.minio.use_ssl = !!res.minio.use_ssl
        storageForm.minio.base_path = res.minio.base_path || 'surveillance'
        storageForm.minio.max_days = res.minio.max_days ?? 30
        storageForm.minio.max_storage_gb = res.minio.max_storage_gb ?? 0
        storageForm.minio.only = !!res.minio.only
      }
    }
  } catch (e) { console.error(e) }
}

const saveStorageSettings = async () => {
  storageSaving.value = true
  try {
    const res: any = await api.settings.updateStorage({
      root_path: storageForm.root_path,
      segment_duration: storageForm.segment_duration,
      max_days: storageForm.max_days,
      max_storage_gb: storageForm.max_storage_gb,
      cleanup_interval: storageForm.cleanup_interval,
      webdav: {
        enabled: storageForm.webdav.enabled,
        url: storageForm.webdav.url,
        username: storageForm.webdav.username,
        password: storageForm.webdav.password,
        base_path: storageForm.webdav.base_path,
        max_days: storageForm.webdav.max_days,
        max_storage_gb: storageForm.webdav.max_storage_gb,
        only: storageForm.webdav.only,
      },
      minio: {
        enabled: storageForm.minio.enabled,
        endpoint: storageForm.minio.endpoint,
        access_key: storageForm.minio.access_key,
        secret_key: storageForm.minio.secret_key,
        bucket: storageForm.minio.bucket,
        use_ssl: storageForm.minio.use_ssl,
        base_path: storageForm.minio.base_path,
        max_days: storageForm.minio.max_days,
        max_storage_gb: storageForm.minio.max_storage_gb,
        only: storageForm.minio.only,
      },
    })
    ElMessage.success(res?.message || t('settingsStorage.saveSuccess'))
    storageForm.webdav.password = ''
    storageForm.minio.secret_key = ''
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error || t('settingsStorage.saveFailed'))
  } finally {
    storageSaving.value = false
  }
}

const testWebdavConnection = async () => {
  if (!storageForm.webdav.url) {
    ElMessage.warning(t('settingsStorage.webdavUrlRequired'))
    return
  }
  webdavTesting.value = true
  webdavTestResult.value = null
  try {
    const res: any = await api.settings.testWebdav({
      url: storageForm.webdav.url,
      username: storageForm.webdav.username,
      password: storageForm.webdav.password,
      base_path: storageForm.webdav.base_path,
    })
    webdavTestResult.value = { ok: !!res.ok, message: res.message, error: res.error }
    if (res.ok) ElMessage.success(t('settingsStorage.webdavTestSuccess'))
  } catch (e: any) {
    webdavTestResult.value = { ok: false, error: e?.message || t('settingsStorage.networkError') }
  } finally {
    webdavTesting.value = false
  }
}

const testMinioConnection = async () => {
  if (!storageForm.minio.endpoint || !storageForm.minio.bucket) {
    ElMessage.warning(t('settingsStorage.minioEndpointBucketRequired'))
    return
  }
  minioTesting.value = true
  minioTestResult.value = null
  try {
    const res: any = await api.settings.testMinio({
      endpoint: storageForm.minio.endpoint,
      access_key: storageForm.minio.access_key,
      secret_key: storageForm.minio.secret_key,
      bucket: storageForm.minio.bucket,
      use_ssl: storageForm.minio.use_ssl,
      base_path: storageForm.minio.base_path,
    })
    minioTestResult.value = { ok: !!res.ok, message: res.message, error: res.error }
    if (res.ok) ElMessage.success(t('settingsStorage.minioTestSuccess'))
  } catch (e: any) {
    minioTestResult.value = { ok: false, error: e?.message || t('settingsStorage.networkError') }
  } finally {
    minioTesting.value = false
  }
}

onMounted(() => {
  loadStorageSettings()
  loadSnapshotSettings()
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
  .text-success { color: #67c23a; font-size: 13px; }
  .text-danger { color: #f56c6c; font-size: 13px; }
  .ml-12 { margin-left: 12px; }
}
</style>