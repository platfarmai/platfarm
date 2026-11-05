<template>
  <div class="playback-page" v-if="camera">

    <el-card :shadow="never" class="mb-16">
      <div class="playback-header">
        <div class="camera-info">
          <el-button link @click="goBack"><el-icon><ArrowLeft /></el-icon></el-button>
          <h2>{{ camera.name }} - {{ t('recordings.playback.subtitle') }}</h2>
        </div>
        <div class="playback-controls">
          <el-date-picker v-model="playDate" type="date" :placeholder="t('recordings.playback.selectDate')" value-format="YYYY-MM-DD" style="width: 160px" />
          <el-button :type="isPlaying ? 'success' : 'primary'" @click="togglePlay" :loading="playLoading">
            <el-icon><VideoPlay v-if="!isPlaying" /><VideoPause v-else /></el-icon>
            {{ isPlaying ? t('recordings.playback.pause') : t('recordings.playback.play') }}
          </el-button>
          <el-button @click="stopPlay"><el-icon><VideoPause /></el-icon> {{ t('recordings.playback.stop') }}</el-button>
          <el-button @click="downloadCurrent"><el-icon><Download /></el-icon> {{ t('recordings.playback.download') }}</el-button>
        </div>
      </div>
    </el-card>

    <el-row :gutter="24">

      <el-col :xs="24" :lg="16">
        <el-card :shadow="never" class="video-card">
          <div class="video-container aspect-16-9" ref="videoContainer">
            <video
              ref="videoPlayer"
              class="hls-video"
              playsinline
              controls
              @timeupdate="onTimeUpdate"
              @error="handleVideoError"
            ></video>
            <div class="loading-overlay" v-if="videoLoading">
              <el-icon class="loading-spinner"><Loading /></el-icon>
              <p>{{ loadingText }}</p>
            </div>
            <div class="video-placeholder" v-if="!currentSegment && !currentWebdav && !videoLoading">
              <el-icon><Film /></el-icon>
              <p>{{ t('recordings.playback.selectSegmentHint') }}</p>
            </div>


            <div class="playback-progress" @click="seek($event)">
              <div class="progress-track">
                <div class="progress-buffer" :style="{ width: bufferPercent + '%' }" />
                <div class="progress-played" :style="{ width: playedPercent + '%' }" />
                <div class="progress-thumb" :style="{ left: playedPercent + '%' }" v-show="isDragging || hoverProgress" />
              </div>
              <div class="progress-time">
                <span>{{ formatTime(currentTime) }}</span>
                <span>/</span>
                <span>{{ formatTime(duration) }}</span>
              </div>
            </div>
          </div>
        </el-card>


        <el-card :shadow="never" class="mt-16">
          <template #header>
            <h3>{{ t('recordings.playback.timelineTitle') }}</h3>
</template>
          <div class="timeline">
            <div class="timeline-track">
              <div class="timeline-segment" v-for="seg in segments" :key="seg.id" :style="segmentStyle(seg)" :class="{ 'segment-motion': seg.record_type === 'motion' }" @click="jumpToSegment(seg)">
                <div class="segment-indicator" />
              </div>
              <div class="timeline-cursor" :style="{ left: cursorPercent + '%' }" />
            </div>
            <div class="timeline-labels">
              <span v-for="i in 24" :key="i" :style="{ left: (i-1)/23*100 + '%' }">{{ String(i-1).padStart(2,'0') }}:00</span>
            </div>
          </div>
        </el-card>
      </el-col>

      <!-- 侧边栏：录像列表 -->
      <el-col :xs="24" :lg="8">
        <el-card :shadow="never">
          <template #header>
            <div class="card-header">
              <h3>{{ t('recordings.playback.dailyListTitle') }}</h3>
              <el-button size="small" @click="loadSegments"><el-icon><Refresh /></el-icon></el-button>
            </div>
