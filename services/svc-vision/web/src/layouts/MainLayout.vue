<template>
  <el-container class="app-container">
    <el-aside :width="isCollapse ? '64px' : '240px'" class="sidebar">
      <div class="logo-container">
        <el-icon class="logo-icon" v-if="!isCollapse"><VideoCamera /></el-icon>
        <span v-if="!isCollapse" class="logo-text">{{ sysName || t('layout.footer.app') }}</span>
      </div>
      <el-menu
        :default-active="activeMenu"
        :collapse="isCollapse"
        :unique-opened="true"
        :collapse-transition="false"
        router
        class="menu"
      >
        <template v-for="route in filteredRoutes" :key="route.fullPath">
          <el-menu-item v-if="!route.children || route.children.length === 0" :index="route.fullPath">
            <el-icon><component :is="route.meta.icon || 'Folder'" /></el-icon>
            <template v-if="!isCollapse">{{ t(route.meta.title as string) }}</template>
          </el-menu-item>
          <el-sub-menu v-else :index="route.fullPath">
            <template #title>
              <el-icon><component :is="route.meta.icon || 'Folder'" /></el-icon>
              <template v-if="!isCollapse">{{ t(route.meta.title as string) }}</template>
            </template>
            <template v-for="child in route.children" :key="child.fullPath">
              <el-menu-item v-if="!child.meta.hideInMenu" :index="child.fullPath">
                {{ t(child.meta.title as string) }}
              </el-menu-item>
            </template>
          </el-sub-menu>
        </template>
      </el-menu>
      <div class="collapse-btn" @click="toggleCollapse">
        <el-icon><Fold v-if="!isCollapse" /><Expand v-else /></el-icon>
      </div>
    </el-aside>

    <el-container>
      <el-header class="header">
        <div class="header-left">
          <el-icon class="hamburger" @click="toggleCollapse"><Menu /></el-icon>
          <h1 class="page-title">{{ pageTitle }}</h1>
        </div>
        <div class="header-right">
          <el-dropdown trigger="click" class="lang-dropdown" @command="switchLang">
            <span class="lang-switch">
              <el-icon><Position /></el-icon>
              <span>{{ currentLangLabel }}</span>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item v-for="l in SUPPORTED_LANGS" :key="l.value" :command="l.value" :disabled="l.value === lang">
                  {{ l.label }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
          <el-dropdown trigger="click" @command="onUserCommand">
            <span class="user-info">
              <el-icon><User /></el-icon>
              <span>{{ userName }}</span>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="account">
                  <el-icon><User /></el-icon>
                  {{ t('layout.accountCenter') }}
                </el-dropdown-item>
                <el-dropdown-item command="logout" divided>
                  <el-icon><SwitchButton /></el-icon>
                  {{ t('layout.logout') }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="main-content">
        <router-view />
      </el-main>

      <el-footer class="app-footer" height="36px">
        <span class="footer-item">{{ sysName || t('layout.footer.app') }}<b v-if="version" class="footer-version"> v{{ version }}</b></span>
      </el-footer>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import {
  VideoCamera, Folder, Menu, Fold, Expand, User, ArrowDown, SwitchButton, Position
} from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { setLang, SUPPORTED_LANGS } from '@/i18n'
import { api } from '@/api'
import { useAuthStore } from '@/stores'

const router = useRouter()
const route = useRoute()
const { t, locale } = useI18n()
const authStore = useAuthStore()

const lang = computed(() => locale.value as string)
const currentLangLabel = computed(() => SUPPORTED_LANGS.find(l => l.value === lang.value)?.label || '中文')
const switchLang = (v: string) => setLang(v as 'zh' | 'en')

const isCollapse = ref(false)
const userName = computed(() => authStore.user?.username || 'user')

const version = ref('')
const sysName = ref('')
onMounted(async () => {
  try {
    const d: any = await api.version()
    version.value = d?.version || ''
    sysName.value = d?.name || ''
  } catch {
    // version endpoint optional
  }
})

const toggleCollapse = () => {
  isCollapse.value = !isCollapse.value
}

const filteredRoutes = computed(() => {
  const rootRoute = router.getRoutes().find(r => r.path === '/')
  if (!rootRoute) return []

  const addFullPath = (routes: any[], parentPath = '') => {
    return routes
      .filter((r) => {
        if (r.meta?.hideInMenu) return false
        if (r.meta?.adminOnly && !authStore.isAdmin) return false
        return true
      })
      .map(routeItem => {
        const fullPath = parentPath
          ? `${parentPath}/${routeItem.path}`.replace(/\/+/g, '/')
          : `/${routeItem.path}`.replace(/\/+/g, '/')
        const routeWithPath = { ...routeItem, fullPath }
        if (routeItem.children && routeItem.children.length > 0) {
          routeWithPath.children = addFullPath(routeItem.children, fullPath)
        }
        return routeWithPath
      })
  }

  return addFullPath(rootRoute.children || [])
})

const activeMenu = computed(() => {
  const findFullPath = (routes: any[], currentPath: string): string => {
    for (const r of routes) {
      if (r.fullPath === currentPath) return r.fullPath
      if (r.children) {
        const found = findFullPath(r.children, currentPath)
        if (found) return found
      }
    }
    return currentPath
  }
  return findFullPath(filteredRoutes.value, route.path)
})

const pageTitle = computed(() => {
  const matched = route.matched[route.matched.length - 1]
  const title = matched?.meta?.title as string
  return title ? t(title) : (sysName.value || t('layout.footer.app'))
})

const onUserCommand = async (cmd: string) => {
  if (cmd === 'account') {
    window.open('/account/', '_blank')
    return
  }
  if (cmd === 'logout') {
    await authStore.logout()
    router.push('/login')
  }
}
</script>

<style scoped lang="scss">
.app-container {
  height: 100vh;
  display: flex;
}

.sidebar {
  height: 100%;
  background: #1f2d3d;
  border-right: 1px solid #2d3a4a;
  display: flex;
  flex-direction: column;
  transition: width 0.3s ease;

  .logo-container {
    height: 60px;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0 20px;
    border-bottom: 1px solid #2d3a4a;
    overflow: hidden;

    .logo-icon {
      font-size: 24px;
      color: var(--el-color-primary, #409eff);
    }

    .logo-text {
      margin-left: 12px;
      font-size: 18px;
      font-weight: 600;
      color: #fff;
      white-space: nowrap;
    }
  }

  .menu {
    flex: 1;
    overflow-y: auto;
    border: none;
    background: transparent;
    padding: 10px 0;

    ::v-deep {
      .el-menu-item, .el-sub-menu__title {
        height: 44px;
        line-height: 44px;
        color: #b4c2d4;
        padding: 0 20px;
        transition: all 0.3s;

        &:hover {
          background: #2d3a4a;
          color: #fff;
        }

        .el-icon {
          margin-right: 12px;
          font-size: 16px;
        }
      }

      .el-menu-item.is-active, .el-sub-menu.is-active > .el-sub-menu__title {
        background: #1e3a5f;
        color: var(--el-color-primary, #409eff);
        border-right: 3px solid var(--el-color-primary, #409eff);
      }

      // 展开的二级菜单：比侧栏深一档形成内嵌层级，缩进 + 矮行高 + 弱化文字
      .el-sub-menu .el-menu {
        background: #18212c;
      }

      .el-sub-menu .el-menu-item {
        height: 40px;
        line-height: 40px;
        padding-left: 48px !important;
        font-size: 13px;
        color: #8fa1b7;
        background: transparent;

        &:hover {
          background: #223040;
          color: #fff;
        }

        &.is-active {
          background: #1e3a5f;
          color: var(--el-color-primary, #409eff);
        }
      }

      .el-sub-menu__icon-arrow {
        transition: transform 0.3s;
      }

      .el-sub-menu.is-opened .el-sub-menu__icon-arrow {
        transform: rotate(90deg);
      }
    }
  }

  .collapse-btn {
    height: 40px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-top: 1px solid #2d3a4a;
    cursor: pointer;
    color: #6a7a8d;
    transition: color 0.3s;

    &:hover {
      color: #fff;
    }
  }
}

.header {
  height: 60px;
  background: var(--el-bg-color, #fff);
  border-bottom: 1px solid var(--el-border-color, #e6e9ed);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 24px;
  box-shadow: 0 1px 4px rgba(0,0,0,0.05);

  .header-left {
    display: flex;
    align-items: center;

    .hamburger {
      font-size: 20px;
      color: var(--el-text-color-secondary, #606266);
      cursor: pointer;
      margin-right: 16px;
    }

    .page-title {
      margin: 0;
      font-size: 18px;
      font-weight: 600;
      color: var(--el-text-color-primary, #303133);
    }
  }

  .header-right {
    display: flex;
    align-items: center;
    gap: 8px;

    .lang-switch {
      display: flex;
      align-items: center;
      gap: 6px;
      padding: 6px 12px;
      border-radius: 6px;
      cursor: pointer;
      color: var(--el-text-color-secondary, #606266);
      font-size: 14px;
      outline: none;

      &:hover {
        background: #f5f7fa;
      }
    }

    .user-info {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 6px 12px;
      border-radius: 6px;
      cursor: pointer;
      color: var(--el-text-color-secondary, #606266);
      font-size: 14px;

      &:hover {
        background: #f5f7fa;
        color: var(--el-text-color-primary, #303133);
      }
    }
  }
}

.main-content {
  padding: 24px;
  background: var(--pf-bg, #f0f2f5);
  min-height: calc(100vh - 60px - 36px);
  overflow-y: auto;
}

.app-footer {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 24px;
  background: var(--el-bg-color, #fff);
  border-top: 1px solid var(--el-border-color, #e6e9ed);
  font-size: 12px;
  color: var(--el-text-color-secondary, #909399);
  flex-shrink: 0;

  .footer-item {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .footer-version {
    color: var(--el-color-primary, #409eff);
    margin-left: 2px;
  }
}
</style>

<style lang="scss">
/* 折叠态悬浮弹出的子菜单 teleport 到 body，scoped 样式覆盖不到，需全局暗色化 */
.el-menu--vertical .el-menu--popup {
  --el-menu-bg-color: #1f2d3d;
  background: #1f2d3d;
  border: 1px solid #2d3a4a;

  .el-menu-item {
    height: 40px;
    line-height: 40px;
    color: #b4c2d4;
    background: transparent;

    &:hover {
      background: #2d3a4a;
      color: #fff;
    }

    &.is-active {
      background: #1e3a5f;
      color: var(--el-color-primary, #409eff);
    }
  }
}
</style>
