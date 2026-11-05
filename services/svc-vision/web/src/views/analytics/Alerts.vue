<template>
  <div class="alert-list-page">
    <div class="page-header">
      <h2>{{ t('analytics.title') }}</h2>
      <div class="header-actions">
        <el-button @click="fetchAlerts"><el-icon><Refresh /></el-icon> {{ t('analytics.refresh') }}</el-button>
        <el-button type="primary" @click="ackAllUnread" :disabled="unreadCount === 0">
          <el-icon><Check /></el-icon> {{ t('analytics.markAllRead') }}
        </el-button>
        <el-button type="danger" @click="clearAllAlerts" :loading="clearLoading" :disabled="total === 0">
          <el-icon><Delete /></el-icon> {{ t('analytics.clearAll') }}
        </el-button>
      </div>
    </div>

    <el-card :shadow="never" class="mb-16">
      <el-form :inline="true" :model="searchForm" class="search-form">
        <el-form-item :label="t('analytics.camera')">
          <el-select v-model="searchForm.camera_id" :placeholder="t('analytics.all')" style="width: 200px">
            <el-option v-for="cam in cameraStore.cameras" :key="cam.id" :label="cam.name" :value="cam.id" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('analytics.alertType')">
          <el-select v-model="searchForm.type" :placeholder="t('analytics.all')" style="width: 160px">
            <el-option :label="t('analytics.type.motion')" value="motion" />
            <el-option :label="t('analytics.type.intrusion')" value="intrusion" />
            <el-option :label="t('analytics.type.line_cross')" value="line_cross" />
            <el-option :label="t('analytics.type.object_detect')" value="object_detect" />
            <el-option :label="t('analytics.type.offline')" value="offline" />
            <el-option :label="t('analytics.type.storage_full')" value="storage_full" />
            <el-option :label="t('analytics.type.error')" value="error" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('analytics.alertLevel')">
          <el-select v-model="searchForm.level" :placeholder="t('analytics.all')" style="width: 140px">
            <el-option :label="t('analytics.level.low')" value="low" />
            <el-option :label="t('analytics.level.medium')" value="medium" />
            <el-option :label="t('analytics.level.high')" value="high" />
            <el-option :label="t('analytics.level.critical')" value="critical" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('analytics.handleStatus')">
          <el-select v-model="searchForm.status" :placeholder="t('analytics.all')" style="width: 140px">
            <el-option :label="t('analytics.status.new')" value="new" />
            <el-option :label="t('analytics.status.acknowledged')" value="acknowledged" />
            <el-option :label="t('analytics.status.resolved')" value="resolved" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('analytics.timeRange')">
          <el-date-picker v-model="searchForm.time_range" type="datetimerange" :range-separator="'-'" :start-placeholder="t('analytics.start')" :end-placeholder="t('analytics.end')" style="width: 360px" value-format="YYYY-MM-DD HH:mm:ss" />
        </el-form-item>
        <el-form-item>
          <el-button @click="handleSearch"><el-icon><Search /></el-icon> {{ t('analytics.search') }}</el-button>
          <el-button @click="resetSearch">{{ t('analytics.reset') }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card :shadow="never">
      <el-table :data="tableData" v-loading="loading" border stripe size="small" row-key="id" style="width: 100%" highlight-current-row @current-change="handleCurrentChange">
        <el-table-column type="selection" width="55" />
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column :label="t('analytics.camera')" width="180">
          <template #default="scope">
            <span>{{ getCameraName(scope.row.camera_id) }}</span>
</template>
        </el-table-column>
        <el-table-column :label="t('analytics.typeCol')" width="130">
          <template #default="scope">
            <el-tag :type="typeType(scope.row.type)" size="small">{{ t('analytics.type' + scope.row.type) }}</el-tag>
</template>
        </el-table-column>
        <el-table-column :label="t('analytics.levelCol')" width="100">
          <template #default="scope">
            <el-tag :type="levelType(scope.row.level)" size="small" effect="dark">{{ scope.row.level }}</el-tag>
</template>
        </el-table-column>
        <el-table-column prop="message" :label="t('analytics.content')" min-width="200" show-overflow-tooltip />
        <el-table-column :label="t('analytics.statusCol')" width="120">
          <template #default="scope">
            <el-tag :type="statusType(scope.row.status)" size="small">{{ t('analytics.status' + scope.row.status) }}</el-tag>
</template>
        </el-table-column>
        <el-table-column prop="created_at" :label="t('analytics.alertTime')" width="180">
          <template #default="scope">{{ formatTime(scope.row.created_at) }}</template>
        </el-table-column>
        <el-table-column :label="t('analytics.actions')" width="220" fixed="right">
          <template #default="scope">
            <el-button-group size="small">
              <el-button link @click.stop="viewSnapshot(scope.row)" v-if="scope.row.snapshot_path">
                <el-icon><Picture /></el-icon> {{ t('analytics.viewImage') }}
              </el-button>
              <el-button link type="primary" @click.stop="goToCamera(scope.row.camera_id)">
                <el-icon><VideoCamera /></el-icon> {{ t('analytics.locateCamera') }}
              </el-button>
              <el-button link @click.stop="ackAlert(scope.row)" v-if="scope.row.status === 'new'">
                <el-icon><Check /></el-icon> {{ t('analytics.confirm') }}
              </el-button>
              <el-button link @click.stop="resolveAlert(scope.row)" v-if="scope.row.status === 'acknowledged'">
                <el-icon><CircleCheck /></el-icon> {{ t('analytics.resolve') }}
              </el-button>
              <el-button link type="danger" @click.stop="deleteAlert(scope.row.id)">
                <el-icon><Delete /></el-icon> {{ t('analytics.delete') }}
              </el-button>
            </el-button-group>
</template>
        </el-table-column>
      </el-table>

      <div class="pagination" v-if="total > pageSize">
        <el-pagination v-model:current-page="page" v-model:page-size="pageSize" :total="total" :page-sizes="[10,20,50,100]" layout="total, sizes, prev, pager, next" @size-change="fetchAlerts" @current-change="fetchAlerts" />
      </div>
    </el-card>

    <!-- 报警详情对话框 -->
    <el-dialog v-model="detailVisible" :title="t('analytics.detailTitle')" width="800" destroy-on-close>
      <div class="alert-detail" v-if="currentAlert">
        <div class="detail-header">
          <div class="alert-badges">
            <el-tag :type="typeType(currentAlert.type)" size="medium">{{ t('analytics.type' + currentAlert.type) }}</el-tag>
            <el-tag :type="levelType(currentAlert.level)" size="medium" effect="dark">{{ currentAlert.level }}</el-tag>
            <el-tag :type="statusType(currentAlert.status)" size="medium">{{ t('analytics.status' + currentAlert.status) }}</el-tag>
          </div>
          <div class="alert-time">{{ formatTime(currentAlert.created_at) }}</div>
        </div>
        <div class="detail-content">
          <p>{{ currentAlert.message }}</p>
          <div class="detail-json" v-if="currentAlert.details">
            <pre>{{ formatJson(currentAlert.details) }}</pre>
          </div>
        </div>
        <div class="detail-image" v-if="currentAlert.snapshot_path">
          <h4>{{ t('analytics.snapshotTitle') }}</h4>
          <img :src="getImageUrl(currentAlert.snapshot_path)" :alt="t('analytics.snapshotAlt')" @error="handleImageError" />
        </div>
        <div class="detail-actions">
          <el-button @click="ackAlert(currentAlert)" v-if="currentAlert.status === 'new'" type="primary">{{ t('analytics.confirmAction') }}</el-button>
          <el-button @click="resolveAlert(currentAlert)" v-if="currentAlert.status === 'acknowledged'" type="success">{{ t('analytics.resolveAction') }}</el-button>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Check, CircleCheck, Search, Picture, VideoCamera, Delete } from '@element-plus/icons-vue'
