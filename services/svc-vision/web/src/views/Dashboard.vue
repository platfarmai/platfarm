<template>
  <div class="dashboard">

    <el-row :gutter="24" class="mb-24">
      <el-col :xs="24" :sm="12" :lg="6" v-for="card in statCards" :key="card.key">
        <el-card :shadow="never" class="stat-card" :class="card.key">
          <div class="stat-content">
            <div class="stat-info">
              <p class="stat-label">{{ card.label }}</p>
              <p class="stat-value">{{ card.value }}</p>
            </div>
            <div class="stat-icon" :class="card.key">
              <el-icon><component :is="card.icon" /></el-icon>
            </div>
          </div>
          <div class="stat-trend" v-if="card.trend !== undefined">
            <el-icon :class="card.trend >= 0 ? 'trend-up' : 'trend-down'">
              <ArrowUp v-if="card.trend >= 0" /><ArrowDown v-else />
            </el-icon>
            <span>{{ card.trend >= 0 ? '+' : '' }}{{ card.trend }}%</span>
            <span class="trend-label">{{ t('dashboard.trendWeek') }}</span>
          </div>
        </el-card>
      </el-col>
    </el-row>


    <el-row :gutter="24">

      <el-col :xs="24" :lg="16">
        <el-card :shadow="never">
          <template #header>
            <div class="card-header">
              <h3>{{ t("dashboard.camerasTitle") }}</h3>
              <el-button size="small" type="primary" @click="refreshCameras" :loading="cameraLoading">
                <el-icon><Refresh /></el-icon> {{ t("common.refresh") }}
              </el-button>
            </div>
</template>

          <el-table :data="cameraTableData" border stripe size="small" style="width: 100%">
            <el-table-column prop="name" :label="t('dashboard.colName')" width="200" />
            <el-table-column prop="ip" :label="t('dashboard.colIp')" width="140" />
            <el-table-column :label="t('dashboard.colStatus')" width="100">
              <template #default="scope">
                <el-tag :class="['status-tag', scope.row.status]" size="small">
                  {{ t('dashboard.status' + scope.row.status) }}
                </el-tag>
</template>
            </el-table-column>
            <el-table-column prop="width" :label="t('dashboard.colResolution')" width="120">
              <template #default="scope">
                {{ scope.row.width }}×{{ scope.row.height }}
</template>
            </el-table-column>
            <el-table-column :label="t('dashboard.colRecord')" width="80">
              <template #default="scope">
                <el-tag v-if="scope.row.record_enabled" type="success" size="small">{{ t("dashboard.recordOn") }}</el-tag>
                <el-tag v-else type="info" size="small">{{ t("dashboard.recordOff") }}</el-tag>
</template>
            </el-table-column>
            <el-table-column :label="t('dashboard.colActions')" width="200">
              <template #default="scope">
                <el-button-group size="small">
                  <el-button link type="primary" @click="goToDetail(scope.row.id)">
                    <el-icon><Monitor /></el-icon> {{ t("dashboard.preview") }}
                  </el-button>
                  <el-button link @click="goToPlayback(scope.row.id)">
                    <el-icon><Film /></el-icon> {{ t("dashboard.playback") }}
                  </el-button>
                </el-button-group>
</template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>

      <!-- 最近报警 -->
      <el-col :xs="24" :lg="8">
        <el-card :shadow="never">
          <template #header>
            <div class="card-header">
              <h3>{{ t("dashboard.recentAlerts") }}</h3>
              <el-button size="small" link @click="goToAlerts">{{ t("dashboard.viewAll") }}</el-button>
            </div>
</template>

          <div class="alert-list" v-if="recentAlerts.length > 0">
            <div class="alert-item" v-for="alert in recentAlerts" :key="alert.id">
              <div class="alert-icon" :class="alert.level">
                <el-icon><Warning /></el-icon>
              </div>
              <div class="alert-content">
                <p class="alert-message">{{ alert.message }}</p>
                <p class="alert-meta">
                  <span>{{ formatTime(alert.created_at) }}</span>
                  <el-tag :type="levelType(alert.level)" size="small">{{ t("utils.alertLevel." + alert.level) }}</el-tag>
                </p>
              </div>
            </div>
          </div>
          <div class="empty-state" v-else>
            <el-icon><Bell /></el-icon>
            <p>{{ t("dashboard.noAlerts") }}</p>
          </div>
        </el-card>

        <!-- 存储概览 -->
        <el-card :shadow="never" class="mt-16">
          <template #header>
            <h3>{{ t("dashboard.storageTitle") }}</h3>