</template>

          <div class="segment-list" v-if="segments.length > 0">
            <div class="segment-item" v-for="seg in displaySegments" :key="seg.id" :class="{ active: currentSegment?.id === seg.id }" @click="jumpToSegment(seg)">
              <div class="segment-type" :class="seg.record_type">
                {{ t('recordings.playback.typeLabel.' + seg.record_type) }}
              </div>
              <div class="segment-info">
                <p class="segment-time">{{ formatTime(seg.start_time) }} - {{ formatTime(seg.end_time) }}</p>
                <p class="segment-duration">{{ formatDuration(seg.duration) }} · {{ formatBytes(seg.file_size) }}</p>
              </div>
              <el-icon v-if="currentSegment?.id === seg.id"><VideoPlay class="playing" /></el-icon>
            </div>
            <div class="load-more" v-if="hiddenSegmentCount > 0">
              <el-button text type="primary" @click="loadMoreSegments">
                {{ t('recordings.playback.loadMore', { count: hiddenSegmentCount }) }}
              </el-button>
            </div>
          </div>
          <div class="empty-state" v-else>
            <el-icon><Film /></el-icon>
            <p>{{ t('recordings.playback.emptyDay') }}</p>
          </div>
        </el-card>

        <!-- WebDAV 云存储录像 -->
        <el-card :shadow="never" class="mt-16">
          <template #header>
            <div class="card-header">
              <h3><el-icon><Cloudy /></el-icon> {{ t('recordings.playback.webdavTitle') }}</h3>
              <el-button size="small" @click="loadWebdavFiles" :loading="webdavLoading">
                <el-icon><Refresh /></el-icon>
              </el-button>
            </div>
</template>
          <p class="webdav-hint" v-if="webdavEnabled === false">{{ t('recordings.playback.webdavDisabled') }}</p>
          <div class="segment-list" v-else-if="webdavFiles.length > 0">
            <div class="segment-item" v-for="f in webdavFiles" :key="f.path" :class="{ active: currentWebdav === f.path }" @click="playWebdavFile(f)">
              <div class="segment-type" :class="f.name.startsWith('motion_') ? 'motion' : 'continuous'">
                {{ t('recordings.playback.typeLabel.' + (f.name.startsWith('motion_') ? 'motion' : 'continuous')) }}
              </div>
              <div class="segment-info">
                <p class="segment-time webdav-file-name" :title="f.name">{{ f.name }}</p>
                <p class="segment-duration">{{ f.mod_time ? new Date(f.mod_time).toLocaleString(locale.value === 'en' ? 'en-US' : 'zh-CN') : '' }} · {{ formatBytes(f.size) }}</p>
              </div>
              <el-icon v-if="currentWebdav === f.path"><VideoPlay class="playing" /></el-icon>
            </div>
          </div>
          <div class="empty-state" v-else>
            <el-icon><Cloudy /></el-icon>
            <p>{{ t('recordings.playback.webdavEmpty') }}</p>
          </div>
        </el-card>

        <!-- MinIO 对象存储录像 -->
        <el-card :shadow="never" class="mt-16">
          <template #header>
            <div class="card-header">
              <h3><el-icon><Cloudy /></el-icon> {{ t('recordings.playback.minioTitle') }}</h3>
              <el-button size="small" @click="loadMinioFiles" :loading="minioLoading">
                <el-icon><Refresh /></el-icon>
              </el-button>
            </div>
</template>
          <p class="webdav-hint" v-if="minioEnabled === false">{{ t('recordings.playback.minioDisabled') }}</p>
          <div class="segment-list" v-else-if="minioFiles.length > 0">
            <div class="segment-item" v-for="f in minioFiles" :key="f.path" :class="{ active: currentMinio === f.path }" @click="playMinioFile(f)">
              <div class="segment-type" :class="f.name.startsWith('motion_') ? 'motion' : 'continuous'">
                {{ t('recordings.playback.typeLabel.' + (f.name.startsWith('motion_') ? 'motion' : 'continuous')) }}
              </div>
              <div class="segment-info">
                <p class="segment-time webdav-file-name" :title="f.name">{{ f.name }}</p>
                <p class="segment-duration">{{ f.mod_time ? new Date(f.mod_time).toLocaleString(locale.value === 'en' ? 'en-US' : 'zh-CN') : '' }} · {{ formatBytes(f.size) }}</p>
              </div>
              <el-icon v-if="currentMinio === f.path"><VideoPlay class="playing" /></el-icon>
            </div>
          </div>
          <div class="empty-state" v-else>
            <el-icon><Cloudy /></el-icon>
            <p>{{ t('recordings.playback.minioEmpty') }}</p>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
  <div class="loading-full" v-else>
    <el-icon class="loading-spinner"><Loading /></el-icon>
    <p>{{ t('recordings.playback.loading') }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft, VideoPlay, VideoPause, Download, Refresh, Loading, Film, Cloudy } from '@element-plus/icons-vue'
import { api } from '@/api'
import { useCameraStore, useAuthStore } from '@/stores'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()
const authStore = useAuthStore()
const route = useRoute()
const router = useRouter()
const cameraStore = useCameraStore()

const camera = ref<any>(null)
const videoContainer = ref<HTMLElement>()
const videoPlayer = ref<HTMLVideoElement>()
const videoLoading = ref(false)
const loadingText = ref('')
const isPlaying = ref(false)
const playLoading = ref(false)


