<template>
  <div class="recording-list-page">
    <div class="page-header">
      <h2>{{ t('recordings.list.title') }}</h2>
      <div class="header-actions">
        <el-button @click="fetchRecordings"><el-icon><Refresh /></el-icon> {{ t('recordings.list.refresh') }}</el-button>
        <el-button type="primary" @click="showCleanupDialog"><el-icon><Delete /></el-icon> {{ t('recordings.list.cleanupExpire') }}</el-button>
        <el-button type="danger" :disabled="multipleSelection.length === 0" @click="batchDelete">
          <el-icon><Delete /></el-icon> {{ t('recordings.list.batchDelete') }}{{ multipleSelection.length ? ` (${multipleSelection.length})` : '' }}
        </el-button>
      </div>
    </div>


    <el-card :shadow="never" class="mb-16">
      <el-form :inline="true" :model="searchForm" class="search-form">
        <el-form-item :label="t('recordings.list.camera')">
          <el-select v-model="searchForm.camera_id" :placeholder="t('recordings.list.all')" style="width: 200px">
            <el-option v-for="cam in cameraStore.cameras" :key="cam.id" :label="cam.name" :value="cam.id" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('recordings.list.recordType')">
          <el-select v-model="searchForm.record_type" :placeholder="t('recordings.list.all')" style="width: 140px">
            <el-option :label="t('recordings.list.typeContinuous')" value="continuous" />
            <el-option :label="t('recordings.list.typeMotion')" value="motion" />
            <el-option :label="t('recordings.list.typeSchedule')" value="schedule" />
            <el-option :label="t('recordings.list.typeManual')" value="manual" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('recordings.list.timeRange')">
          <el-date-picker v-model="searchForm.time_range" type="datetimerange" :range-separator="'-'" :start-placeholder="t('recordings.list.start')" :end-placeholder="t('recordings.list.end')" style="width: 360px" value-format="YYYY-MM-DD HH:mm:ss" />
        </el-form-item>
        <el-form-item>
          <el-button @click="handleSearch"><el-icon><Search /></el-icon> {{ t('recordings.list.search') }}</el-button>
          <el-button @click="resetSearch">{{ t('recordings.list.reset') }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>


    <el-card :shadow="never">
      <el-table :data="tableData" v-loading="loading" border stripe size="small" row-key="id" style="width: 100%" @selection-change="onSelectionChange">
        <el-table-column type="selection" width="55" />
        <el-table-column prop="camera_id" :label="t('recordings.list.colCamera')" width="180">
          <template #default="scope">
            <span>{{ getCameraName(scope.row.camera_id) }}</span>
</template>
        </el-table-column>
        <el-table-column :label="t('recordings.list.colTimeRange')" width="300">
          <template #default="scope">
            <div>{{ formatTime(scope.row.start_time) }}</div>
            <div class="text-muted">{{ formatTime(scope.row.end_time) }}</div>
</template>
        </el-table-column>
        <el-table-column :label="t('recordings.list.colDuration')" width="100">
          <template #default="scope">{{ formatDuration(scope.row.duration) }}</template>
        </el-table-column>
        <el-table-column prop="record_type" :label="t('recordings.list.colType')" width="120">
          <template #default="scope">
            <el-tag :type="typeMap[scope.row.record_type] || 'info'" size="small">{{ t('recordings.list.typeLabel.' + scope.row.record_type) }}</el-tag>
</template>
        </el-table-column>
        <el-table-column :label="t('recordings.list.colSize')" width="120">
          <template #default="scope">{{ formatBytes(scope.row.file_size) }}</template>
        </el-table-column>
        <el-table-column :label="t('recordings.list.colStorage')" width="100">
          <template #default="scope">
            <el-tag :type="scope.row.storage_type === 'local' ? 'success' : 'info'" size="small">{{ scope.row.storage_type }}</el-tag>
</template>
        </el-table-column>
        <el-table-column :label="t('recordings.list.colActions')" width="180" fixed="right">
          <template #default="scope">
            <el-button-group size="small">
              <el-button link type="primary" @click.stop="playRecording(scope.row)">
                <el-icon><VideoPlay /></el-icon> {{ t('recordings.list.play') }}
              </el-button>
              <el-button link @click.stop="downloadRecording(scope.row)">
                <el-icon><Download /></el-icon> {{ t('recordings.list.download') }}
              </el-button>
              <el-button link type="danger" @click.stop="deleteRecording(scope.row.id)">
                <el-icon><Delete /></el-icon> {{ t('recordings.list.delete') }}
              </el-button>
            </el-button-group>
</template>
        </el-table-column>
      </el-table>

      <div class="pagination" v-if="total > pageSize">
        <el-pagination v-model:current-page="page" v-model:page-size="pageSize" :total="total" :page-sizes="[10,20,50,100]" layout="total, sizes, prev, pager, next" @size-change="fetchRecordings" @current-change="fetchRecordings" />
      </div>
    </el-card>

    <!-- 清理对话框 -->
    <el-dialog v-model="cleanupVisible" :title="t('recordings.list.cleanupTitle')" width="400">
      <p>{{ t('recordings.list.cleanupConfirm', { days: cleanupDays }) }}</p>
      <el-form :model="cleanupForm" label-width="80">
        <el-form-item :label="t('recordings.list.retentionDays')">
          <el-input-number v-model="cleanupForm.days" :min="1" :max="365" :controls="false" style="width: 100%" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="cleanupVisible = false">{{ t('recordings.list.cancel') }}</el-button>
        <el-button type="danger" @click="confirmCleanup">{{ t('recordings.list.confirmCleanup') }}</el-button>
</template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Delete, Search, Download, VideoPlay } from '@element-plus/icons-vue'
import { api } from '@/api'
import { useCameraStore } from '@/stores'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()
const router = useRouter()

