<template>
  <div class="video-player" :class="['aspect-' + aspectRatio]">
    <video
      ref="videoRef"
      :src="src"
      :playsinline="true"
      :muted="muted"
      :controls="showControls"
      @error="handleError"
      @loadstart="onLoadStart"
      @loadeddata="onLoadedData"
      @waiting="onWaiting"
      @playing="onPlaying"
      @timeupdate="onTimeUpdate"
      @ended="onEnded"
    ></video>


    <div ref="hlsContainer" v-if="type === 'hls' && Hls.isSupported()" />


    <div class="player-overlay loading" v-if="loading">
      <div class="spinner" />
      <p>{{ loadingText }}</p>
    </div>


    <div class="player-overlay error" v-if="error">
      <el-icon class="error-icon"><VideoPause /></el-icon>
      <p>{{ error }}</p>
      <el-button type="primary" size="small" @click="retry">{{ t('videoPlayer.retry') }}</el-button>
    </div>


    <div class="controls" v-show="showControls && !loading && !error" :style="{ opacity: controlsVisible ? 1 : 0 }">
      <div class="controls-left">
        <el-tooltip :content="t('videoPlayer.playPause')" placement="top">
          <button class="control-btn" @click="togglePlay">
            <el-icon><VideoPlay v-if="paused" /><VideoPause v-else /></el-icon>
          </button>
        </el-tooltip>
        <el-tooltip :content="t('videoPlayer.mute')" placement="top">
          <button class="control-btn" @click="toggleMute">
            <el-icon><Microphone v-if="!muted" /><Mute v-else /></el-icon>
          </button>
        </el-tooltip>
        <el-tooltip :content="t('videoPlayer.volume')" placement="top">
          <el-slider v-model="volume" :max="1" :step="0.1" :vertical="false" style="width: 80px" show-input />
        </el-tooltip>
      </div>

      <div class="controls-center">
        <div class="progress-bar" @mousedown="onProgressMouseDown" @click="onProgressClick">
          <div class="progress-buffer" :style="{ width: bufferPercent + '%' }" />
          <div class="progress-played" :style="{ width: playedPercent + '%' }" />
          <div class="progress-thumb" :style="{ left: playedPercent + '%' }" v-show="dragging || hoverProgress" />
        </div>
        <span class="time-display">{{ formatTime(currentTime) }} / {{ formatTime(duration) }}</span>
      </div>

      <div class="controls-right">
        <el-tooltip :content="t('videoPlayer.pictureInPicture')" placement="top">
          <button class="control-btn" @click="togglePiP">
            <el-icon><FullScreen v-if="!isPiP" /><Fold v-else /></el-icon>
          </button>
        </el-tooltip>
        <el-tooltip :content="t('videoPlayer.fullscreen')" placement="top">
          <button class="control-btn" @click="toggleFullscreen">
            <el-icon><FullScreen v-if="!isFullscreen" /><Fold v-else /></el-icon>
          </button>
        </el-tooltip>
        <el-tooltip :content="playbackRate + 'x'" placement="top">
          <el-select v-model="playbackRate" :options="rateOptions" style="width: 70px" popper-class="rate-select" />
        </el-tooltip>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch, nextTick } from 'vue'
import Hls from 'hls.js'
import { VideoPlay, VideoPause, Microphone, Mute, FullScreen, Fold } from '@element-plus/icons-vue'
import { formatDuration } from '@/utils'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

interface Props {
  src: string
  type?: 'hls' | 'mp4' | 'flv'
  autoplay?: boolean
  muted?: boolean
  showControls?: boolean
  aspectRatio?: '16-9' | '4-3' | '1-1'
  onError?: (error: Error) => void
  onTimeUpdate?: (current: number, duration: number) => void
  onEnded?: () => void
}

const props = withDefaults(defineProps<Props>(), {
  type: 'hls',
  autoplay: true,
  muted: true,
  showControls: true,
  aspectRatio: '16-9',
})

const emit = defineEmits<{
  error: [error: Error]
  timeupdate: [current: number, duration: number]
  ended: []
  play: []
  pause: []
  loaded: []
}>

const videoRef = ref<HTMLVideoElement>()
const hlsContainer = ref<HTMLDivElement>()

