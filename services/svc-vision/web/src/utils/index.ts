
import i18n from '@/i18n'


export function formatBytes(bytes: number, decimals = 2): string {
  if (!bytes || bytes === 0) return '0 B'
  const k = 1024
  const dm = decimals < 0 ? 0 : decimals
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i]
}


export function formatDuration(seconds: number): string {
  if (!seconds || seconds < 0) return i18n.global.t('utils.time.zero')
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = seconds % 60
  const parts: string[] = []
  if (d) parts.push(i18n.global.t('utils.time.days', { n: d }))
  if (h) parts.push(i18n.global.t('utils.time.hours', { n: h }))
  if (m) parts.push(i18n.global.t('utils.time.minutes', { n: m }))
  if (s || parts.length === 0) parts.push(i18n.global.t('utils.time.seconds', { n: s }))
  return parts.join('')
}


export function formatDateTime(date: string | number | Date, format = 'YYYY-MM-DD HH:mm:ss'): string {
  const d = new Date(date)
  if (isNaN(d.getTime())) return '-'
  const pad = (n: number) => String(n).padStart(2, '0')
  return format
    .replace('YYYY', String(d.getFullYear()))
    .replace('MM', pad(d.getMonth() + 1))
    .replace('DD', pad(d.getDate()))
    .replace('HH', pad(d.getHours()))
    .replace('mm', pad(d.getMinutes()))
    .replace('ss', pad(d.getSeconds()))
}


export function formatRelativeTime(date: string | number | Date): string {
  const now = Date.now()
  const then = new Date(date).getTime()
  const diff = Math.floor((now - then) / 1000)

  if (diff < 60) return i18n.global.t('utils.time.justNow')
  if (diff < 3600) return i18n.global.t('utils.time.minutesAgo', { n: Math.floor(diff / 60) })
  if (diff < 86400) return i18n.global.t('utils.time.hoursAgo', { n: Math.floor(diff / 3600) })
  if (diff < 2592000) return i18n.global.t('utils.time.daysAgo', { n: Math.floor(diff / 86400) })
  return formatDateTime(date, 'YYYY-MM-DD')
}


export function debounce<T extends (...args: any[]) => any>(
  fn: T,
  delay: number
): (...args: Parameters<T>) => void {
  let timer: ReturnType<typeof setTimeout> | null = null
  return (...args: Parameters<T>) => {
    if (timer) clearTimeout(timer)
    timer = setTimeout(() => fn(...args), delay)
  }
}


export function throttle<T extends (...args: any[]) => any>(
  fn: T,
  limit: number
): (...args: Parameters<T>) => void {
  let inThrottle = false
  return (...args: Parameters<T>) => {
    if (!inThrottle) {
      fn(...args)
      inThrottle = true
      setTimeout(() => (inThrottle = false), limit)
    }
  }
}


export function deepClone<T>(obj: T): T {
  if (obj === null || typeof obj !== 'object') return obj
  if (obj instanceof Date) return new Date(obj.getTime()) as any
  if (obj instanceof Array) return obj.map(item => deepClone(item)) as any
  if (obj instanceof Object) {
    const cloned = {} as T
    for (const key in obj) {
      if (Object.prototype.hasOwnProperty.call(obj, key)) {
        cloned[key] = deepClone(obj[key])
      }
    }
    return cloned
  }
  return obj
}


export function generateUUID(): string {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => {
    const r = (Math.random() * 16) | 0
    const v = c === 'x' ? r : (r & 0x3) | 0x8
    return v.toString(16)
  })
}


export function hexToRgba(hex: string, alpha = 1): string {
  const clean = hex.replace('#', '')
  const r = parseInt(clean.slice(0, 2), 16)
  const g = parseInt(clean.slice(2, 4), 16)
  const b = parseInt(clean.slice(4, 6), 16)
  return `rgba(${r}, ${g}, ${b}, ${alpha})`
}


export function downloadFile(blob: Blob, filename: string): void {
  const url = window.URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  window.URL.revokeObjectURL(url)
}


export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {

    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    const success = document.execCommand('copy')
    document.body.removeChild(textarea)
    return success
  }
}


export function getFileExtension(filename: string): string {
  return filename.slice((filename.lastIndexOf('.') - 1 >>> 0) + 2).toLowerCase()
}


export function isVideoFile(filename: string): boolean {
  const videoExts = ['mp4', 'mkv', 'avi', 'mov', 'flv', 'ts', 'm3u8', 'webm']
  return videoExts.includes(getFileExtension(filename))
}


export function isImageFile(filename: string): boolean {
  const imageExts = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'svg']
  return imageExts.includes(getFileExtension(filename))
}


export function parseQueryString(query: string): Record<string, string> {
  const params: Record<string, string> = {}
  new URLSearchParams(query).forEach((value, key) => {
    params[key] = value
  })
  return params
}


export function buildQueryString(params: Record<string, any>): string {
  const searchParams = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') {
      searchParams.append(key, String(value))
    }
  })
  return searchParams.toString()
}


export function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms))
}


export async function retry<T>(
  fn: () => Promise<T>,
  retries = 3,
  delay = 1000,
  backoff = 2
): Promise<T> {
  try {
    return await fn()
  } catch (error) {
    if (retries <= 0) throw error
    await sleep(delay)
    return retry(fn, retries - 1, delay * backoff, backoff)
  }
}


export function getStatusType(status: string): 'success' | 'warning' | 'danger' | 'info' | 'primary' {
  const map: Record<string, any> = {
    online: 'success',
    offline: 'warning',
    error: 'danger',
    recording: 'danger',
    connecting: 'info',
    new: 'danger',
    acknowledged: 'warning',
    resolved: 'success',
    low: 'success',
    medium: 'warning',
    high: 'danger',
    critical: 'danger',
    motion: 'primary',
    intrusion: 'warning',
    line_cross: 'warning',
    object_detect: 'success',
    storage_full: 'warning',
  }
  return map[status] || 'info'
}


export const deviceIcons: Record<string, string> = {
  rtsp: 'VideoCamera',
  onvif: 'Connection',
  gb28181: 'Setting',
}
