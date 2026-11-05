import axios, { AxiosInstance, InternalAxiosRequestConfig, AxiosResponse, AxiosError } from 'axios'
import { ElMessage } from 'element-plus'
import i18n from '@/i18n'

const API_BASE = '/api/vision'
const ACCESS_KEY = 'pf_access_token'
const REFRESH_KEY = 'pf_refresh_token'
const MEDIA_TOKEN_KEY = 'pf_media_token'
const MEDIA_EXPIRES_KEY = 'pf_media_expires'

export type VisionRole = 'admin' | 'user'

export interface VisionUser {
  userId: string
  username: string
  role: VisionRole
  tenantId?: string
}

export interface MediaTokenInfo {
  token: string
  expiresAt: string
}

function getAccessToken(): string {
  return localStorage.getItem(ACCESS_KEY) || ''
}

function getRefreshToken(): string {
  return localStorage.getItem(REFRESH_KEY) || ''
}

export function persistTokens(accessToken: string, refreshToken: string): void {
  localStorage.setItem(ACCESS_KEY, accessToken)
  localStorage.setItem(REFRESH_KEY, refreshToken)
}

export function clearTokens(): void {
  localStorage.removeItem(ACCESS_KEY)
  localStorage.removeItem(REFRESH_KEY)
  localStorage.removeItem(MEDIA_TOKEN_KEY)
  localStorage.removeItem(MEDIA_EXPIRES_KEY)
}

export function persistMediaToken(token: string, expiresAt: string): void {
  localStorage.setItem(MEDIA_TOKEN_KEY, token)
  localStorage.setItem(MEDIA_EXPIRES_KEY, expiresAt)
}

export function readCachedMediaToken(): MediaTokenInfo | null {
  const token = localStorage.getItem(MEDIA_TOKEN_KEY) || ''
  const expiresAt = localStorage.getItem(MEDIA_EXPIRES_KEY) || ''
  if (!token || !expiresAt) return null
  return { token, expiresAt }
}

function loginPath(): string {
  return `${import.meta.env.BASE_URL}login`.replace(/\/{2,}/g, '/').replace(':/', '://')
}

function redirectToLogin(): void {
  const path = loginPath()
  if (!window.location.pathname.endsWith('/login') && !window.location.pathname.endsWith('/login/')) {
    window.location.assign(path)
  }
}

/** Append signed media token query to a media URL. */
export function mediaUrl(path: string, mediaToken?: string): string {
  const token = mediaToken ?? (localStorage.getItem(MEDIA_TOKEN_KEY) || '')
  if (!token) return path
  const sep = path.includes('?') ? '&' : '?'
  return `${path}${sep}token=${encodeURIComponent(token)}`
}

const request: AxiosInstance = axios.create({
  baseURL: API_BASE,
  timeout: 30000,
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
})

const platformAuth: AxiosInstance = axios.create({
  baseURL: '',
  timeout: 30000,
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
})

let refreshing: Promise<string | null> | null = null

async function refreshAccessToken(): Promise<string | null> {
  const refreshToken = getRefreshToken()
  if (!refreshToken) return null
  try {
    const { data } = await platformAuth.post('/auth/refresh', { refreshToken })
    const accessToken = data?.accessToken as string | undefined
    const nextRefresh = data?.refreshToken as string | undefined
    if (!accessToken || !nextRefresh) return null
    persistTokens(accessToken, nextRefresh)
    return accessToken
  } catch {
    clearTokens()
    return null
  }
}

request.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    const token = getAccessToken()
    if (token && config.headers) {
      config.headers.Authorization = `Bearer ${token}`
    }
    if (config.headers) {
      config.headers['Accept-Language'] = i18n.global.locale.value === 'en' ? 'en' : 'zh-CN'
    }
    return config
  },
  (error) => Promise.reject(error)
)

platformAuth.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    const token = getAccessToken()
    if (token && config.headers && !config.headers.Authorization) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