const loading = ref(true)
const loadingText = ref(t('videoPlayer.loading'))
const error = ref<string>('')
const paused = ref(true)
const muted = ref(props.muted)
const volume = ref(1)
const currentTime = ref(0)
const duration = ref(0)
const bufferPercent = ref(0)
const playedPercent = ref(0)
const dragging = ref(false)
const hoverProgress = ref(false)
const controlsVisible = ref(true)
const isFullscreen = ref(false)
const isPiP = ref(false)
const playbackRate = ref(1)

const rateOptions = [
  { value: 0.5, label: '0.5x' },
  { value: 0.75, label: '0.75x' },
  { value: 1, label: '1x' },
  { value: 1.25, label: '1.25x' },
  { value: 1.5, label: '1.5x' },
  { value: 2, label: '2x' },
]

let hls: Hls | null = null
let controlsHideTimer: ReturnType<typeof setTimeout> | null = null

const initHls = async () => {
  if (!videoRef.value || !props.src) return

  loading.value = true
  error.value = ''

  if (hls) {
    hls.destroy()
    hls = null
  }

  if (props.type === 'hls' && Hls.isSupported()) {
    hls = new Hls({
      enableWorker: true,
      lowLatencyMode: true,
      maxBufferLength: 30,
      maxMaxBufferLength: 60,
      liveSyncDurationCount: 3,
    })

    hls.loadSource(props.src)
    hls.attachMedia(videoRef.value)

    hls.on(Hls.Events.MANIFEST_PARSED, () => {
      if (props.autoplay) videoRef.value?.play().catch(() => {})
      loading.value = false
      paused.value = false
      emit('loaded')
    })

    hls.on(Hls.Events.ERROR, (_, data) => {
      if (data.fatal) {
        error.value = t('videoPlayer.loadFailed')
        loading.value = false
        hls?.destroy()
        hls = null
        emit('error', new Error(data.details))
      }
    })


    setInterval(() => {
      if (hls && videoRef.value && !videoRef.value.paused) {
        bufferPercent.value = hls.bufferInfo().bufferLength / (duration.value || 1) * 100
      }
    }, 1000)
  } else if (videoRef.value.canPlayType('application/vnd.apple.mpegurl')) {

    videoRef.value.src = props.src
    videoRef.value.addEventListener('loadedmetadata', () => {
      if (props.autoplay) videoRef.value?.play().catch(() => {})
      loading.value = false
      paused.value = false
      emit('loaded')
    })
  } else {
    error.value = t('videoPlayer.hlsNotSupported')
    loading.value = false
  }
}

const handleError = (e: Event) => {
  const target = e.target as HTMLVideoElement
  error.value = t('videoPlayer.playError', { code: target.error?.code })
  loading.value = false
  emit('error', new Error(target.error?.message || 'Unknown error'))
}

const onLoadStart = () => { loading.value = true }
const onLoadedData = () => { loading.value = false }
const onWaiting = () => { loading.value = true; loadingText.value = t('videoPlayer.buffering') }
const onPlaying = () => { paused.value = false; loading.value = false; emit('play') }
const onTimeUpdate = () => {
  if (!videoRef.value) return
  currentTime.value = videoRef.value.currentTime
  duration.value = videoRef.value.duration || 0
  playedPercent.value = duration.value ? (currentTime.value / duration.value) * 100 : 0

  if (videoRef.value.buffered.length) {
    bufferPercent.value = (videoRef.value.buffered.end(0) / duration.value) * 100
  }

  emit('timeupdate', currentTime.value, duration.value)
}
const onEnded = () => { paused.value = true; emit('ended') }

const togglePlay = () => {
  if (!videoRef.value) return
  if (paused.value) videoRef.value.play() else videoRef.value.pause()
}

const toggleMute = () => {
  if (!videoRef.value) return
  videoRef.value.muted = !videoRef.value.muted
  muted.value = videoRef.value.muted
}

const toggleFullscreen = async () => {
  if (!videoRef.value) return
  try {
    if (!isFullscreen.value) {
      await videoRef.value.requestFullscreen()
    } else {
      await document.exitFullscreen()
    }
    isFullscreen.value = !isFullscreen.value
  } catch (e) { console.error(e) }
}

const togglePiP = async () => {
  if (!videoRef.value) return
  try {
    if (!isPiP.value) {
      await videoRef.value.requestPictureInPicture()
    } else {
      await document.exitPictureInPicture()
    }
    isPiP.value = !isPiP.value
  } catch (e) { console.error(e) }
}

