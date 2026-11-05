<template>
  <div class="camera-add-page">
    <div class="page-header">
      <el-button @click="goBack"><el-icon><ArrowLeft /></el-icon> {{ isEditMode ? t('camerasAdd.backToDetail') : t('camerasAdd.backToList') }}</el-button>
      <h2>{{ isEditMode ? t('camerasAdd.editCamera') : t('camerasAdd.addCamera') }}</h2>
      <span v-if="isEditMode" class="form-hint">{{ t('camerasAdd.editHint') }}</span>
    </div>

    <el-card :shadow="never">
      <el-form :model="cameraForm" :rules="cameraRules" ref="cameraFormRef" label-width="120">


        <el-form-item :label="t('camerasAdd.protocol')" prop="protocol">
          <el-radio-group v-model="cameraForm.protocol" :disabled="isEditMode" style="display: flex; gap: 16px;">
            <el-radio value="onvif">{{ t('camerasAdd.onvifAuto') }}</el-radio>
            <el-radio value="rtsp">{{ t('camerasAdd.rtspDirect') }}</el-radio>
          </el-radio-group>
          <p class="form-hint" v-if="isEditMode">{{ t('camerasAdd.protocolLocked') }}</p>
          <p class="form-hint" v-else-if="!isRtspMode">{{ t('camerasAdd.onvifHint') }}</p>
          <p class="form-hint" v-else>{{ t('camerasAdd.rtspHint') }}</p>
        </el-form-item>


        <template v-if="!isRtspMode">
        <el-form-item :label="t('camerasAdd.ip')" prop="ip">
          <el-input v-model="cameraForm.ip" placeholder="192.168.1.100" style="width: 300px" @blur="onIpBlur" />
        </el-form-item>

        <el-form-item :label="t('camerasAdd.username')" prop="username">
          <el-input v-model="cameraForm.username" placeholder="admin" style="width: 300px" />
        </el-form-item>

        <el-form-item :label="t('camerasAdd.password')" prop="password">
          <el-input v-model="cameraForm.password" type="password" show-password :placeholder="isEditMode ? t('camerasAdd.passwordKeep') : t('camerasAdd.cameraPassword')" style="width: 300px" />
          <p class="form-hint" v-if="isEditMode">{{ t('camerasAdd.passwordChangedHint') }}</p>
        </el-form-item>


        <el-form-item :label="t('camerasAdd.autoConfig')">
          <el-button
            type="primary"
            :loading="detectLoading"
            :disabled="!cameraForm.ip || !cameraForm.username || !cameraForm.password"
            @click="autoDetectAndFill"
            style="width: 100%;"
          >
            <el-icon v-if="!detectLoading"><Search /></el-icon>
            <span v-if="!detectLoading">{{ t('camerasAdd.autoDetect') }}</span>
            <span v-else>{{ t('camerasAdd.detecting') }}</span>
          </el-button>
          <p class="form-hint" v-if="detectError" style="color: #f56c6c;">{{ detectError }}</p>
          <p class="form-hint" v-if="detectSuccess" style="color: #67c23a;">{{ detectSuccess }}</p>
        </el-form-item>
</template>

        <!-- RTSP 模式：粘贴完整流地址 -->
        <template v-else>
          <el-form-item :label="t('camerasAdd.name')" prop="name">
            <el-input v-model="cameraForm.name" :placeholder="t('camerasAdd.namePlaceholderRtsp')" style="width: 400px" maxlength="100" />
          </el-form-item>

          <el-form-item :label="t('camerasAdd.rtspUrl')" prop="rtsp_url">
            <el-input v-model="cameraForm.rtsp_url" :placeholder="t('camerasAdd.rtspPlaceholder')" style="width: 560px" />
            <p class="form-hint">{{ t('camerasAdd.rtspPasteHint') }}</p>
            <p class="form-hint" v-if="isEditMode">{{ t('camerasAdd.rtspEditHint') }}</p>
          </el-form-item>
