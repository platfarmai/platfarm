<template>
  <div class="camera-list-page">
    <div class="page-header">
      <h2>{{ t('camerasList.title') }}</h2>
      <div class="header-actions">
        <el-button v-if="isAdmin" type="primary" @click="showAddDialog">
          <el-icon><Plus /></el-icon> {{ t('camerasList.addCamera') }}
        </el-button>
        <el-button v-if="isAdmin" @click="openLanScan">
          <el-icon><Search /></el-icon> {{ t('camerasList.autoDiscover') }}
        </el-button>
        <el-button @click="fetchCameras">
          <el-icon><Refresh /></el-icon> {{ t('camerasList.refresh') }}
        </el-button>
      </div>
    </div>


    <el-card :shadow="never" class="mb-16">
      <el-form :inline="true" :model="searchForm" class="search-form">
        <el-form-item :label="t('camerasList.keyword')">
          <el-input v-model="searchForm.keyword" :placeholder="t('camerasList.nameIp')" clearable style="width: 200px" />
        </el-form-item>
        <el-form-item :label="t('camerasList.statusLabel')">
          <el-select v-model="searchForm.status" :placeholder="t('camerasList.all')" style="width: 140px">
            <el-option :label="t('camerasList.status.online')" value="online" />
            <el-option :label="t('camerasList.status.offline')" value="offline" />
            <el-option :label="t('camerasList.status.error')" value="error" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('camerasList.protocol')">
          <el-select v-model="searchForm.protocol" :placeholder="t('camerasList.all')" style="width: 140px">
            <el-option label="RTSP" value="rtsp" />
            <el-option label="ONVIF" value="onvif" />
            <el-option label="GB28181" value="gb28181" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button @click="handleSearch"><el-icon><Search /></el-icon> {{ t('camerasList.search') }}</el-button>
          <el-button @click="resetSearch">{{ t('camerasList.reset') }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>


    <el-card :shadow="never">
      <el-table
        :data="tableData"
        v-loading="loading"
        border
        stripe
        row-key="id"
        style="width: 100%"
        @row-click="handleRowClick"
      >
        <el-table-column type="selection" width="55" />
        <el-table-column prop="name" :label="t('camerasList.name')" min-width="160" />
        <el-table-column prop="ip" :label="t('camerasList.ipAddress')" width="140" />
        <el-table-column prop="port" :label="t('camerasList.port')" width="80" />
        <el-table-column prop="protocol" :label="t('camerasList.protocol')" width="100">
          <template #default="scope">
            <el-tag :type="protocolType(scope.row.protocol)" size="small">{{ scope.row.protocol.toUpperCase() }}</el-tag>
</template>
        </el-table-column>
        <el-table-column :label="t('camerasList.resolution')" width="130">
          <template #default="scope">
            {{ scope.row.width }}×{{ scope.row.height }}
</template>
        </el-table-column>
        <el-table-column :label="t('camerasList.codecFps')" width="140">
          <template #default="scope">
            {{ scope.row.codec.toUpperCase() }} / {{ scope.row.fps }}fps
</template>
        </el-table-column>
        <el-table-column :label="t('camerasList.statusLabel')" width="100">
          <template #default="scope">
            <el-tag :class="['status-tag', scope.row.status]" size="small">
              {{ t('camerasList.status.' + scope.row.status) }}
            </el-tag>
</template>
        </el-table-column>
        <el-table-column :label="t('camerasList.recording')" width="100">
          <template #default="scope">
            <el-switch
              v-model="scope.row.record_enabled"
              :disabled="!isAdmin"
              @change="toggleRecord(scope.row)"
              :active-value="true"
              :inactive-value="false"
            />
</template>
        </el-table-column>
        <el-table-column :label="t('camerasList.recordMode')" width="130">
          <template #default="scope">
            <el-tag :type="recordTypeTagType(scope.row.record_type)" size="small" effect="plain">
              {{ recordTypeLabel(scope.row.record_type) }}
            </el-tag>
</template>
        </el-table-column>
        <el-table-column :label="t('camerasList.updatedAt')" width="160">
          <template #default="scope">
            {{ formatTime(scope.row.updated_at) }}
</template>
        </el-table-column>
        <el-table-column :label="t('camerasList.actions')" width="220" fixed="right">
          <template #default="scope">
            <el-button-group size="small">
              <el-button link type="primary" @click.stop="goToDetail(scope.row.id)">
                <el-icon><Monitor /></el-icon> {{ t('camerasList.preview') }}
              </el-button>
              <el-button link @click.stop="goToPlayback(scope.row.id)">
                <el-icon><Film /></el-icon> {{ t('camerasList.playback') }}
              </el-button>
              <el-button v-if="isAdmin" link @click.stop="editCamera(scope.row)">
                <el-icon><Edit /></el-icon> {{ t('camerasList.edit') }}
              </el-button>
              <el-button v-if="isAdmin" link type="danger" @click.stop="deleteCamera(scope.row.id)">
                <el-icon><Delete /></el-icon> {{ t('camerasList.delete') }}
              </el-button>
            </el-button-group>
</template>
        </el-table-column>
      </el-table>

      <div class="pagination" v-if="total > pageSize">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[10, 20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @size-change="fetchCameras"
          @current-change="fetchCameras"
        />
      </div>
    </el-card>

    <!-- 局域网扫描对话框 -->
    <el-dialog v-model="lanScanDialogVisible" :title="t('camerasList.lanScanTitle')" width="700" destroy-on-close>
      <div v-if="!lanScanning && lanScanResults.length === 0" style="text-align: center; padding: 40px;">
        <el-icon style="font-size: 48px; color: #909399;"><Search /></el-icon>
        <p class="mt-8" style="color: #909399;">{{ t('camerasList.lanScanHint') }}</p>
        <p style="font-size: 12px; color: #c0c4cc;">{{ t('camerasList.lanScanHint2') }}</p>
        <div class="mt-16" style="display: flex; align-items: center; justify-content: center; gap: 8px;">
          <span style="color: #606266; font-size: 13px;">{{ t('camerasList.scanDuration') }}</span>
          <el-select v-model="scanTimeout" style="width: 120px">
            <el-option :value="10" :label="t('camerasList.seconds', { n: 10 })" />
            <el-option :value="20" :label="t('camerasList.seconds', { n: 20 })" />
            <el-option :value="30" :label="t('camerasList.seconds', { n: 30 })" />
          </el-select>
        </div>
      </div>

      <div v-if="lanScanning" style="text-align: center; padding: 40px;">
        <el-icon class="loading-spinner" style="font-size: 32px;"><Loading /></el-icon>
        <p class="mt-8">{{ t('camerasList.lanScanning', { seconds: scanTimeout }) }}</p>
      </div>

      <div v-if="!lanScanning && lanScanResults.length > 0">
        <el-alert :title="t('camerasList.lanFound', { count: lanScanResults.length })" type="success" show-icon :closable="false" class="mb-8" />
        <el-table :data="lanScanResults" size="small" border max-height="300" @row-click="selectLanDevice" :highlight-current-row="true">
          <el-table-column prop="ip" :label="t('camerasList.ip')" width="130" />
          <el-table-column prop="port" :label="t('camerasList.port')" width="70" />
          <el-table-column prop="manufacturer" :label="t('camerasList.manufacturer')" min-width="90" />
          <el-table-column prop="model" :label="t('camerasList.model')" min-width="110" />
          <el-table-column prop="firmware" :label="t('camerasList.firmware')" min-width="90" />
          <el-table-column prop="auth_required" :label="t('camerasList.auth')" width="90" align="center">
            <template #default="{ row }">
              <el-tag size="small" :type="row.auth_required ? 'warning' : 'success'">{{ row.auth_required ? t('camerasList.authRequired') : t('camerasList.noAuth') }}</el-tag>
</template>
          </el-table-column>
        </el-table>
        <p class="mt-4" style="font-size: 12px; color: #909399;">{{ t('camerasList.lanSelectHint') }}</p>

        <div v-if="selectedLanDevice" class="mt-8 p-12" style="background: #f0f9eb; border-radius: 8px;">
          <h4 style="margin: 0 0 8px; color: #67c23a;">
            {{ t('camerasList.lanSelected', { manufacturer: selectedLanDevice.manufacturer || t('camerasList.unknownManufacturer'), model: selectedLanDevice.model || '', ip: selectedLanDevice.ip, port: selectedLanDevice.port }) }}
          </h4>
          <p style="margin: 0; font-size: 12px; color: #909399;">
            <el-tag size="small" :type="selectedLanDevice.auth_required ? 'warning' : 'success'" style="margin-right: 8px;">{{ selectedLanDevice.auth_required ? t('camerasList.lanAuthFormHint') : t('camerasList.lanNoAuthDevice') }}</el-tag>
          </p>
        </div>
      </div>

      <el-alert v-if="lanScanError" :title="lanScanError" type="error" show-icon :closable="false" class="mt-8" />

      <template #footer>
        <div style="width: 100%; display: flex; justify-content: space-between;">
          <el-button @click="lanScanDialogVisible = false">{{ t('camerasList.close') }}</el-button>
          <el-button type="primary" :loading="lanScanning" @click="runLanScan" v-if="!lanScanning || lanScanResults.length === 0">
            <el-icon v-if="!lanScanning"><Search /></el-icon> {{ lanScanResults.length === 0 ? t('camerasList.startScan') : t('camerasList.rescan') }}
          </el-button>
          <el-button type="primary" :disabled="!selectedLanDevice" @click="fillFromLanScan" v-if="lanScanResults.length > 0 && !lanScanning">
            <el-icon><Plus /></el-icon> {{ t('camerasList.fillAddForm') }}
          </el-button>
        </div>
</template>
    </el-dialog>

    <!-- 添加摄像头对话框：ONVIF 自动发现 / RTSP 流地址 -->
    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="640" destroy-on-close>
      <el-form :model="cameraForm" :rules="cameraRules" ref="cameraFormRef" label-width="120">
        
        <!-- 连接方式：ONVIF 自动发现 / RTSP 手动粘贴 -->
        <el-form-item :label="t('camerasList.connection')">
          <el-radio-group v-model="cameraForm.protocol" style="display: flex; gap: 16px;">
            <el-radio value="onvif">{{ t('camerasList.onvifDiscover') }}</el-radio>
            <el-radio value="rtsp">{{ t('camerasList.rtspDirect') }}</el-radio>
          </el-radio-group>
          <p class="form-hint" v-if="!isRtspMode">{{ t('camerasList.onvifHint') }}</p>
          <p class="form-hint" v-else>{{ t('camerasList.rtspHint') }}</p>
        </el-form-item>

        <!-- ONVIF 模式：核心输入 + 自动探测 -->
        <template v-if="!isRtspMode">
        <el-form-item :label="t('camerasList.ipAddress')" prop="ip">
          <el-input v-model="cameraForm.ip" placeholder="192.168.1.100" style="width: 300px" @blur="onIpBlur" />
        </el-form-item>

        <el-form-item :label="t('camerasList.username')" prop="username">
          <el-input v-model="cameraForm.username" placeholder="admin" style="width: 300px" />
        </el-form-item>

        <el-form-item :label="t('camerasList.password')" prop="password">
          <el-input v-model="cameraForm.password" type="password" show-password :placeholder="t('camerasList.passwordPlaceholder')" style="width: 300px" />
        </el-form-item>


        <el-form-item :label="t('camerasList.autoConfig')">
          <el-button
            type="primary"
            :loading="detectLoading"
            :disabled="!cameraForm.ip || !cameraForm.username || !cameraForm.password"
            @click="autoDetectAndFill"
            style="width: 100%;"
          >
            <el-icon v-if="!detectLoading"><Search /></el-icon>
            <span v-if="!detectLoading">{{ t('camerasList.autoDetectFill') }}</span>
            <span v-else>{{ t('camerasList.detecting') }}</span>
          </el-button>
          <p class="form-hint" v-if="detectError" style="color: #f56c6c;">{{ detectError }}</p>
          <p class="form-hint" v-if="detectSuccess" style="color: #67c23a;">{{ detectSuccess }}</p>
        </el-form-item>
</template>

        <!-- RTSP 模式：粘贴完整流地址 -->
        <template v-else>
          <el-form-item :label="t('camerasList.cameraName')" prop="name">
            <el-input v-model="cameraForm.name" :placeholder="t('camerasList.namePlaceholder')" style="width: 400px" maxlength="100" />
          </el-form-item>

          <el-form-item :label="t('camerasList.rtspUrl')" prop="rtsp_url">
            <el-input v-model="cameraForm.rtsp_url" :placeholder="t('camerasList.rtspPlaceholder')" style="width: 500px" />
            <p class="form-hint">{{ t('camerasList.rtspPasteHint') }}</p>
          </el-form-item>
</template>

        <el-divider v-if="!isRtspMode" />

        <!-- 探测成功后显示：设备信息、Profile 选择、高级设置 -->
        <template v-if="detectedDevice">
          <el-form-item :label="t('camerasList.deviceInfo')" class="section-title">
            <div class="section-divider" />
          </el-form-item>

          <el-form-item :label="t('camerasList.manufacturerModel')">
            <el-tag size="small">{{ detectedDevice.manufacturer }} {{ detectedDevice.model }}</el-tag>
            <el-tag size="small" style="margin-left: 8px;">{{ t('camerasList.firmware') }}: {{ detectedDevice.firmware }}</el-tag>
          </el-form-item>

          <el-form-item :label="t('camerasList.cameraName')" prop="name">
            <el-input v-model="cameraForm.name" :placeholder="t('camerasList.nameAutoFillPlaceholder')" style="width: 400px" maxlength="100" />
          </el-form-item>

          <el-form-item :label="t('camerasList.profileLabel')" prop="onvif_profile_token">
            <el-select v-model="cameraForm.onvif_profile_token" :placeholder="t('camerasList.profilePlaceholder')" style="width: 400px" clearable>
              <el-option
                v-for="p in detectedDevice.profiles"
                :key="p.token"
                :label="formatProfileLabel(p)"
                :value="p.token"
              />
            </el-select>
            <p class="form-hint">{{ t('camerasList.profileHint') }}</p>
          </el-form-item>


          <el-form-item :label="t('camerasList.resolution')" v-if="selectedProfile">
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

          <el-form-item :label="t('camerasList.codecFpsBitrate')" v-if="selectedProfile">
            <el-row :gutter="12">
              <el-col :span="8">
                <el-select v-model="cameraForm.codec" :disabled="true" style="width: 100%" :placeholder="t('camerasList.codec')">
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

          <el-form-item :label="t('camerasList.onvifAddress')" prop="onvif_address">
            <el-input v-model="cameraForm.onvif_address" :disabled="true" style="width: 400px" />
          </el-form-item>
</template>

        <!-- 高级选项（录像配置）：ONVIF 探测成功后 / RTSP 模式 均显示 -->
        <template v-if="isRtspMode || detectedDevice">
          <el-divider />
          <el-form-item :label="t('camerasList.advancedOptions')" class="section-title">
            <div class="section-divider" />
          </el-form-item>

          <el-form-item :label="t('camerasList.enablePtz')" prop="ptz_enabled" v-if="!isRtspMode">
            <el-switch v-model="cameraForm.ptz_enabled" :disabled="!detectedDevice.ptzSupported" />
            <span v-if="!detectedDevice.ptzSupported" style="margin-left: 8px; color: #909399;">{{ t('camerasList.ptzNotSupported') }}</span>
          </el-form-item>

          <el-form-item :label="t('camerasList.enableRecording')" prop="record_enabled">
            <el-switch v-model="cameraForm.record_enabled" />
          </el-form-item>

          <el-form-item :label="t('camerasList.recordTypeLabel')" prop="record_type">
            <el-select v-model="cameraForm.record_type" :placeholder="t('camerasList.selectType')" style="width: 200px">
              <el-option :label="t('camerasList.recordContinuousOption')" value="continuous" />
              <el-option v-if="!isRtspMode" :label="t('camerasList.recordType.motion')" value="motion" />
              <el-option :label="t('camerasList.recordType.schedule')" value="schedule" />
            </el-select>
            <p class="form-hint" v-if="isRtspMode">{{ t('camerasList.recordTypeRtspHint') }}</p>
          </el-form-item>

          <el-form-item :label="t('camerasList.recordSchedule')" prop="record_schedule" v-if="cameraForm.record_type === 'schedule'">
            <el-input v-model="cameraForm.record_schedule" :placeholder="t('camerasList.schedulePlaceholder')" style="width: 300px" />
            <p class="form-hint">{{ t('camerasList.scheduleHint') }}</p>
          </el-form-item>
</template>

        <div class="form-actions">
          <el-button @click="dialogVisible = false">{{ t('camerasList.cancel') }}</el-button>
          <el-button type="primary" :loading="submitLoading" @click="submitCamera" :disabled="isRtspMode ? false : !detectedDevice">
            <el-icon><Check /></el-icon> {{ t('camerasList.saveAndStart') }}
          </el-button>
        </div>
      </el-form>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Plus, Search, Refresh, Monitor, Film, Edit, Delete,
  SwitchButton, VideoCamera, Check, Loading
} from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api'
import { useAuthStore } from '@/stores'
import { useCameraStore } from '@/stores'

const router = useRouter()
const cameraStore = useCameraStore()
const { t, locale } = useI18n()
const authStore = useAuthStore()
const isAdmin = computed(() => authStore.isAdmin)

const loading = ref(false)
const tableData = ref<any[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)


const detectedDevice = ref<any>(null)
const detectLoading = ref(false)
const detectError = ref('')
const detectSuccess = ref('')

const onvifProfiles = ref<Array<{token: string, name: string, width: number, height: number, codec: string}>>([])
const fetchProfilesLoading = ref(false)
const detectOnvifLoading = ref(false)

const searchForm = reactive({
  keyword: '',
  status: '',
  protocol: '',
})

const dialogVisible = ref(false)
const dialogTitle = ref(t('camerasList.addCamera'))
const submitLoading = ref(false)
const cameraFormRef = ref()
const editingId = ref<number | null>(null)

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


const isRtspMode = computed(() => cameraForm.protocol === 'rtsp')

const cameraRules = computed(() => {
  const base: any = {
    name: [{ required: true, message: t('camerasList.ruleName'), trigger: 'blur' }],
  }
  if (isRtspMode.value) {
    base.rtsp_url = [
      { required: true, message: t('camerasList.ruleRtspUrl'), trigger: 'blur' },
      {
        validator: (_: any, value: string, cb: any) => {
          if (!value) return cb()
          try {
            const u = new URL(value.trim())
            if (u.protocol !== 'rtsp:') cb(new Error(t('camerasList.ruleRtspPrefix')))
            else if (!u.hostname) cb(new Error(t('camerasList.ruleRtspHost')))
            else cb()
          } catch {
            cb(new Error(t('camerasList.ruleRtspFormat')))
          }
        },
        trigger: 'blur',
      },
    ]
  } else {
    base.ip = [{ required: true, message: t('camerasList.ruleIp'), trigger: 'blur' }]
    base.username = [{ required: true, message: t('camerasList.ruleUsername'), trigger: 'blur' }]
    base.password = [{ required: true, message: t('camerasList.rulePassword'), trigger: 'blur' }]
    base.onvif_profile_token = [{ required: true, message: t('camerasList.ruleProfile'), trigger: 'change' }]
  }
  return base
})

const protocolType = (p: string) => ({ rtsp: 'primary', onvif: 'success', gb28181: 'warning' }[p] || 'info')

const recordTypeLabel = (v: string) => {
  const key = `camerasList.recordType.${v}`
  const label = t(key)
  return label === key ? (v || t('camerasList.recordType.continuous')) : label
}
const recordTypeTagType = (t: string) => ({ continuous: 'success', motion: 'warning', schedule: 'primary' }[t] || 'info')


const allCameras = ref<any[]>([])

const applyFilters = () => {
  let list = allCameras.value
  const kw = searchForm.keyword.trim().toLowerCase()
  if (kw) {
    list = list.filter(c =>
      (c.name || '').toLowerCase().includes(kw) ||
      (c.ip || '').toLowerCase().includes(kw)
    )
  }
  if (searchForm.status) list = list.filter(c => c.status === searchForm.status)
  if (searchForm.protocol) list = list.filter(c => c.protocol === searchForm.protocol)
  return list
}

const fetchCameras = async () => {
  loading.value = true
  try {
    const res: any = await api.cameras.list()

    allCameras.value = Array.isArray(res) ? res : (res?.data || [])
    const filtered = applyFilters()
    total.value = filtered.length
    const start = (page.value - 1) * pageSize.value
    tableData.value = filtered.slice(start, start + pageSize.value)
  } catch (e) {
    console.error('Failed to fetch camera list', e)
  } finally {
    loading.value = false
  }
}

const handleSearch = () => {
  page.value = 1
  fetchCameras()
}

const resetSearch = () => {
  searchForm.keyword = ''
  searchForm.status = ''
  searchForm.protocol = ''
  page.value = 1
  fetchCameras()
}

const handleRowClick = (row: any) => {
  goToDetail(row.id)
}

const formatTime = (time: string) => time ? new Date(time).toLocaleString(locale.value === 'en' ? 'en-US' : 'zh-CN') : '-'


const selectedProfile = computed(() => {
  if (!detectedDevice.value || !cameraForm.onvif_profile_token) return null
  return detectedDevice.value.profiles.find(p => p.token === cameraForm.onvif_profile_token) || null
})


const formatProfileLabel = (p: any) => {
  const type = p.name.toLowerCase().includes('main') || p.width >= 1920 ? t('camerasList.mainStream') : t('camerasList.subStream')
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

    const r: any = await api.cameras.probe(cameraForm.ip, cameraForm.username, cameraForm.password)
    const d = r?.device

    if (!d) {
      detectError.value = r?.error || t('camerasList.detectFailNoData')
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

    detectSuccess.value = t('camerasList.detectSuccess', { manufacturer: d.manufacturer, model: d.model, count: d.profiles?.length || 0 })
    ElMessage.success(t('camerasList.autoConfigDone'))

  } catch (e: any) {
    console.error(e)
    detectError.value = t('camerasList.detectFail', { msg: e.message || t('camerasList.detectFailDefault') })
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

const showAddDialog = () => {
  editingId.value = null
  dialogTitle.value = t('camerasList.addCamera')
  resetForm()
  dialogVisible.value = true
}

const editCamera = (row: any) => {

  router.push(`/cameras/edit/${row.id}`)
}

// 服务端持久化的新建默认值（settings/defaults），加载失败时保留内置兜底值
const serverDefaults = reactive({
  record_enabled: true, record_type: 'continuous',
  width: 1920, height: 1080, fps: 25, codec: 'h264', bitrate: 4096,
})
const loadServerDefaults = async () => {
  try {
    const res: any = await api.settings.getDefaults()
    if (res) Object.assign(serverDefaults, res)
  } catch { /* 用内置兜底值 */ }
}

const resetForm = () => {
  Object.assign(cameraForm, {
    name: '', description: '', protocol: 'onvif', ip: '', port: 80, path: '',
    onvif_address: '', discover_network: '', onvif_profile_token: '',
    username: '', password: '', rtsp_url: '',
    width: serverDefaults.width, height: serverDefaults.height,
    fps: serverDefaults.fps, codec: serverDefaults.codec,
    bitrate: serverDefaults.bitrate,
    ptz_enabled: false, record_enabled: serverDefaults.record_enabled,
    record_type: serverDefaults.record_type, record_schedule: '0-23'
  })


  detectedDevice.value = null
  detectError.value = ''
  detectSuccess.value = ''
  onIpBlur()
  cameraFormRef.value?.clearValidate()
}

const submitCamera = async () => {
  try {
    await cameraFormRef.value?.validate()
    submitLoading.value = true

    let payload: any

    if (isRtspMode.value) {
      let u: URL
      try {
        u = new URL(cameraForm.rtsp_url.trim())
      } catch {
        ElMessage.error(t('camerasList.rtspInvalid'))
        return
      }
      payload = {
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
    } else {

      payload = {
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
      }
    }

    if (editingId.value) {
      await api.cameras.update(editingId.value, payload)
      ElMessage.success(t('camerasList.updateSuccess'))
    } else {
      await api.cameras.create(payload)
      ElMessage.success(t('camerasList.createSuccess'))
    }
    dialogVisible.value = false
    fetchCameras()
  } catch (e) {
    console.error(e)
  } finally {
    submitLoading.value = false
  }
}

const toggleRecord = async (row: any) => {
  try {
    await api.cameras.update(row.id, { record_enabled: row.record_enabled })
    ElMessage.success(row.record_enabled ? t('camerasList.recordOn') : t('camerasList.recordOff'))
  } catch (e) {
    row.record_enabled = !row.record_enabled
    ElMessage.error(t('camerasList.opFailed'))
  }
}

const deleteCamera = (id: number) => {
  ElMessageBox.confirm(t('camerasList.confirmDelete'), t('camerasList.tips'), { type: 'warning' })
    .then(async () => {
      try {
        await api.cameras.delete(id)
        ElMessage.success(t('camerasList.deleteSuccess'))
        fetchCameras()
      } catch (e) { ElMessage.error(t('camerasList.deleteFailed')) }
    })
    .catch(() => {})
}


const lanScanDialogVisible = ref(false)
const lanScanResults = ref<any[]>([])
const lanScanning = ref(false)
const lanScanError = ref('')
const scanTimeout = ref(20)
const selectedLanDevice = ref<any>(null)

const openLanScan = () => {
  lanScanResults.value = []
  lanScanError.value = ''
  selectedLanDevice.value = null
  lanScanDialogVisible.value = true
}

const runLanScan = async () => {
  lanScanning.value = true
  lanScanError.value = ''
  lanScanResults.value = []
  selectedLanDevice.value = null
  try {
    const r: any = await api.cameras.discoverLAN(scanTimeout.value)
    if (r && Array.isArray(r)) {
      lanScanResults.value = r
      if (r.length === 0) {
        lanScanError.value = t('camerasList.lanNotFound')
        ElMessage.warning(t('camerasList.lanNotFoundShort'))
      } else {
        ElMessage.success(t('camerasList.lanFoundCount', { count: r.length }))
      }
    } else {
      lanScanError.value = t('camerasList.lanBadResponse')
    }
  } catch (e: any) {
    console.error(e)
    lanScanError.value = t('camerasList.lanScanFailed', { msg: e?.message || e?.response?.data?.error || t('camerasList.networkError') })
    ElMessage.error(lanScanError.value)
  } finally {
    lanScanning.value = false
  }
}

const selectLanDevice = (row: any) => {
  selectedLanDevice.value = row
}

const fillFromLanScan = () => {
  const d = selectedLanDevice.value
  if (!d) return
  showAddDialog()


  cameraForm.ip = d.ip || ''
  cameraForm.port = d.port || 80
  cameraForm.onvif_address = d.xaddr || ''
  cameraForm.name = d.name || d.manufacturer || d.model || `Camera_${(d.ip || '').split('.').pop() || ''}`
  if (!cameraForm.name && d.ip) cameraForm.name = `Camera_${d.ip}`
  detectedDevice.value = null
  lanScanDialogVisible.value = false
  if (d.auth_required) {
    ElMessage.success(t('camerasList.lanFilledAuth', { ip: cameraForm.ip }))
  } else {
    ElMessage.info(t('camerasList.lanFilledNoAuth', { ip: cameraForm.ip }))
  }
}

const goToDetail = (id: number) => router.push(`/cameras/${id}`)
const goToPlayback = (id: number) => router.push(`/recordings/playback/${id}`)

onMounted(() => { fetchCameras(); loadServerDefaults() })
</script>

<style scoped lang="scss">
.camera-list-page {
  .page-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 24px;
    h2 { margin: 0; font-size: 20px; font-weight: 600; }
    .header-actions { display: flex; gap: 12px; }
  }

  .search-form {
    :deep(.el-form-item) { margin-bottom: 0; }
  }

  .pagination { margin-top: 16px; text-align: right; }
}
</style>