const onProgressMouseDown = () => { dragging.value = true }
const onProgressClick = (e: MouseEvent) => {
  if (!videoRef.value || dragging.value) return
  const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
  const percent = (e.clientX - rect.left) / rect.width
  videoRef.value.currentTime = percent * (videoRef.value.duration || 0)
}

const retry = () => { initHls() }

const formatTime = (sec: number) => formatDuration(sec)


const showControls = () => {
  controlsVisible.value = true
  if (controlsHideTimer) clearTimeout(controlsHideTimer)
  controlsHideTimer = setTimeout(() => { controlsVisible.value = false }, 3000)
}

const hideControls = () => {
  if (controlsHideTimer) clearTimeout(controlsHideTimer)
  controlsVisible.value = false
}

watch(() => props.src, (newSrc) => {
  if (newSrc) initHls()
}, { immediate: true })

onMounted(() => {
  if (videoRef.value) {
    videoRef.value.playbackRate = playbackRate.value
    videoRef.value.addEventListener('mousemove', showControls)
    videoRef.value.addEventListener('mouseleave', hideControls)
  }
  document.addEventListener('mouseup', () => { dragging.value = false })
  document.addEventListener('fullscreenchange', () => { isFullscreen.value = !!document.fullscreenElement })
})

onUnmounted(() => {
  if (hls) { hls.destroy(); hls = null }
  if (controlsHideTimer) clearTimeout(controlsHideTimer)
})
</script>

<style scoped lang="scss">
.video-player {
  position: relative;
  width: 100%;
  background: #000;
  border-radius: 8px;
  overflow: hidden;
  font-family: inherit;

  &.aspect-16-9 { aspect-ratio: 16/9; }
  &.aspect-4-3 { aspect-ratio: 4/3; }
  &.aspect-1-1 { aspect-ratio: 1/1; }

  video { width: 100%; height: 100%; object-fit: contain; display: block; }

  .player-overlay {
    position: absolute; inset: 0;
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    background: rgba(0,0,0,0.7); color: #fff; z-index: 10;
    gap: 16px; text-align: center; padding: 20px;
    .spinner { width: 40px; height: 40px; border: 3px solid rgba(255,255,255,0.3); border-top-color: #fff; border-radius: 50%; animation: spin 1s linear infinite; }
    .error-icon { font-size: 48px; color: #f56c6c; }
    @keyframes spin { to { transform: rotate(360deg); } }
  }

  .controls {
    position: absolute; bottom: 0; left: 0; right: 0;
    display: flex; justify-content: space-between; align-items: center;
    padding: 8px 16px; z-index: 5;
    background: linear-gradient(transparent, rgba(0,0,0,0.6));
    transition: opacity 0.3s ease;

    .controls-left, .controls-center, .controls-right { display: flex; align-items: center; gap: 12px; }
    .controls-center { flex: 1; display: flex; flex-direction: column; gap: 4px; align-items: center; }

    .control-btn {
      background: rgba(255,255,255,0.2); border: none; border-radius: 4px;
      width: 36px; height: 36px; display: flex; align-items: center; justify-content: center;
      color: #fff; cursor: pointer; transition: background 0.2s;
      &:hover { background: rgba(255,255,255,0.3); }
    }

    .progress-bar {
      width: 100%; max-width: 600px; height: 4px;
      background: rgba(255,255,255,0.3); border-radius: 2px;
      position: relative; cursor: pointer;
      .progress-buffer, .progress-played { position: absolute; top: 0; left: 0; height: 100%; border-radius: 2px; }
      .progress-buffer { background: rgba(255,255,255,0.4); }
      .progress-played { background: #409eff; transition: width 0.1s linear; }
      .progress-thumb { position: absolute; top: -6px; width: 16px; height: 16px; background: #fff; border-radius: 50%; transform: translateX(-50%); box-shadow: 0 2px 6px rgba(0,0,0,0.3); }
    }

    .time-display { color: #fff; font-size: 12px; font-variant-numeric: tabular-nums; min-width: 140px; text-align: right; }

    .rate-select :deep(.el-select__wrapper) { font-size: 12px; }
    .rate-select :deep(.el-input__inner) { height: 30px; padding: 0 8px; }
  }
}
</style>