</template>

        <el-divider v-if="!isRtspMode" />

        <!-- 探测成功后显示：设备信息、Profile 选择、高级设置 -->
        <template v-if="detectedDevice">
          <el-form-item :label="t('camerasAdd.deviceInfo')" class="section-title">
            <div class="section-divider" />
          </el-form-item>

          <el-form-item :label="t('camerasAdd.manufacturerModel')">
            <el-tag size="small">{{ detectedDevice.manufacturer }} {{ detectedDevice.model }}</el-tag>
            <el-tag size="small" style="margin-left: 8px;">{{ t('camerasAdd.firmware', { value: detectedDevice.firmware }) }}</el-tag>
          </el-form-item>

          <el-form-item :label="t('camerasAdd.name')" prop="name">
            <el-input v-model="cameraForm.name" :placeholder="t('camerasAdd.namePlaceholderAuto')" style="width: 400px" maxlength="100" />
          </el-form-item>

          <el-form-item :label="t('camerasAdd.profile')" prop="onvif_profile_token">
            <el-select v-model="cameraForm.onvif_profile_token" :placeholder="t('camerasAdd.selectProfile')" style="width: 400px" clearable>
              <el-option
                v-for="p in detectedDevice.profiles"
                :key="p.token"
                :label="formatProfileLabel(p)"
                :value="p.token"
              />
            </el-select>
            <p class="form-hint">{{ t('camerasAdd.streamHint') }}</p>
            <p class="form-hint" v-if="isEditMode">{{ t('camerasAdd.profileEditHint') }}</p>
          </el-form-item>


          <el-form-item :label="t('camerasAdd.resolution')" v-if="selectedProfile">
            <el-row :gutter="12">
              <el-col :span="11">
                <el-input-number v-model="cameraForm.width" :disabled="true" style="width: 100%" /> px
              </el-col>
              <el-col :span="2"><span class="text-center">×</span></el-col>
              <el-col :span="11">
                <el-input-number v-model="cameraForm.height" :disabled="true" style="width: 100%" /> px
              </el-col>
            </el-row>
          </el-form-item>

          <el-form-item :label="t('camerasAdd.codecFpsBitrate')" v-if="selectedProfile">
            <el-row :gutter="12">
              <el-col :span="8">
                <el-select v-model="cameraForm.codec" :disabled="true" style="width: 100%" :placeholder="t('camerasAdd.codec')">
                  <el-option label="H.264" value="h264" />
                  <el-option label="H.265" value="h265" />
                </el-select>
              </el-col>
              <el-col :span="8">
                <el-input-number v-model="cameraForm.fps" :disabled="true" style="width: 100%" /> fps
              </el-col>
              <el-col :span="8">
                <el-input-number v-model="cameraForm.bitrate" :disabled="true" style="width: 100%" /> kbps
              </el-col>
            </el-row>
          </el-form-item>

          <el-form-item :label="t('camerasAdd.onvifAddress')" prop="onvif_address">
            <el-input v-model="cameraForm.onvif_address" :disabled="true" style="width: 400px" />
          </el-form-item>

          <el-form-item :label="t('camerasAdd.rtspUrl')" prop="path" v-if="selectedProfile?.rtspUri">
            <el-input v-model="cameraForm.path" :disabled="true" style="width: 500px" />
            <p class="form-hint">{{ t('camerasAdd.rtspUriHint') }}</p>
          </el-form-item>