const today = new Date()
const pad2 = (n: number) => String(n).padStart(2, '0')
const playDate = ref(`${today.getFullYear()}-${pad2(today.getMonth() + 1)}-${pad2(today.getDate())}`)
const segments = ref<any[]>([])
const currentSegment = ref<any>(null)


const SEGMENT_PAGE_SIZE = 50
const visibleCount = ref(SEGMENT_PAGE_SIZE)
const displaySegments = computed(() => [...segments.value].reverse().slice(0, visibleCount.value))
const hiddenSegmentCount = computed(() => Math.max(0, segments.value.length - displaySegments.value.length))
const loadMoreSegments = () => { visibleCount.value += SEGMENT_PAGE_SIZE }


const webdavEnabled = ref<boolean | null>(null)
const webdavFiles = ref<any[]>([])
const webdavLoading = ref(false)
const currentWebdav = ref<string | null>(null)


const minioEnabled = ref<boolean | null>(null)
const minioFiles = ref<any[]>([])
const minioLoading = ref(false)
const currentMinio = ref<string | null>(null)
const currentTime = ref(0)
const duration = ref(0)
const bufferPercent = ref(0)
const playedPercent = ref(0)
const isDragging = ref(false)
const hoverProgress = ref(false)
const cursorPercent = ref(0)

let segmentCheckTimer: any = null

const formatTime = (time: string | number) => {
  if (typeof time === 'number') { const m = Math.floor(time/60), s = Math.floor(time%60); return `${String(m).padStart(2,'0')}:${String(s).padStart(2,'0')}` }
  return time ? new Date(time).toLocaleTimeString(locale.value === 'en' ? 'en-US' : 'zh-CN', {hour:'2-digit',minute:'2-digit',second:'2-digit'}) : '00:00:00'
}
const formatDuration = (sec: number) => { const h = Math.floor(sec/3600), m = Math.floor((sec%3600)/60); return h>0?`${h}h${m}m`:`${m}m` }
const formatBytes = (bytes: number) => { if(!bytes) return '0 B'; const k=1024,sizes=['B','KB','MB','GB']; const i=Math.floor(Math.log(bytes)/Math.log(k)); return parseFloat((bytes/Math.pow(k,i)).toFixed(1))+' '+sizes[i] }

const loadCamera = async () => {
  try {
    const res = await api.cameras.get(Number(route.params.cameraId))
    camera.value = res.data || res
    loadSegments()
    loadWebdavFiles()
    loadMinioFiles()
  } catch (e) { ElMessage.error(t('recordings.playback.fetchCameraFailed')); router.push('/recordings') }
}

const loadSegments = async () => {
  if (!camera.value) return
  try {
    const start = `${playDate.value} 00:00:00`
    const end = `${playDate.value} 23:59:59`
    const res = await api.recordings.segments(camera.value.id, start, end)
    segments.value = res.data || res || []
    visibleCount.value = SEGMENT_PAGE_SIZE
  } catch (e) { ElMessage.error(t('recordings.playback.fetchSegmentsFailed')) }
}


const loadWebdavFiles = async () => {
  if (!camera.value) return
  webdavLoading.value = true
  try {
    const res: any = await api.webdav.list(camera.value.id)
    webdavEnabled.value = res.enabled !== false
    webdavFiles.value = (res.files || []).map((f: any) => ({
      name: f.name, path: f.path, size: f.size,
      mod_time: f.mod_time ? new Date(f.mod_time) : null,
    }))
  } catch (e) {
    webdavEnabled.value = null
    webdavFiles.value = []
  } finally {
    webdavLoading.value = false
  }
}


const playWebdavFile = async (f: any) => {
  currentWebdav.value = f.path
  currentSegment.value = null
  const video = videoPlayer.value
  if (!video) return
  videoLoading.value = true
  loadingText.value = t('recordings.playback.loadingWebdav')
  isPlaying.value = false
  video.pause()
  video.removeAttribute('src')
  video.load()
  await authStore.ensureMediaToken()
  video.src = api.webdav.fileUrl(f.path)
  video.onloadedmetadata = () => {
    videoLoading.value = false
    video.play().catch(() => {})
    isPlaying.value = true
  }
}


const loadMinioFiles = async () => {
  if (!camera.value) return
  minioLoading.value = true
  try {
    const res: any = await api.minio.list(camera.value.id)
    minioEnabled.value = res.enabled !== false
    minioFiles.value = (res.files || []).map((f: any) => ({
      name: f.name, path: f.path, size: f.size,
      mod_time: f.mod_time ? new Date(f.mod_time) : null,
    }))
  } catch (e) {
    minioEnabled.value = null
    minioFiles.value = []
  } finally {
    minioLoading.value = false
  }
}


