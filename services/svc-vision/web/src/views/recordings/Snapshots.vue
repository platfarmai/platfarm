<template>
  <div class="snapshots-page">
    <div class="page-header">
      <h2>{{ t('recordings.snapshot.title') }}</h2>
      <div class="header-actions">
        <el-button @click="fetchSnapshots"><el-icon><Refresh /></el-icon> {{ t('recordings.snapshot.refresh') }}</el-button>
        <el-button v-if="isAdmin" type="danger" @click="clearAll" :loading="clearLoading" :disabled="total === 0">
          <el-icon><Delete /></el-icon> {{ t('recordings.snapshot.clearAll') }}
        </el-button>
      </div>
    </div>


    <el-card :shadow="never" class="mb-16">
      <el-form :inline="true" :model="searchForm" class="search-form">
        <el-form-item :label="t('recordings.snapshot.camera')">
          <el-select v-model="searchForm.camera_id" :placeholder="t('recordings.snapshot.all')" style="width: 200px">
            <el-option v-for="cam in cameraStore.cameras" :key="cam.id" :label="cam.name" :value="cam.id" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('recordings.snapshot.timeRange')">
          <el-date-picker v-model="searchForm.time_range" type="datetimerange" :range-separator="'-'" :start-placeholder="t('recordings.snapshot.start')" :end-placeholder="t('recordings.snapshot.end')" style="width: 360px" value-format="YYYY-MM-DD HH:mm:ss" />
        </el-form-item>
        <el-form-item>
          <el-button @click="handleSearch"><el-icon><Search /></el-icon> {{ t('recordings.snapshot.search') }}</el-button>
          <el-button @click="resetSearch">{{ t('recordings.snapshot.reset') }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>


    <el-card :shadow="never">
      <el-table :data="tableData" v-loading="loading" border stripe size="small" row-key="id" style="width: 100%">
        <el-table-column :label="t('recordings.snapshot.colImage')" width="150">
          <template #default="scope">
            <el-image
              :src="snapUrl(scope.row)"
              :preview-src-list="[snapUrl(scope.row)]"
              :preview-teleported="true"
              fit="cover"
              lazy
              class="snap-thumb"
            >
              <template #error><div class="snap-error"><el-icon><Picture /></el-icon></div></template>
            </el-image>
          </template>
        </el-table-column>
        <el-table-column :label="t('recordings.snapshot.colCamera')" width="180">
          <template #default="scope">
            <span>{{ scope.row.camera_name || getCameraName(scope.row.camera_id) }}</span>
</template>
        </el-table-column>
        <el-table-column :label="t('recordings.snapshot.colTime')" width="200">
          <template #default="scope">{{ formatTime(scope.row.timestamp) }}</template>
        </el-table-column>
        <el-table-column :label="t('recordings.snapshot.colType')" width="100">
          <template #default="scope">
            <el-tag :type="typeMap[scope.row.type] || 'info'" size="small">{{ t('recordings.snapshot.typeLabel.' + scope.row.type) }}</el-tag>
</template>
        </el-table-column>
        <el-table-column :label="t('recordings.snapshot.colSize')" width="120">
          <template #default="scope">{{ formatBytes(scope.row.file_size) }}</template>
        </el-table-column>
        <el-table-column :label="t('recordings.snapshot.colActions')" width="110" fixed="right">
          <template #default="scope">
            <el-button link type="danger" @click.stop="deleteSnapshot(scope.row.id)">
              <el-icon><Delete /></el-icon> {{ t('recordings.snapshot.delete') }}
            </el-button>
</template>
        </el-table-column>
      </el-table>

      <div class="pagination" v-if="total > pageSize">
        <el-pagination v-model:current-page="page" v-model:page-size="pageSize" :total="total" :page-sizes="[20,50,100]" layout="total, sizes, prev, pager, next" @size-change="fetchSnapshots" @current-change="fetchSnapshots" />
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Delete, Search, Picture } from '@element-plus/icons-vue'
import { api } from '@/api'
import { useAuthStore, useCameraStore } from '@/stores'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()
const authStore = useAuthStore()
const isAdmin = computed(() => authStore.isAdmin)
const cameraStore = useCameraStore()