</template>

        <!-- 高级选项（录像配置）：ONVIF 探测成功后 / RTSP 模式 均显示 -->
        <template v-if="isRtspMode || detectedDevice">
          <el-divider />
          <el-form-item :label="t('camerasAdd.advanced')" class="section-title">
            <div class="section-divider" />
          </el-form-item>

          <el-form-item :label="t('camerasAdd.ptzEnabled')" prop="ptz_enabled" v-if="!isRtspMode">
            <el-switch v-model="cameraForm.ptz_enabled" :disabled="!detectedDevice.ptzSupported" />
            <span v-if="!detectedDevice.ptzSupported" style="margin-left: 8px; color: #909399;">{{ t('camerasAdd.ptzNotSupported') }}</span>
          </el-form-item>

          <el-form-item :label="t('camerasAdd.recordEnabled')" prop="record_enabled">
            <el-switch v-model="cameraForm.record_enabled" />
          </el-form-item>

          <el-form-item :label="t('camerasAdd.recordType')" prop="record_type">
            <el-select v-model="cameraForm.record_type" :placeholder="t('camerasAdd.selectType')" style="width: 200px">
              <el-option :label="t('camerasAdd.recordContinuous')" value="continuous" />
              <el-option v-if="!isRtspMode" :label="t('camerasAdd.recordMotion')" value="motion" />
              <el-option :label="t('camerasAdd.recordSchedule')" value="schedule" />
            </el-select>
            <p class="form-hint" v-if="isRtspMode">{{ t('camerasAdd.motionHint') }}</p>
          </el-form-item>

          <el-form-item :label="t('camerasAdd.recordPlan')" prop="record_schedule" v-if="cameraForm.record_type === 'schedule'">
            <el-input v-model="cameraForm.record_schedule" :placeholder="t('camerasAdd.schedulePlaceholder')" style="width: 300px" />
            <p class="form-hint">{{ t('camerasAdd.scheduleHint') }}</p>
          </el-form-item>
</template>

        <div class="form-actions">
          <el-button @click="goBack">{{ t('camerasAdd.cancel') }}</el-button>
          <el-button type="primary" :loading="submitLoading" @click="submitForm" :disabled="isRtspMode ? editLoading : !detectedDevice || editLoading">
            <el-icon><Check /></el-icon> {{ isEditMode ? t('camerasAdd.saveChanges') : t('camerasAdd.saveAndStart') }}
          </el-button>
        </div>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { ArrowLeft, Check, Search } from '@element-plus/icons-vue'
import { api } from '@/api'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()


const isEditMode = computed(() => route.name === 'CameraEdit')
const editId = computed(() => (route.params.id ? Number(route.params.id) : null))
const editLoading = ref(false)
const loadedRecordEnabled = ref(true)


const isRtspMode = computed(() => cameraForm.protocol === 'rtsp')


const buildRtspUrl = (c: any) => {
  if (!c) return ''
  const port = c.port || 554
  const path = c.path && c.path !== '/' ? c.path : ''
  return `rtsp://${c.ip || ''}:${port}${path}`
}

const submitLoading = ref(false)
const detectLoading = ref(false)
const cameraFormRef = ref()

const cameraForm = reactive({
  name: '',
  description: '',
  protocol: 'onvif',
  ip: '',
  port: 80,
  path: '',
  onvif_address: '',
  discover_network: '',
  onvif_profile_token: '',
  device_id: '',
  username: '',
  password: '',
  rtsp_url: '',
  width: 1920,
  height: 1080,
  fps: 25,
  codec: 'h264',
  bitrate: 4096,
  ptz_enabled: false,
  record_enabled: true,
  record_type: 'continuous',
  record_schedule: '0-23',
})


const detectedDevice = ref<{
  ip: string
  port: number
  name: string
  manufacturer: string
  model: string
  firmware: string
  serialNumber: string
  hardwareId: string
  mac: string
  profiles: Array<{
    token: string
    name: string
    width: number
    height: number
    fps: number
    bitrate: number
    codec: string
    rtspUri: string
    videoSourceTok: string
    videoEncoderTok: string
    ptzConfigurationToken: string
  }>
  xaddr: string
  ptzSupported: boolean
} | null>(null)

const detectError = ref('')
const detectSuccess = ref('')


