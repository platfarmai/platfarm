import { createRouter, createWebHistory, RouteRecordRaw } from 'vue-router'
import i18n from '@/i18n'
import { useAuthStore } from '@/stores'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('@/views/Login.vue'),
    meta: { title: 'layout.menu.login', hideInMenu: true, public: true },
  },
  {
    path: '/',
    component: () => import('@/layouts/MainLayout.vue'),
    redirect: '/dashboard',
    children: [
      {
        path: 'dashboard',
        name: 'Dashboard',
        component: () => import('@/views/Dashboard.vue'),
        meta: { title: 'layout.menu.dashboard', icon: 'Monitor' },
      },
      {
        path: 'cameras',
        name: 'Cameras',
        component: () => import('@/views/cameras/Index.vue'),
        meta: { title: 'layout.menu.cameras', icon: 'VideoCamera' },
        children: [
          {
            path: '',
            name: 'CameraList',
            component: () => import('@/views/cameras/List.vue'),
            meta: { title: 'layout.menu.camerasList' },
          },
          {
            path: 'add',
            name: 'CameraAdd',
            component: () => import('@/views/cameras/Add.vue'),
            meta: { title: 'layout.menu.camerasAdd', hideInMenu: true, adminOnly: true },
          },
          {
            path: 'edit/:id',
            name: 'CameraEdit',
            component: () => import('@/views/cameras/Add.vue'),
            meta: { title: 'layout.menu.camerasEdit', hideInMenu: true, adminOnly: true },
          },
          {
            path: ':id',
            name: 'CameraDetail',
            component: () => import('@/views/cameras/Detail.vue'),
            meta: { title: 'layout.menu.camerasDetail', hideInMenu: true },
          },
        ],
      },
      {
        path: 'recordings',
        name: 'Recordings',
        component: () => import('@/views/recordings/Index.vue'),
        meta: { title: 'layout.menu.recordings', icon: 'Film' },
        children: [
          {
            path: '',
            name: 'RecordingList',
            component: () => import('@/views/recordings/List.vue'),
            meta: { title: 'layout.menu.recordingsList' },
          },
          {
            path: 'snapshots',
            name: 'SnapshotList',
            component: () => import('@/views/recordings/Snapshots.vue'),
            meta: { title: 'layout.menu.snapshots' },
          },
          {
            path: 'playback/:cameraId',
            name: 'Playback',
            component: () => import('@/views/recordings/Playback.vue'),
            meta: { title: 'layout.menu.playback', hideInMenu: true },
          },
        ],
      },
      {
        path: 'analytics',
        name: 'Analytics',
        component: () => import('@/views/analytics/Index.vue'),
        meta: { title: 'layout.menu.analytics', icon: 'Cpu' },
        children: [
          {
            path: '',
            name: 'AlertList',
            component: () => import('@/views/analytics/Alerts.vue'),
            meta: { title: 'layout.menu.alerts' },
          },
        ],
      },
      {
        path: 'storage',
        name: 'Storage',
        component: () => import('@/views/storage/Index.vue'),
        meta: { title: 'layout.menu.storage', icon: 'HardDrive' },
      },
      {
        path: 'settings',
        name: 'Settings',
        component: () => import('@/views/settings/Index.vue'),
        meta: { title: 'layout.menu.settings', icon: 'Setting', adminOnly: true },
        children: [
          {
            path: '',
            name: 'SystemConfig',
            component: () => import('@/views/settings/System.vue'),
            meta: { title: 'layout.menu.settingsSystem', adminOnly: true },
          },
          {
            path: 'camera-defaults',
            name: 'CameraDefaults',
            component: () => import('@/views/settings/CameraDefaults.vue'),
            meta: { title: 'layout.menu.settingsCameraDefaults', adminOnly: true },
          },
          {
            path: 'storage',
            name: 'StorageSettings',
            component: () => import('@/views/settings/Storage.vue'),
            meta: { title: 'layout.menu.settingsStorage', adminOnly: true },
          },
          {
            path: 'alerts',
            name: 'AlertConfig',
            component: () => import('@/views/settings/Alerts.vue'),
            meta: { title: 'layout.menu.settingsAlerts', adminOnly: true },
          },
        ],
      },
    ],
  },
  {
    path: '/:pathMatch(.*)*',
    redirect: '/dashboard',
  },
]

const router = createRouter({
  history: createWebHistory('/api/vision/console/'),
  routes,
})

let bootPromise: Promise<boolean> | null = null

router.beforeEach(async (to, _from, next) => {
  document.title = `${to.meta.title ? i18n.global.t(to.meta.title as string) : i18n.global.t('layout.system')} - ${i18n.global.t('layout.appName')}`

  const auth = useAuthStore()
  if (!auth.bootstrapped) {
    if (!bootPromise) bootPromise = auth.bootstrap()
    await bootPromise
  }

  if (to.meta.public) {
    if (auth.isLoggedIn && to.name === 'Login') {
      next('/dashboard')
      return
    }
    next()
    return
  }

  if (!auth.isLoggedIn) {
    next({ path: '/login', query: { redirect: to.fullPath } })
    return
  }

  if (to.meta.adminOnly && !auth.isAdmin) {
    next('/dashboard')
    return
  }

  next()
})

export default router
