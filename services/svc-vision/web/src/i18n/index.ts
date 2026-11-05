import { createI18n } from 'vue-i18n'
import en from 'element-plus/es/locale/lang/en'
import zhCn from 'element-plus/es/locale/lang/zh-cn'

import commonZh from './messages/common.zh'
import commonEn from './messages/common.en'
import layoutZh from './messages/layout.zh'
import layoutEn from './messages/layout.en'
import loginZh from './messages/login.zh'
import loginEn from './messages/login.en'
import dashboardZh from './messages/dashboard.zh'
import dashboardEn from './messages/dashboard.en'
import camerasAddZh from './messages/cameras-add.zh'
import camerasAddEn from './messages/cameras-add.en'
import camerasListZh from './messages/cameras-list.zh'
import camerasListEn from './messages/cameras-list.en'
import camerasDetailZh from './messages/cameras-detail.zh'
import camerasDetailEn from './messages/cameras-detail.en'
import recordingsZh from './messages/recordings.zh'
import recordingsEn from './messages/recordings.en'
import storageZh from './messages/storage.zh'
import storageEn from './messages/storage.en'
import analyticsZh from './messages/analytics.zh'
import analyticsEn from './messages/analytics.en'
import settingsSystemZh from './messages/settings-system.zh'
import settingsSystemEn from './messages/settings-system.en'
import settingsDefaultsZh from './messages/settings-defaults.zh'
import settingsDefaultsEn from './messages/settings-defaults.en'
import settingsStorageZh from './messages/settings-storage.zh'
import settingsStorageEn from './messages/settings-storage.en'
import settingsAlertsZh from './messages/settings-alerts.zh'
import settingsAlertsEn from './messages/settings-alerts.en'
import envCheckZh from './messages/env-check.zh'
import envCheckEn from './messages/env-check.en'
import videoPlayerZh from './messages/video-player.zh'
import videoPlayerEn from './messages/video-player.en'
import utilsZh from './messages/utils.zh'
import utilsEn from './messages/utils.en'

export type Lang = 'zh' | 'en'

export const SUPPORTED_LANGS: { value: Lang; label: string }[] = [
  { value: 'zh', label: '中文' },
  { value: 'en', label: 'English' },
]

function getInitialLang(): Lang {
  try {
    const saved = localStorage.getItem('lang')
    if (saved === 'zh' || saved === 'en') return saved
  } catch {
    // ignore
  }
  return 'zh'
}

const i18n = createI18n({
  legacy: false,
  locale: getInitialLang(),
  fallbackLocale: 'zh',
  messages: {
    zh: {
      ...commonZh, ...layoutZh, ...loginZh, ...dashboardZh,
      ...camerasAddZh, ...camerasListZh, ...camerasDetailZh,
      ...recordingsZh, ...storageZh, ...analyticsZh,
      ...settingsSystemZh, ...settingsDefaultsZh, ...settingsStorageZh, ...settingsAlertsZh,
      ...envCheckZh, ...videoPlayerZh, ...utilsZh,
    },
    en: {
      ...commonEn, ...layoutEn, ...loginEn, ...dashboardEn,
      ...camerasAddEn, ...camerasListEn, ...camerasDetailEn,
      ...recordingsEn, ...storageEn, ...analyticsEn,
      ...settingsSystemEn, ...settingsDefaultsEn, ...settingsStorageEn, ...settingsAlertsEn,
      ...envCheckEn, ...videoPlayerEn, ...utilsEn,
    },
  },
})

try {
  document.documentElement.lang = getInitialLang() === 'en' ? 'en' : 'zh-CN'
} catch {
  // ignore
}

export const elementLocales: Record<Lang, any> = { zh: zhCn, en }

export function setLang(lang: Lang) {
  try {
    localStorage.setItem('lang', lang)
  } catch {
    // ignore
  }
  i18n.global.locale.value = lang
  try {
    document.documentElement.lang = lang === 'en' ? 'en' : 'zh-CN'
  } catch {
    // ignore
  }
}

export function useLocale() {
  return i18n.global
}

export default i18n