const loadCameraForEdit = async () => {
  if (!editId.value) return
  editLoading.value = true
  try {
    const res: any = await api.cameras.get(editId.value)
    const c = res?.data || res
    if (!c || !c.id) {
      ElMessage.error(t('camerasAdd.cameraNotFound'))
      goBack()
      return
    }

    cameraForm.name = c.name || ''
    cameraForm.description = c.description || ''
    cameraForm.protocol = c.protocol || 'onvif'
    cameraForm.ip = c.ip || ''
    cameraForm.port = c.port || 80
    cameraForm.path = c.path || ''
    cameraForm.onvif_address = c.onvif_address || ''
    cameraForm.onvif_profile_token = c.onvif_profile_token || ''
    cameraForm.device_id = c.device_id || ''
    cameraForm.username = c.username || ''
    cameraForm.password = ''

    cameraForm.rtsp_url = c.protocol === 'rtsp' ? buildRtspUrl(c) : ''
    cameraForm.width = c.width || 1920
    cameraForm.height = c.height || 1080
    cameraForm.fps = c.fps || 25
    cameraForm.codec = c.codec || 'h264'
    cameraForm.bitrate = c.bitrate || 4096
    cameraForm.ptz_enabled = !!c.ptz_enabled
    cameraForm.record_enabled = c.record_enabled !== false
    cameraForm.record_type = c.record_type || 'continuous'

    if (c.protocol === 'rtsp' && cameraForm.record_type === 'motion') {
      cameraForm.record_type = 'continuous'
    }
    cameraForm.record_schedule = c.record_schedule || '0-23'
    loadedRecordEnabled.value = cameraForm.record_enabled


    detectedDevice.value = {
      ip: c.ip || '',
      port: c.port || 80,
      name: c.name || '',
      manufacturer: '-',
      model: '-',
      firmware: '-',
      serialNumber: '',
      hardwareId: '',
      mac: '',
      profiles: c.onvif_profile_token ? [{
        token: c.onvif_profile_token,
        name: t('camerasAdd.currentProfile'),
        width: c.width || 1920,
        height: c.height || 1080,
        fps: c.fps || 25,
        bitrate: c.bitrate || 4096,
        codec: c.codec || 'h264',
        rtspUri: c.path || '',
        videoSourceTok: '',
        videoEncoderTok: '',
        ptzConfigurationToken: '',
      }] : [],
      xaddr: c.onvif_address || '',
      ptzSupported: true,
    }
  } catch (e) {
    console.error(e)
    ElMessage.error(t('camerasAdd.loadFailed'))
  } finally {
    editLoading.value = false
  }
}

onMounted(() => {
  if (isEditMode.value) loadCameraForEdit()
})

const cameraRules = computed(() => {
  const base: any = {
    name: [{ required: true, message: t('camerasAdd.nameRequired'), trigger: 'blur' }],
  }
  if (isRtspMode.value) {
    base.rtsp_url = [
      { required: true, message: t('camerasAdd.rtspUrlRequired'), trigger: 'blur' },
      {
        validator: (_: any, value: string, cb: any) => {
          if (!value) return cb()
          try {
            const u = new URL(value.trim())
            if (u.protocol !== 'rtsp:') cb(new Error(t('camerasAdd.rtspOnly')))
            else if (!u.hostname) cb(new Error(t('camerasAdd.hostRequired')))
            else cb()
          } catch {
            cb(new Error(t('camerasAdd.rtspInvalid')))
          }
        },
        trigger: 'blur',
      },
    ]
  } else {
    base.ip = [{ required: true, message: t('camerasAdd.ipRequired'), trigger: 'blur' }]
    base.username = [{ required: true, message: t('camerasAdd.usernameRequired'), trigger: 'blur' }]

    base.password = isEditMode.value
      ? [{ required: false, message: '', trigger: 'blur' }]
      : [{ required: true, message: t('camerasAdd.passwordRequired'), trigger: 'blur' }]
    base.onvif_profile_token = [{ required: true, message: t('camerasAdd.profileRequired'), trigger: 'change' }]
  }
  return base
})