const loading = ref(false)
const tableData = ref<any[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const clearLoading = ref(false)

const searchForm = reactive({
  camera_id: '',
  time_range: <[string, string]>[],
})

const typeMap = { schedule: 'success', motion: 'warning', alert: 'danger', manual: 'info' }

const formatTime = (time: string) => time ? new Date(time).toLocaleString(locale.value === 'en' ? 'en-US' : 'zh-CN') : '-'
const formatBytes = (bytes: number) => { if (!bytes) return '0 B'; const k = 1024, sizes = ['B', 'KB', 'MB', 'GB']; const i = Math.floor(Math.log(bytes) / Math.log(k)); return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i] }

const getCameraName = (id: number) => cameraStore.cameras.find(c => c.id === id)?.name || `ID:${id}`

const snapUrl = (row: any) => api.snapshots.fileUrl(row.file_path, row.camera_id)

const fetchSnapshots = async () => {
  loading.value = true
  try {
    const params: any = { page: page.value, page_size: pageSize.value }
    if (searchForm.camera_id) params.camera_id = searchForm.camera_id
    if (searchForm.time_range?.length === 2) { params.start = searchForm.time_range[0]; params.end = searchForm.time_range[1] }
    const res: any = await api.snapshots.list(params)
    tableData.value = res || []
    total.value = res?._total || 0
  } catch (e) {
    ElMessage.error(t('recordings.snapshot.fetchFailed'))
  } finally {
    loading.value = false
  }
}

const handleSearch = () => { page.value = 1; fetchSnapshots() }
const resetSearch = () => { Object.keys(searchForm).forEach(k => searchForm[k] = ''); page.value = 1; fetchSnapshots() }

const deleteSnapshot = (id: number) => {
  ElMessageBox.confirm(t('recordings.snapshot.deleteConfirm'), t('recordings.snapshot.tips'), { type: 'warning' }).then(async () => {
    try {
      await api.snapshots.remove(id)
      ElMessage.success(t('recordings.snapshot.deleteSuccess'))
      if (tableData.value.length === 1 && page.value > 1) page.value--
      fetchSnapshots()
    } catch (e) { ElMessage.error(t('recordings.snapshot.deleteError')) }
  }).catch(() => {})
}


const clearAll = () => {
  if (total.value === 0) return
  ElMessageBox.confirm(t('recordings.snapshot.clearConfirm', { count: total.value }), t('recordings.snapshot.clearTitle'), {
    type: 'warning',
    confirmButtonText: t('recordings.snapshot.confirmDelete'),
    cancelButtonText: t('recordings.snapshot.cancel'),
    confirmButtonClass: 'el-button--danger',
  }).then(async () => {
    clearLoading.value = true
    try {
      const res: any = await api.snapshots.clear()
      ElMessage.success(res?.message || t('recordings.snapshot.cleared'))
      tableData.value = []
      total.value = 0
      page.value = 1
    } catch (e) { ElMessage.error(t('recordings.snapshot.clearFailed')) }
    finally { clearLoading.value = false }
  }).catch(() => {})
}

onMounted(async () => {
  await authStore.ensureMediaToken()
  fetchSnapshots()
  cameraStore.fetchCameras()
})
</script>

<style scoped lang="scss">
.snapshots-page {
  .page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px; h2 { margin: 0; font-size: 20px; font-weight: 600; } .header-actions { display: flex; gap: 12px; } }
  .search-form { :deep(.el-form-item) { margin-bottom: 0; } }
  .pagination { margin-top: 16px; text-align: right; }
  .snap-thumb { width: 120px; height: 68px; border-radius: 4px; display: block; cursor: pointer; }
  .snap-error { width: 100%; height: 100%; display: flex; align-items: center; justify-content: center; background: #f5f7fa; color: #c0c4cc; font-size: 20px; }
}
</style>