const playMinioFile = async (f: any) => {
  currentMinio.value = f.path
  currentSegment.value = null
  const video = videoPlayer.value
  if (!video) return
  videoLoading.value = true
  loadingText.value = t('recordings.playback.loadingMinio')
  isPlaying.value = false
  video.pause()
  video.removeAttribute('src')
  video.load()
  await authStore.ensureMediaToken()
  video.src = api.minio.fileUrl(f.path)
  video.onloadedmetadata = () => {
    videoLoading.value = false
    video.play().catch(() => {})
    isPlaying.value = true
  }
}

const jumpToSegment = (seg: any) => {
  currentSegment.value = seg
  playSegment(seg)
}

const playSegment = async (seg: any) => {
  const video = videoPlayer.value
  if (!video) return
  currentWebdav.value = null
  currentMinio.value = null
  videoLoading.value = true
  loadingText.value = t('recordings.playback.loadingFile')
  isPlaying.value = false
  video.pause()


  video.removeAttribute('src')
  video.load()


  await authStore.ensureMediaToken()
  video.src = api.recordings.file(seg.id)
  video.onloadedmetadata = () => {
    videoLoading.value = false
    video.play().catch(() => {})
    isPlaying.value = true
  }
}

const onTimeUpdate = () => {
  if (!videoPlayer.value) return
  currentTime.value = videoPlayer.value.currentTime
  duration.value = videoPlayer.value.duration || 0
  playedPercent.value = duration.value ? (currentTime.value / duration.value) * 100 : 0
  bufferPercent.value = videoPlayer.value.buffered.length ? (videoPlayer.value.buffered.end(0) / duration.value) * 100 : 0


  if (currentSegment.value && duration.value) {
    const segStartMs = new Date(currentSegment.value.start_time).getTime()
    const posMs = segStartMs + videoPlayer.value.currentTime * 1000
    const dayStartMs = new Date(`${playDate.value}T00:00:00`).getTime()
    cursorPercent.value = Math.max(0, Math.min(100, ((posMs - dayStartMs) / 86400000) * 100))
  }
}

const seek = (e: MouseEvent) => {
  if (!videoPlayer.value || !videoContainer.value) return
  const rect = videoContainer.value.getBoundingClientRect()
  const percent = (e.clientX - rect.left) / rect.width
  videoPlayer.value.currentTime = percent * (videoPlayer.value.duration || 0)
}

const togglePlay = async () => {
  if (!videoPlayer.value) return
  playLoading.value = true
  try {
    if (isPlaying.value) { await videoPlayer.value.pause() } else { await videoPlayer.value.play() }
    isPlaying.value = !isPlaying.value
  } catch (e) { ElMessage.error(t('recordings.playback.operationFailed')) }
  finally { playLoading.value = false }
}

const stopPlay = () => {
  if (videoPlayer.value) {
    videoPlayer.value.pause()
    videoPlayer.value.removeAttribute('src')
    videoPlayer.value.load()
    videoPlayer.value.currentTime = 0
  }
  isPlaying.value = false
  currentSegment.value = null
  currentWebdav.value = null
  currentMinio.value = null
}

const downloadCurrent = async () => {
  if (!currentSegment.value && !currentWebdav.value) { ElMessage.warning(t('recordings.playback.selectSegmentFirst')); return }
  try {

    if (currentWebdav.value) {
      const name = currentWebdav.value.split('/').pop() || 'webdav_file.mp4'
      const a = document.createElement('a')
      await authStore.ensureMediaToken()
  a.href = api.webdav.fileUrl(currentWebdav.value)
      a.download = name
      a.target = '_blank'
      a.click()
      return
    }
    const res = await api.recordings.download(currentSegment.value.id)
    const url = window.URL.createObjectURL(new Blob([res]))
    const a = document.createElement('a')
    a.href = url; a.download = `playback_${currentSegment.value.id}.mp4`; a.click()
    URL.revokeObjectURL(url)
  } catch (e) { ElMessage.error(t('recordings.playback.downloadFailed')) }
}

const handleVideoError = () => { videoLoading.value = false; loadingText.value = t('recordings.playback.videoLoadError') }

const segmentStyle = (seg: any) => {
  const s = new Date(seg.start_time)
  const e = new Date(seg.end_time)
  let startH = s.getHours() + s.getMinutes() / 60 + s.getSeconds() / 3600
  let endH = e.getHours() + e.getMinutes() / 60 + e.getSeconds() / 3600
  if (endH <= startH) endH = startH + 0.02
  return { left: `${startH / 24 * 100}%`, width: `${Math.max(0.3, (endH - startH) / 24 * 100)}%` }
}