const selectedProfile = computed(() => {
  if (!detectedDevice.value || !cameraForm.onvif_profile_token) return null
  return detectedDevice.value.profiles.find(p => p.token === cameraForm.onvif_profile_token) || null
})


const formatProfileLabel = (p: any) => {
  const type = p.name.toLowerCase().includes('main') || p.width >= 1920 ? t('camerasAdd.mainStream') : t('camerasAdd.subStream')
  return `${p.name} (${p.width}×${p.height}, ${p.codec.toUpperCase()}, ${p.bitrate}kbps) [${type}]`
}


const onIpBlur = () => {
  if (cameraForm.ip && !cameraForm.name) {
    const suffix = cameraForm.ip.split('.').pop()
    cameraForm.name = `Camera_${suffix}`
  }
}


const autoDetectAndFill = async () => {
  detectLoading.value = true
  detectError.value = ''
  detectSuccess.value = ''

  try {
    const res: any = await api.cameras.probe(cameraForm.ip, cameraForm.username, cameraForm.password)

    const d = res?.device

    if (!d) {
      detectError.value = res?.error || t('camerasAdd.probeNoData')
      return
    }

    detectedDevice.value = {
      ip: d.ip,
      port: d.port,
      name: d.name,
      manufacturer: d.manufacturer,
      model: d.model,
      firmware: d.firmware,
      serialNumber: d.serialNumber,
      hardwareId: d.hardwareId,
      mac: d.mac,
      profiles: d.profiles || [],
      xaddr: d.xaddr,
      ptzSupported: false,
    }


    cameraForm.onvif_address = d.xaddr
    cameraForm.port = d.port


    if (!cameraForm.name && d.manufacturer && d.model) {
      const ipSuffix = d.ip.split('.').pop()
      cameraForm.name = `${d.manufacturer}_${d.model}_${ipSuffix}`
    }


    if (d.profiles && d.profiles.length > 0) {
      let mainProfile = d.profiles.find(p =>
        p.name.toLowerCase().includes('main') || p.width >= 1920
      ) || d.profiles.reduce((max, p) => p.width * p.height > max.width * max.height ? p : max)
      cameraForm.onvif_profile_token = mainProfile.token
    }


    if (selectedProfile.value) {
      const p = selectedProfile.value
      cameraForm.width = p.width
      cameraForm.height = p.height
      cameraForm.fps = p.fps || 25
      cameraForm.codec = p.codec || 'h264'
      cameraForm.bitrate = p.bitrate || 4096
      if (p.rtspUri) {
        cameraForm.path = p.rtspUri
      }
    }

    detectSuccess.value = t('camerasAdd.probeSuccess', { manufacturer: d.manufacturer, model: d.model, count: d.profiles?.length || 0 })
    ElMessage.success(t('camerasAdd.autoConfigDone'))

  } catch (e: any) {
    console.error(e)
    detectError.value = t('camerasAdd.probeFailed', { message: e.message || t('camerasAdd.networkErrorAuth') })
    ElMessage.error(detectError.value)
  } finally {
    detectLoading.value = false
  }
}


watch(() => cameraForm.onvif_profile_token, (newToken) => {
  if (!newToken || !detectedDevice.value) return
  const p = detectedDevice.value.profiles.find(pr => pr.token === newToken)
  if (p) {
    cameraForm.width = p.width
    cameraForm.height = p.height
    cameraForm.fps = p.fps || 25
    cameraForm.codec = p.codec || 'h264'
    cameraForm.bitrate = p.bitrate || 4096
    if (p.rtspUri) cameraForm.path = p.rtspUri
  }
})