</template>

          <div class="storage-overview" v-if="storageStats">
            <div class="storage-bar">
              <div class="storage-bar-track">
                <div class="storage-bar-fill" :style="{ width: storagePercent + '%' }" />
              </div>
              <div class="storage-bar-labels">
                <span>{{ formatBytes(storageStats.used_space) }}</span>
                <span>{{ formatBytes(storageStats.total_space) }}</span>
              </div>
            </div>
            <div class="storage-details">
              <div class="detail-item">
                <span class="detail-label">{{ t("dashboard.storageUsed") }}</span>
                <span class="detail-value">{{ storagePercent.toFixed(1) }}%</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">{{ t("dashboard.storageFree") }}</span>
                <span class="detail-value">{{ formatBytes(storageStats.free_space) }}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">{{ t("dashboard.storageSegments") }}</span>
                <span class="detail-value">{{ storageStats.recording_count }}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">{{ t("dashboard.storageSnapshots") }}</span>
                <span class="detail-value">{{ storageStats.snapshot_count }}</span>
              </div>
            </div>
          </div>
          <div class="empty-state" v-else>
            <el-icon><Memo /></el-icon>
            <p>{{ t("dashboard.storageLoading") }}</p>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  VideoCamera, Monitor, Film, Warning, Bell, Memo,
  ArrowUp, ArrowDown, Refresh, User, Setting
} from '@element-plus/icons-vue'
import { api } from '@/api'
import { useCameraStore, useAlertStore, useStorageStore } from '@/stores'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()

const router = useRouter()
const cameraStore = useCameraStore()
const alertStore = useAlertStore()
const storageStore = useStorageStore()

const cameraLoading = ref(false)
const cameraTableData = ref<any[]>([])
const recentAlerts = ref<any[]>([])
const storageStats = ref<any>(null)

const statCards = computed(() => [
  { key: 'cameras', label: t('dashboard.statCameras'), value: cameraStore.cameras.length, icon: VideoCamera, trend: 0 },
  { key: 'online', label: t('dashboard.statOnline'), value: cameraStore.onlineCount, icon: Monitor, trend: 0 },
  { key: 'alerts', label: t('dashboard.statAlertsToday'), value: 0, icon: Warning, trend: 12 },
  { key: 'storage', label: t('dashboard.statStorage'), value: storageStats.value ? ((storageStats.value.used_space / storageStats.value.total_space) * 100).toFixed(1) + '%' : '--', icon: Memo, trend: -5 },
])


const levelType = (level: string) => {
  const map: Record<string, any> = {
    low: 'success',
    medium: 'warning',
    high: 'danger',
    critical: 'danger',
  }
  return map[level] || 'info'
}