request.interceptors.response.use(
  (response: AxiosResponse) => {
    const { data } = response
    if (data && typeof data === 'object' && data.code !== undefined && data.code !== 0) {
      ElMessage.error(data.message || i18n.global.t('common.failed'))
      return Promise.reject(new Error(data.message))
    }
    if (data && typeof data === 'object' && data.data !== undefined) {
      const result = data.data
      if (data.total !== undefined) {
        ;(result as { _total?: number })._total = data.total
      }
      return result
    }
    return data
  },
  async (error: AxiosError) => {
    const original = error.config as (InternalAxiosRequestConfig & { _retry?: boolean }) | undefined
    if (error.response?.status === 401 && original && !original._retry) {
      original._retry = true
      if (!refreshing) {
        refreshing = refreshAccessToken().finally(() => {
          refreshing = null
        })
      }
      const newToken = await refreshing
      if (newToken) {
        if (original.headers) {
          original.headers.Authorization = `Bearer ${newToken}`
        }
        return request(original)
      }
      clearTokens()
      redirectToLogin()
      return Promise.reject(error)
    }
    if (error.response?.status === 401) {
      clearTokens()
      redirectToLogin()
    } else {
      const payload = error.response?.data as { message?: string; error?: string } | undefined
      const msg = payload?.message || payload?.error
      ElMessage.error(msg || error.message || i18n.global.t('common.networkError'))
    }
    return Promise.reject(error)
  }
)