const goBack = () => router.push('/recordings')

watch(playDate, loadSegments)

onMounted(() => { loadCamera() })
onUnmounted(() => { if (segmentCheckTimer) clearInterval(segmentCheckTimer) })
</script>

<style scoped lang="scss">
.playback-page {
  .playback-header { display:flex; justify-content:space-between; align-items:center; flex-wrap:wrap; gap:16px; .camera-info{display:flex;align-items:center;gap:12px; h2{margin:0;font-size:20px;font-weight:600;}} .playback-controls{display:flex;gap:12px;align-items:center;} }
  .video-card { .video-container{position:relative;background:#000;border-radius:8px;overflow:hidden;} .loading-overlay{position:absolute;inset:0;background:rgba(0,0,0,0.7);display:flex;flex-direction:column;align-items:center;justify-content:center;color:#fff;gap:16px;z-index:10;.loading-spinner{font-size:32px;animation:spin 1s linear infinite;}} .video-placeholder{position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;color:#909399;gap:12px;z-index:4;font-size:13px;.el-icon{font-size:36px;opacity:0.5;}} .playback-progress{position:absolute;bottom:0;left:0;right:0;padding:8px 12px;background:linear-gradient(transparent,rgba(0,0,0,0.6));display:flex;flex-direction:column;gap:4px;z-index:5;pointer-events:none; .progress-track{height:4px;background:rgba(255,255,255,0.3);border-radius:2px;position:relative;cursor:pointer;pointer-events:auto; .progress-buffer{position:absolute;top:0;left:0;height:100%;background:rgba(255,255,255,0.4);border-radius:2px;} .progress-played{position:absolute;top:0;left:0;height:100%;background:#409eff;border-radius:2px;transition:width 0.1s;} .progress-thumb{position:absolute;top:-6px;width:16px;height:16px;background:#fff;border-radius:50%;transform:translateX(-50%);box-shadow:0 2px 6px rgba(0,0,0,0.3);} &:hover .progress-thumb{display:block;} } .progress-time{display:flex;justify-content:space-between;color:#fff;font-size:12px;} } }
  .timeline { .timeline-track{position:relative;height:40px;background:#f5f7fa;border-radius:8px;overflow:hidden; .timeline-segment{position:absolute;top:0;height:100%;background:#409eff;border-radius:4px;cursor:pointer;transition:all 0.2s;&:hover{opacity:0.8;transform:scaleY(1.2);}&.segment-motion{background:#f56c6c;} .segment-indicator{position:absolute;top:-4px;left:50%;width:8px;height:8px;background:#fff;border-radius:50%;transform:translateX(-50%);box-shadow:0 1px 3px rgba(0,0,0,0.2);} } .timeline-cursor{position:absolute;top:0;bottom:0;width:2px;background:#f56c6c;pointer-events:none;z-index:10;} } .timeline-labels{display:flex;justify-content:space-between;margin-top:8px;font-size:11px;color:#909399;} }
  .segment-list { .segment-item{display:flex;align-items:center;gap:12px;padding:12px;border-radius:8px;cursor:pointer;transition:background 0.2s;border:1px solid transparent;&:hover{background:#fafafa;border-color:#e6e9ed;}&.active{background:#eef7ff;border-color:#409eff;} .segment-type{width:48px;height:20px;border-radius:10px;display:flex;align-items:center;justify-content:center;font-size:10px;color:#fff;&.continuous{background:#409eff;}&.motion{background:#f56c6c;}&.schedule{background:#67c23a;}&.manual{background:#909399;} } .segment-info{flex:1;min-width:0;.segment-time{margin:0 0 2px;font-size:13px;color:#303133;}.segment-duration{margin:0;font-size:12px;color:#909399;}.webdav-file-name{font-size:12px;word-break:break-all;} } .playing{color:#409eff;animation:pulse 1s infinite;} .load-more{text-align:center;padding:8px 0 4px;border-top:1px solid #f0f1f3;} }
  .webdav-hint{margin:0;padding:16px 0;font-size:13px;color:#909399;text-align:center;} .empty-state{display:flex;flex-direction:column;align-items:center;justify-content:center;padding:40px 20px;color:#909399;font-size:14px;.el-icon{font-size:32px;margin-bottom:12px;opacity:0.5;}} }
  .loading-full{display:flex;flex-direction:column;align-items:center;justify-content:center;height:50vh;color:#909399;.loading-spinner{font-size:32px;animation:spin 1s linear infinite;}}
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}
</style>