const formatTime = (time: string) => {
  return new Date(time).toLocaleString(locale.value === 'en' ? 'en-US' : 'zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

const formatBytes = (bytes: number) => {
  if (!bytes) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

const storagePercent = computed(() => {
  if (!storageStats.value?.total_space) return 0
  return (storageStats.value.used_space / storageStats.value.total_space) * 100
})

const refreshCameras = async () => {
  cameraLoading.value = true
  await cameraStore.fetchCameras()
  cameraTableData.value = cameraStore.cameras
  cameraLoading.value = false
}

const loadDashboardData = async () => {
  await cameraStore.fetchCameras()
  cameraTableData.value = cameraStore.cameras


  try {
    const res = await api.analytics.alerts({ page: 1, page_size: 10 })
    recentAlerts.value = res.data || res || []
  } catch (e) {
    console.error(e)
  }


  await storageStore.fetchStats()
  storageStats.value = storageStore.stats
}

const goToDetail = (id: number) => router.push(`/cameras/${id}`)
const goToPlayback = (id: number) => router.push(`/recordings/playback/${id}`)
const goToAlerts = () => router.push('/analytics')

onMounted(() => {
  loadDashboardData()
  alertStore.connect()
})

onMounted(() => {
  alertStore.connect()
})
</script>

<style scoped lang="scss">
.dashboard {
  .stat-card {
    border-radius: 12px;
    border-left: 4px solid;
    transition: transform 0.2s, box-shadow 0.2s;

    &:hover {
      transform: translateY(-2px);
      box-shadow: 0 8px 24px rgba(0,0,0,0.1);
    }

    &.cameras { border-color: #409eff; }
    &.online { border-color: #67c23a; }
    &.alerts { border-color: #f56c6c; }
    &.storage { border-color: #e6a23c; }

    .stat-content {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;

      .stat-info {
        .stat-label { margin: 0 0 4px; font-size: 14px; color: #909399; }
        .stat-value { margin: 0; font-size: 28px; font-weight: 700; color: #303133; }
      }

      .stat-icon {
        width: 56px;
        height: 56px;
        border-radius: 12px;
        display: flex;
        align-items: center;
        justify-content: center;
        font-size: 24px;

        &.cameras { background: rgba(64,158,255,0.15); color: #409eff; }
        &.online { background: rgba(103,194,58,0.15); color: #67c23a; }
        &.alerts { background: rgba(245,108,108,0.15); color: #f56c6c; }
        &.storage { background: rgba(230,162,60,0.15); color: #e6a23c; }
      }
    }

    .stat-trend {
      margin-top: 16px;
      padding-top: 12px;
      border-top: 1px solid #f0f0f0;
      display: flex;
      align-items: center;
      gap: 4px;
      font-size: 13px;

      .trend-up { color: #67c23a; }
      .trend-down { color: #f56c6c; }
      .trend-label { color: #909399; margin-left: 4px; }
    }
  }

  .card-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    h3 { margin: 0; font-size: 16px; font-weight: 600; }
  }

  .alert-list {
    .alert-item {
      display: flex;
      gap: 12px;
      padding: 12px 0;
      border-bottom: 1px solid #f0f0f0;

      &:last-child { border-bottom: none; }

      .alert-icon {
        width: 36px;
        height: 36px;
        border-radius: 8px;
        display: flex;
        align-items: center;
        justify-content: center;
        font-size: 16px;
        flex-shrink: 0;

        &.low { background: #f0f9eb; color: #67c23a; }
        &.medium { background: #fdf6ec; color: #e6a23c; }
        &.high, &.critical { background: #fef0f0; color: #f56c6c; }
      }

      .alert-content { flex: 1; min-width: 0; }
      .alert-message { margin: 0 0 4px; font-size: 13px; color: #303133; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
      .alert-meta { margin: 0; font-size: 12px; color: #909399; display: flex; align-items: center; gap: 8px; }
    }
  }

  .storage-overview {
    .storage-bar {
      .storage-bar-track {
        height: 8px;
        background: #e6e9ed;
        border-radius: 4px;
        overflow: hidden;
      }
      .storage-bar-fill {
        height: 100%;
        background: linear-gradient(90deg, #409eff, #67c23a);
        border-radius: 4px;
        transition: width 0.5s ease;
      }
      .storage-bar-labels {
        display: flex;
        justify-content: space-between;
        margin-top: 8px;
        font-size: 12px;
        color: #909399;
      }
    }

    .storage-details {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 12px;
      margin-top: 16px;
      padding-top: 16px;
      border-top: 1px solid #f0f0f0;
      .detail-item {
        display: flex;
        justify-content: space-between;
        .detail-label { color: #909399; font-size: 13px; }
        .detail-value { color: #303133; font-weight: 500; font-size: 13px; }
      }
    }
  }

  .empty-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 40px 20px;
    color: #909399;
    font-size: 14px;
    .el-icon { font-size: 32px; margin-bottom: 12px; opacity: 0.5; }
  }
}
</style>