export const api = {
  auth: {
    login: (username: string, password: string) =>
      platformAuth.post('/auth/login', { Username: username, Password: password }).then((r) => r.data),
    logout: (refreshToken?: string) =>
      platformAuth.post('/auth/logout', refreshToken ? { refreshToken } : {}).then((r) => r.data),
    refresh: (refreshToken: string) =>
      platformAuth.post('/auth/refresh', { refreshToken }).then((r) => r.data),
  },

  me: () => request.get('/me') as Promise<VisionUser>,

  mediaToken: () => request.get('/media-token') as Promise<MediaTokenInfo>,

  version: () => request.get('/version'),

  cameras: {
    list: () => request.get('/cameras'),
    get: (id: number) => request.get(`/cameras/${id}`),
    create: (data: Record<string, unknown>) => request.post('/cameras', data),
    update: (id: number, data: Record<string, unknown>) => request.put(`/cameras/${id}`, data),
    delete: (id: number) => request.delete(`/cameras/${id}`),
    status: (id: number) => request.get(`/cameras/${id}/status`),
    start: (id: number) => request.post(`/cameras/${id}/start`),
    stop: (id: number) => request.post(`/cameras/${id}/stop`),
    restart: (id: number) => request.post(`/cameras/${id}/restart`),
    snapshot: (id: number) => request.post(`/cameras/${id}/snapshot`),
    ptz: (id: number, command: string, speed = 1) =>
      request.post(`/cameras/${id}/ptz`, { command, speed }),
    snapshots: (id: number, params?: Record<string, unknown>) =>
      request.get(`/cameras/${id}/snapshots`, { params }),
  discover: (network?: string) =>
    request.get('/cameras/discover', { params: network ? { network } : {} }),
  discoverLAN: (timeout = 10) =>
    request.post('/cameras/discover/lan', { timeout }),
    probe: (ip: string, username?: string, password?: string) =>
      request.get('/cameras/probe', { params: { ip, username, password } }),
  },

  recordings: {
    list: (params?: Record<string, unknown>) => request.get('/recordings', { params }),
    get: (id: number) => request.get(`/recordings/${id}`),
    file: (id: number) => mediaUrl(`/api/vision/media/recordings/${id}/file`),
    download: (id: number) =>
      request.get(`/recordings/${id}/download`, { responseType: 'blob' }),
    delete: (id: number) => request.delete(`/recordings/${id}`),
    byCamera: (cameraId: number, params?: Record<string, unknown>) =>
      request.get(`/recordings/camera/${cameraId}`, { params }),
    segments: (cameraId: number, start: string, end: string) =>
      request.get(`/recordings/camera/${cameraId}/segments`, { params: { start, end } }),
  },

  stream: {
    hlsPlaylist: (cameraId: number, stream?: string) => {
      const base = `/api/vision/media/stream/camera/${cameraId}/hls`
      const path = stream ? `${base}?stream=${encodeURIComponent(stream)}` : base
      return mediaUrl(path)
    },
    hlsSegment: (cameraId: number, file: string) =>
      mediaUrl(`/api/vision/media/stream/camera/${cameraId}/hls/${file}`),
    mp4: (cameraId: number) => mediaUrl(`/api/vision/media/stream/camera/${cameraId}/mp4`),
    snapshot: (cameraId: number) => mediaUrl(`/api/vision/media/stream/camera/${cameraId}/snapshot`),
    recordingHLS: (recordingId: number) =>
      mediaUrl(`/api/vision/media/stream/camera/recordings/${recordingId}/hls`),
  },

  analytics: {
    alerts: (params?: Record<string, unknown>) => request.get('/analytics/alerts', { params }),
    getAlert: (id: number) => request.get(`/analytics/alerts/${id}`),
    acknowledge: (id: number) => request.put(`/analytics/alerts/${id}/ack`),
    resolve: (id: number) => request.put(`/analytics/alerts/${id}/resolve`),
    deleteAlert: (id: number) => request.delete(`/analytics/alerts/${id}`),
    clearAlerts: () => request.delete('/analytics/alerts'),
  },

  storage: {
    stats: () => request.get('/storage/stats'),
    cleanup: () => request.post('/storage/cleanup'),
  },

  webdav: {
    list: (cameraId?: number) =>
      request.get(mediaUrl('/media/webdav/list' + (cameraId ? ('?camera_id=' + cameraId) : ''))),
    fileUrl: (path: string) =>
      mediaUrl(`/api/vision/media/webdav/file?path=${encodeURIComponent(path)}`),
  },

  minio: {
    list: (cameraId?: number) =>
      request.get(mediaUrl('/media/minio/list' + (cameraId ? ('?camera_id=' + cameraId) : ''))),
    fileUrl: (path: string) =>
      mediaUrl(`/api/vision/media/minio/file?path=${encodeURIComponent(path)}`),
  },

  snapshots: {
    list: (params: Record<string, unknown>) => request.get('/snapshots', { params }),
    remove: (id: number) => request.delete(`/snapshots/${id}`),
    clear: () => request.delete('/snapshots'),
    fileUrl: (path: string, cameraId: number) => {
      const name = (path || '').split('/').pop() || ''
      return mediaUrl(`/api/vision/media/stream/camera/${cameraId}/snapshots/${encodeURIComponent(name)}`)
    },
  },

  alerts: {
    config: () => request.get('/alerts/config'),
    updateConfig: (data: Record<string, unknown>) => request.put('/alerts/config', data),
    test: (channel: string, data?: Record<string, unknown>) =>
      request.post('/alerts/test', { channel, ...data }),
  },

  system: {
    config: () => request.get('/system/config'),
    updateConfig: (data: Record<string, unknown>) => request.put('/system/config', data),
    info: () => request.get('/system/info'),
    envCheck: () => request.get('/system/env'),
  },

  settings: {
    getDefaults: () => request.get('/settings/defaults'),
    updateDefaults: (data: Record<string, unknown>) => request.put('/settings/defaults', data),
    getStorage: () => request.get('/settings/storage'),
    updateStorage: (data: Record<string, unknown>) => request.put('/settings/storage', data),
    getCamera: () => request.get('/settings/camera'),
    updateCamera: (data: { snapshot_enabled: boolean; snapshot_interval: number }) =>
      request.put('/settings/camera', data),
    testWebdav: (data: { url: string; username: string; password: string; base_path: string }) =>
      request.post('/settings/webdav/test', data),
    testMinio: (data: {
      endpoint: string
      access_key: string
      secret_key: string
      bucket: string
      use_ssl: boolean
      base_path: string
    }) => request.post('/settings/minio/test', data),
  },
}

export function buildMediaWsUrl(mediaToken?: string): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return mediaUrl(`${protocol}//${window.location.host}/api/vision/media/ws`, mediaToken)
}

export function buildCameraWsUrl(cameraId: number, mediaToken?: string): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return mediaUrl(`${protocol}//${window.location.host}/api/vision/media/ws/camera/${cameraId}`, mediaToken)
}

export default request
