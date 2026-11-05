<template>
  <div class="login-container">
    <div class="login-box">
      <div class="login-header">
        <el-icon class="logo-icon"><VideoCamera /></el-icon>
        <h1>{{ t('login.title') }}</h1>
        <p>{{ t('login.subtitle') }}</p>
      </div>

      <el-form :model="form" :rules="rules" ref="formRef" class="login-form" label-width="0">
        <el-form-item prop="username">
          <el-input
            v-model="form.username"
            :placeholder="t('login.username')"
            prefix-icon="User"
            @keyup.enter="handleLogin"
          />
        </el-form-item>
        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            :placeholder="t('login.password')"
            prefix-icon="Lock"
            show-password
            @keyup.enter="handleLogin"
          />
        </el-form-item>
        <el-form-item>
          <el-button :loading="loading" type="primary" block @click="handleLogin">{{ t('login.submit') }}</el-button>
        </el-form-item>
      </el-form>

      <div class="login-footer">
        <el-dropdown trigger="click" @command="switchLang">
          <span class="lang-switch">
            <el-icon><Position /></el-icon>
            {{ currentLangLabel }}
          </span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item v-for="l in SUPPORTED_LANGS" :key="l.value" :command="l.value" :disabled="l.value === lang">
                {{ l.label }}
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { VideoCamera, Position } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores'
import { useI18n } from 'vue-i18n'
import { setLang, SUPPORTED_LANGS } from '@/i18n'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const { t, locale } = useI18n()
const formRef = ref()

const lang = computed(() => locale.value as string)
const currentLangLabel = computed(() => SUPPORTED_LANGS.find(l => l.value === lang.value)?.label || '中文')
const switchLang = (v: string) => setLang(v as 'zh' | 'en')

const loading = ref(false)
const form = reactive({
  username: '',
  password: '',
})

const rules = computed(() => ({
  username: [{ required: true, message: t('login.usernameRequired'), trigger: 'blur' }],
  password: [{ required: true, message: t('login.passwordRequired'), trigger: 'blur' }],
}))

const handleLogin = async () => {
  await formRef.value?.validate()
  loading.value = true
  try {
    await authStore.login(form.username, form.password)
    ElMessage.success(t('login.success'))
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/dashboard'
    router.replace(redirect)
  } catch (e) {
    console.error(e)
  } finally {
    loading.value = false
  }
}
</script>

<style scoped lang="scss">
.login-container {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #1e3a5f 0%, #0f1d2d 100%);
  overflow: hidden;

  &::before {
    content: '';
    position: absolute;
    top: 0; left: 0; right: 0; bottom: 0;
    background-image: url("data:image/svg+xml,%3Csvg width='60' height='60' viewBox='0 0 60 60' xmlns='http://www.w3.org/2000/svg'%3E%3Cg fill='none' fill-rule='evenodd'%3E%3Cg fill='%23ffffff' fill-opacity='0.03'%3E%3Cpath d='M36 34v-4h-2v4h-4v2h4v4h2v-4h4v-2h-4zm0-30V0h-2v4h-4v2h4v4h2V6h4V4h-4zM6 34v-4H4v4H0v2h4v4h2v-4h4v-2H6zM6 4V0H4v4H0v2h4v4h2V6h4V4H6z'/%3E%3C/g%3E%3C/g%3E%3C/svg%3E");
    opacity: 0.5;
  }
}

.login-box {
  position: relative;
  width: 100%;
  max-width: 400px;
  padding: 48px 40px;
  background: var(--el-bg-color, #fff);
  border-radius: 16px;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.3);

  .login-header {
    text-align: center;
    margin-bottom: 32px;

    .logo-icon {
      font-size: 48px;
      color: var(--el-color-primary, #409eff);
      margin-bottom: 16px;
    }

    h1 {
      margin: 0 0 8px;
      font-size: 24px;
      font-weight: 700;
      color: var(--el-text-color-primary, #303133);
    }

    p {
      margin: 0;
      color: var(--el-text-color-secondary, #909399);
      font-size: 14px;
    }
  }

  .login-form {
    margin-bottom: 24px;

    :deep(.el-input__inner) {
      height: 44px;
      font-size: 14px;
    }

    :deep(.el-form-item) {
      margin-bottom: 20px;
    }
  }

  .login-footer {
    text-align: center;
    color: var(--el-text-color-secondary, #909399);
    font-size: 12px;

    .lang-switch {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      cursor: pointer;
      outline: none;
    }
  }
}
</style>
