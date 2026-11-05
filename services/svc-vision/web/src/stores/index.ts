import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  api,
  persistTokens,
  clearTokens,
  persistMediaToken,
  readCachedMediaToken,
  buildMediaWsUrl,
  type VisionUser,
  type MediaTokenInfo,
} from '@/api'

const ACCESS_KEY = 'pf_access_token'
const REFRESH_KEY = 'pf_refresh_token'

export const useAuthStore = defineStore('auth', () => {
  const accessToken = ref(localStorage.getItem(ACCESS_KEY) || '')
  const refreshToken = ref(localStorage.getItem(REFRESH_KEY) || '')
  const user = ref<VisionUser | null>(null)
  const mediaToken = ref(localStorage.getItem('pf_media_token') || '')
  const mediaExpiresAt = ref(localStorage.getItem('pf_media_expires') || '')
  const bootstrapped = ref(false)

  const isLoggedIn = computed(() => !!user.value)
  const isAdmin = computed(() => user.value?.role === 'admin')
  const isViewer = computed(() => !!user.value && user.value.role !== 'admin')

  const setTokens = (access: string, refresh: string) => {
    accessToken.value = access
    refreshToken.value = refresh
    persistTokens(access, refresh)
  }

  const setUser = (userInfo: VisionUser | null) => {
    user.value = userInfo
  }

  const clearSession = () => {
    accessToken.value = ''
    refreshToken.value = ''
    user.value = null
    mediaToken.value = ''
    mediaExpiresAt.value = ''
    clearTokens()
  }

  const login = async (username: string, password: string) => {
    const res = await api.auth.login(username, password)
    setTokens(res.accessToken, res.refreshToken)
    const me = await api.me()
    setUser(me)
    await ensureMediaToken(true)
    return me
  }

  const fetchMe = async (): Promise<VisionUser | null> => {
    const me = await api.me()
    setUser(me)
    return me
  }

  /** Cookie SSO / stored token bootstrap. Returns true if authenticated. */
  const bootstrap = async (): Promise<boolean> => {
    try {
      const me = await api.me()
      setUser(me)
      await ensureMediaToken(false)
      bootstrapped.value = true
      return true
    } catch {
      if (!accessToken.value) {
        clearSession()
      }
      bootstrapped.value = true
      return false
    }
  }

  const logout = async () => {
    try {
      await api.auth.logout(refreshToken.value || undefined)
    } catch {
      // ignore logout network errors; always clear local session
    }
    clearSession()
  }

  const mediaTokenValid = (): boolean => {
    if (!mediaToken.value || !mediaExpiresAt.value) return false
    const expires = Date.parse(mediaExpiresAt.value)
    if (Number.isNaN(expires)) return false
    return expires - Date.now() > 60_000
  }

  const ensureMediaToken = async (force = false): Promise<string> => {
    if (!force && mediaTokenValid()) {
      return mediaToken.value
    }
    const cached = readCachedMediaToken()
    if (!force && cached) {
      const expires = Date.parse(cached.expiresAt)
      if (!Number.isNaN(expires) && expires - Date.now() > 60_000) {
        mediaToken.value = cached.token
        mediaExpiresAt.value = cached.expiresAt
        return cached.token
      }
    }
    const info: MediaTokenInfo = await api.mediaToken()
    mediaToken.value = info.token
    mediaExpiresAt.value = info.expiresAt
    persistMediaToken(info.token, info.expiresAt)
    return info.token
  }

  const getMediaToken = async (): Promise<string> => ensureMediaToken(false)

  return {
    accessToken,
    refreshToken,
    user,
    mediaToken,
    mediaExpiresAt,
    bootstrapped,
    isLoggedIn,
    isAdmin,
    isViewer,
    setTokens,
    setUser,
    clearSession,
    login,
    fetchMe,
    bootstrap,
    logout,
    ensureMediaToken,
    getMediaToken,
  }
})

export const useCameraStore = defineStore('camera', () => {
  const cameras = ref<any[]>([])
  const currentCamera = ref<any>(null)
  const loading = ref(false)

  const onlineCount = computed(() => cameras.value.filter(c => c.status === 'online').length)
  const offlineCount = computed(() => cameras.value.filter(c => c.status === 'offline').length)

  const fetchCameras = async () => {
    loading.value = true
    try {
      const res = await api.cameras.list()
      cameras.value = (res as any)?.data || res
    } catch (e) {
      console.error('获取摄像头列表失败', e)
    } finally {
      loading.value = false
    }
  }

  const setCurrentCamera = (camera: any) => {
    currentCamera.value = camera
  }

  return { cameras, currentCamera, loading, onlineCount, offlineCount, fetchCameras, setCurrentCamera }
})

export const useAlertStore = defineStore('alert', () => {
  const alerts = ref<any[]>([])
  const unreadCount = ref(0)
  const ws = ref<WebSocket | null>(null)

  const connect = async () => {
    try {
      const auth = useAuthStore()
      const token = await auth.getMediaToken()
      const url = buildMediaWsUrl(token)
      ws.value = new WebSocket(url)

      ws.value.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data)
          if (msg.type === 'alert') {
            alerts.value.unshift(msg.data)
            unreadCount.value++
          }
        } catch (e) {
          console.error('WebSocket 消息解析失败', e)
        }
      }

      ws.value.onclose = () => {
        setTimeout(() => { void connect() }, 5000)
      }
    } catch (e) {
      console.error('WebSocket 连接失败', e)
      setTimeout(() => { void connect() }, 5000)
    }
  }

  const disconnect = () => {
    ws.value?.close()
    ws.value = null
  }

  const markRead = () => {
    unreadCount.value = 0
  }

  return { alerts, unreadCount, connect, disconnect, markRead }
})

export const useStorageStore = defineStore('storage', () => {
  const stats = ref<any>(null)
  const loading = ref(false)

  const fetchStats = async () => {
    loading.value = true
    try {
      stats.value = await api.storage.stats()
    } catch (e) {
      console.error('获取存储统计失败', e)
    } finally {
      loading.value = false
    }
  }

  return { stats, loading, fetchStats }
})