const submitForm = async () => {
  try {
    await cameraFormRef.value?.validate()
    submitLoading.value = true


    if (isRtspMode.value) {
      let u: URL
      try {
        u = new URL(cameraForm.rtsp_url.trim())
      } catch {
        ElMessage.error(t('camerasAdd.rtspInvalidRetry'))
        return
      }
      const payload = {
        name: cameraForm.name,
        description: cameraForm.description,
        protocol: 'rtsp',
        ip: u.hostname,
        port: u.port ? Number(u.port) : 554,
        username: u.username,
        password: u.password,
        path: u.pathname + u.search,
        width: cameraForm.width,
        height: cameraForm.height,
        fps: cameraForm.fps,
        codec: cameraForm.codec,
        bitrate: cameraForm.bitrate,
        ptz_enabled: false,
        record_enabled: cameraForm.record_enabled,
        record_type: cameraForm.record_type,
        record_schedule: cameraForm.record_schedule,
      }
      if (isEditMode.value && editId.value) {
        await api.cameras.update(editId.value, payload)
        try {
          const st: any = await api.cameras.status(editId.value)
          if (st?.is_streaming) await api.cameras.restart(editId.value)
        } catch (e) {  }
        ElMessage.success(t('camerasAdd.updatedReconnecting'))
        router.push(`/cameras/${editId.value}`)
      } else {
        const res = await api.cameras.create(payload)
        ElMessage.success(t('camerasAdd.addedConnecting'))
        router.push(`/cameras/${res.id}`)
      }
      return
    }


    const dd = detectedDevice.value
    const payload = {
      name: cameraForm.name,
      description: cameraForm.description,
      protocol: cameraForm.protocol,
      ip: cameraForm.ip,
      port: cameraForm.port,
      path: cameraForm.path,
      onvif_address: cameraForm.onvif_address,
      onvif_profile_token: cameraForm.onvif_profile_token,
      username: cameraForm.username,
      password: cameraForm.password,
      width: cameraForm.width,
      height: cameraForm.height,
      fps: cameraForm.fps,
      codec: cameraForm.codec,
      bitrate: cameraForm.bitrate,
      ptz_enabled: cameraForm.ptz_enabled,
      record_enabled: cameraForm.record_enabled,
      record_type: cameraForm.record_type,
      record_schedule: cameraForm.record_schedule,

      manufacturer: dd && dd.manufacturer && dd.manufacturer !== '-' ? dd.manufacturer : '',
      model: dd && dd.model && dd.model !== '-' ? dd.model : '',
      firmware: dd && dd.firmware && dd.firmware !== '-' ? dd.firmware : '',
      serial_number: dd && dd.serialNumber ? dd.serialNumber : '',
    }

    if (isEditMode.value && editId.value) {
      await api.cameras.update(editId.value, payload)


      let restarted = false
      if (cameraForm.record_enabled === loadedRecordEnabled.value) {
        try {
          const st: any = await api.cameras.status(editId.value)
          if (st?.is_streaming) {
            await api.cameras.restart(editId.value)
            restarted = true
          }
        } catch (e) {  }
      }
      ElMessage.success(t('camerasAdd.updated') + (restarted ? t('camerasAdd.updatedReconnectingStream') : ''))
      router.push(`/cameras/${editId.value}`)
    } else {
      const res = await api.cameras.create(payload)
      ElMessage.success(t('camerasAdd.addedConnecting'))
      router.push(`/cameras/${res.id}`)
    }
  } catch (e) {
    console.error(e)
  } finally {
    submitLoading.value = false
  }
}

const goBack = () => {
  if (isEditMode.value && editId.value) router.push(`/cameras/${editId.value}`)
  else router.push('/cameras')
}
</script>

<style scoped lang="scss">
.camera-add-page {
  .page-header {
    display:flex; align-items:center; gap:16px; margin-bottom:24px;
    h2{margin:0;font-size:20px;font-weight:600;}
  }
  .section-title { margin: 24px 0 12px !important; font-size: 14px; font-weight: 600; color: #303133; }
  .section-divider { border-top: 1px solid #e6e9ed; margin-top: 8px; }
  .form-hint { margin: 4px 0 0; font-size: 12px; color: #909399; }
  .form-actions {
    display:flex; justify-content:flex-end; gap:12px; margin-top:24px; padding-top:16px; border-top:1px solid #f0f0f0;
  }
}
</style>