const cameraStore = useCameraStore()

const loading = ref(false)
const tableData = ref<any[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)

const searchForm = reactive({
  camera_id: '',
  record_type: '',
  time_range: <[string, string]>[],
})

const cleanupVisible = ref(false)
const cleanupDays = 7
const cleanupForm = reactive({ days: 7 })

const typeMap = { continuous: 'primary', motion: 'warning', schedule: 'success', manual: 'info' }

const formatTime = (time: string) => time ? new Date(time).toLocaleString(locale.value === 'en' ? 'en-US' : 'zh-CN') : '-'
const formatDuration = (sec: number) => { const h = Math.floor(sec/3600), m = Math.floor((sec%3600)/60), s = sec%60; return `${h}h${m}m${s}s` }
const formatBytes = (bytes: number) => { if(!bytes) return '0 B'; const k=1024,sizes=['B','KB','MB','GB']; const i=Math.floor(Math.log(bytes)/Math.log(k)); return parseFloat((bytes/Math.pow(k,i)).toFixed(1))+' '+sizes[i] }

const getCameraName = (id: number) => cameraStore.cameras.find(c => c.id === id)?.name || `ID:${id}`

const fetchRecordings = async () => {
  loading.value = true
  try {
    const params = { page: page.value, page_size: pageSize.value }
    if (searchForm.camera_id) params.camera_id = searchForm.camera_id
    if (searchForm.record_type) params.record_type = searchForm.record_type
    if (searchForm.time_range?.length === 2) { params.start = searchForm.time_range[0]; params.end = searchForm.time_range[1] }
    const res = await api.recordings.list(params)
    const all = res.data || res || []
    total.value = all.length
    const start = (page.value - 1) * pageSize.value
    tableData.value = all.slice(start, start + pageSize.value)
  } catch (e) { ElMessage.error(t('recordings.list.fetchFailed')) }
  finally { loading.value = false }
}

const handleSearch = () => { page.value = 1; fetchRecordings() }
const resetSearch = () => { Object.keys(searchForm).forEach(k => searchForm[k] = ''); fetchRecordings() }

const playRecording = (rec: any) => { router.push(`/recordings/playback/${rec.camera_id}`) }
const downloadRecording = async (rec: any) => { try { const res = await api.recordings.download(rec.id); const url = window.URL.createObjectURL(new Blob([res])); const a = document.createElement('a'); a.href = url; a.download = `recording_${rec.id}.mp4`; a.click(); URL.revokeObjectURL(url) } catch(e) { ElMessage.error(t('recordings.list.downloadFailed')) } }
const deleteRecording = (id: number) => { ElMessageBox.confirm(t('recordings.list.deleteConfirm'), t('recordings.list.tips'), {type:'warning'}).then(async()=>{try{await api.recordings.delete(id);ElMessage.success(t('recordings.list.deleteSuccess'));fetchRecordings()}catch(e){ElMessage.error(t('recordings.list.deleteError'))}}).catch(()=>{}) }

const multipleSelection = ref<any[]>([])
const onSelectionChange = (rows: any[]) => { multipleSelection.value = rows }

const batchDelete = () => {
  const count = multipleSelection.value.length
  if (count === 0) return
  ElMessageBox.confirm(t('recordings.list.batchDeleteConfirm', { count }), t('recordings.list.tips'), { type: 'warning' })
    .then(async () => {
      let ok = 0
      let fail = 0
      for (const row of multipleSelection.value) {
        try { await api.recordings.delete(row.id); ok++ } catch { fail++ }
      }
      if (fail === 0) {
        ElMessage.success(t('recordings.list.batchDeleteDone', { count: ok }))
      } else {
        ElMessage.warning(t('recordings.list.batchDeletePartial', { ok, fail }))
      }
      fetchRecordings()
    })
    .catch(() => {})
}

const showCleanupDialog = () => { cleanupVisible.value = true }
const confirmCleanup = async () => { try { await api.storage.cleanup(); ElMessage.success(t('recordings.list.cleanupDone')); fetchRecordings(); cleanupVisible.value = false } catch(e) { ElMessage.error(t('recordings.list.cleanupError')) } }

onMounted(() => { fetchRecordings(); cameraStore.fetchCameras() })
</script>

<style scoped lang="scss">
.recording-list-page { .page-header { display:flex; justify-content:space-between; align-items:center; margin-bottom:24px; h2{margin:0;font-size:20px;font-weight:600;} .header-actions{display:flex;gap:12px;} } .search-form{:deep(.el-form-item){margin-bottom:0;}} .pagination{margin-top:16px;text-align:right;} .text-muted{color:#909399;font-size:12px;} }
</style>