import { api, mediaUrl } from '@/api'
import { useAuthStore, useCameraStore } from '@/stores'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()
const authStore = useAuthStore()

const cameraStore = useCameraStore()

const loading = ref(false)
const tableData = ref<any[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const unreadCount = ref(0)

const searchForm = reactive({
  camera_id: '',
  type: '',
  level: '',
  status: '',
  time_range: <[string, string]>[],
})

const detailVisible = ref(false)
const currentAlert = ref<any>(null)

const typeType = (t: string) => ({ motion: 'primary', intrusion: 'warning', line_cross: 'warning', object_detect: 'success', offline: 'danger', storage_full: 'warning', error: 'danger' }[t] || 'info')
const levelType = (l: string) => ({ low: 'success', medium: 'warning', high: 'danger', critical: 'danger' }[l] || 'info')
const statusType = (s: string) => ({ new: 'danger', acknowledged: 'warning', resolved: 'success' }[s] || 'info')

const formatTime = (time: string) => time ? new Date(time).toLocaleString(locale.value === 'en' ? 'en-US' : 'zh-CN') : '-'
const formatJson = (str: string) => { try { return JSON.stringify(JSON.parse(str), null, 2) } catch { return str } }
const getImageUrl = (path: string) => mediaUrl(path.replace('./recordings', '/api/vision/media/stream').replace(/\\/g, '/'))
const getCameraName = (id: number) => cameraStore.cameras.find(c => c.id === id)?.name || `ID:${id}`

const fetchAlerts = async () => {
  loading.value = true
  try {
    const params = { page: page.value, page_size: pageSize.value }
    if (searchForm.camera_id) params.camera_id = searchForm.camera_id
    if (searchForm.type) params.type = searchForm.type
    if (searchForm.level) params.level = searchForm.level
    if (searchForm.status) params.status = searchForm.status
    if (searchForm.time_range?.length === 2) { params.start = searchForm.time_range[0]; params.end = searchForm.time_range[1] }
    const res: any = await api.analytics.alerts(params)
    const all = res || []

    total.value = all._total ?? all.length
    unreadCount.value = all.filter((a: any) => a.status === 'new').length
    tableData.value = all
  } catch (e) { ElMessage.error(t('analytics.fetchListFailed')) }
  finally { loading.value = false }
}

const handleSearch = () => { page.value = 1; fetchAlerts() }
const resetSearch = () => { Object.keys(searchForm).forEach(k => searchForm[k] = ''); fetchAlerts() }

const handleCurrentChange = (row: any) => { currentAlert.value = row; detailVisible.value = true }

const viewSnapshot = (alert: any) => { currentAlert.value = alert; detailVisible.value = true }
const goToCamera = (id: number) => {  }

const ackAlert = async (alert: any) => {
  try { await api.analytics.acknowledge(alert.id); alert.status = 'acknowledged'; ElMessage.success(t('analytics.ackSuccess')); fetchAlerts() } catch(e) { ElMessage.error(t('analytics.operationFailed')) }
}
const resolveAlert = async (alert: any) => {
  try { await api.analytics.resolve(alert.id); alert.status = 'resolved'; ElMessage.success(t('analytics.resolveSuccess')); fetchAlerts() } catch(e) { ElMessage.error(t('analytics.operationFailed')) }
}
const deleteAlert = (id: number) => { ElMessageBox.confirm(t('analytics.confirmDeleteAlert'), t('analytics.tip'), {type:'warning'}).then(async()=>{try{await api.analytics.deleteAlert(id);ElMessage.success(t('analytics.deleteSuccess'));fetchAlerts()}catch(e){ElMessage.error(t('analytics.deleteFailed'))}}).catch(()=>{}) }
const ackAllUnread = async () => { try { const unread = tableData.value.filter(a => a.status === 'new'); for (const a of unread) { await api.analytics.acknowledge(a.id) } ElMessage.success(t('analytics.ackAllSuccess', { n: unread.length })); fetchAlerts() } catch(e) { ElMessage.error(t('analytics.batchAckFailed')) } }

const clearLoading = ref(false)
const clearAllAlerts = async () => {
  try {
    await ElMessageBox.confirm(
      t('analytics.confirmClearAll', { n: total.value }),
      t('analytics.clearAll'),
      { type: 'warning', confirmButtonText: t('analytics.confirmDelete'), cancelButtonText: t('analytics.cancel') }
    )
  } catch { return }
  clearLoading.value = true
  try {
    const res: any = await api.analytics.clearAlerts()
    tableData.value = []
    total.value = 0
    unreadCount.value = 0
    detailVisible.value = false
    ElMessage.success(res?.message || t('analytics.clearSuccess'))
  } catch (e) {
    ElMessage.error(t('analytics.deleteFailed'))
  } finally {
    clearLoading.value = false
  }
}

const handleImageError = (e: Event) => { (e.target as HTMLImageElement).style.display = 'none' }

onMounted(async () => {
  await authStore.ensureMediaToken()
  fetchAlerts()
  cameraStore.fetchCameras()
})
</script>

<style scoped lang="scss">
.alert-list-page { .page-header{display:flex;justify-content:space-between;align-items:center;margin-bottom:24px;h2{margin:0;font-size:20px;font-weight:600;}.header-actions{display:flex;gap:12px;}} .search-form{:deep(.el-form-item){margin-bottom:0;}} .pagination{margin-top:16px;text-align:right;} .alert-detail{.detail-header{display:flex;justify-content:space-between;align-items:flex-start;margin-bottom:16px;padding-bottom:16px;border-bottom:1px solid #f0f0f0;.alert-badges{display:flex;gap:8px;flex-wrap:wrap;}}.detail-content{margin-bottom:16px;p{margin:0 0 12px;font-size:14px;}.detail-json{background:#f5f7fa;border-radius:8px;padding:12px;max-height:200px;overflow:auto;pre{margin:0;font-size:12px;font-family:monospace;color:#606266;}}} .detail-image{margin-bottom:16px;h4{margin:0 0 8px;font-size:14px;}img{max-width:100%;border-radius:8px;border:1px solid #e6e9ed;}}.detail-actions{display:flex;justify-content:flex-end;gap:12px;padding-top:16px;border-top:1px solid #f0f0f0;}}
}
</style>
