<template>
  <div class="settings-page">
    <el-card :shadow="never" class="mb-16">
      <template #header>
        <h3>{{ t('settingsAlerts.webhookTitle') }}</h3>
      </template>
      <el-form :model="webhookForm" label-width="140">
        <el-form-item :label="t('settingsAlerts.webhookEnabled')">
          <el-switch v-model="webhookForm.enabled" />
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.webhookType')">
          <el-select v-model="webhookForm.type" style="width: 240px">
            <el-option :label="t('settingsAlerts.webhookTypeGeneric')" value="generic" />
            <el-option :label="t('settingsAlerts.webhookTypeGotify')" value="gotify" />
          </el-select>
          <span class="form-hint">{{ t('settingsAlerts.webhookTypeHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.webhookUrl')">
          <el-input
            v-model="webhookForm.url"
            :placeholder="webhookForm.type === 'gotify' ? t('settingsAlerts.webhookUrlGotifyPlaceholder') : t('settingsAlerts.webhookUrlPlaceholder')"
            style="width: 500px"
          />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="testWebhook"><el-icon><Connection /></el-icon> {{ t('settingsAlerts.testConnection') }}</el-button>
          <el-button @click="saveWebhook"><el-icon><Check /></el-icon> {{ t('settingsAlerts.save') }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card :shadow="never" class="mb-16">
      <template #header>
        <h3>{{ t('settingsAlerts.emailTitle') }}</h3>
      </template>
      <el-form :model="emailForm" label-width="140">
        <el-form-item :label="t('settingsAlerts.emailEnabled')">
          <el-switch v-model="emailForm.enabled" />
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.smtpHost')">
          <el-input v-model="emailForm.smtp_host" :placeholder="t('settingsAlerts.smtpHostPlaceholder')" style="width: 300px" />
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.smtpPort')">
          <el-input-number v-model="emailForm.smtp_port" :min="1" :max="65535" :controls="false" style="width: 120px" />
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.smtpUsername')">
          <el-input v-model="emailForm.username" :placeholder="t('settingsAlerts.smtpUsernamePlaceholder')" style="width: 300px" />
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.smtpPassword')">
          <el-input v-model="emailForm.password" type="password" show-password :placeholder="t('settingsAlerts.smtpPasswordPlaceholder')" style="width: 300px" />
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.emailFrom')">
          <el-input v-model="emailForm.from" :placeholder="t('settingsAlerts.emailFromPlaceholder')" style="width: 400px" />
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.emailTo')">
          <el-input v-model="emailForm.to" :placeholder="t('settingsAlerts.emailToPlaceholder')" style="width: 100%" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="testEmail"><el-icon><Connection /></el-icon> {{ t('settingsAlerts.testEmail') }}</el-button>
          <el-button @click="saveEmail"><el-icon><Check /></el-icon> {{ t('settingsAlerts.save') }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card :shadow="never" class="mb-16">
      <template #header>
        <h3>{{ t('settingsAlerts.smsTitle') }}</h3>
      </template>
      <el-form :model="smsForm" label-width="140">
        <el-form-item :label="t('settingsAlerts.smsEnabled')">
          <el-switch v-model="smsForm.enabled" />
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.smsProvider')">
          <el-select v-model="smsForm.provider" :placeholder="t('settingsAlerts.placeholderProvider')" style="width: 200px">
            <el-option :label="t('settingsAlerts.smsProviderAliyun')" value="aliyun" />
            <el-option :label="t('settingsAlerts.smsProviderTencent')" value="tencent" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.smsAccessKey')">
          <el-input v-model="smsForm.access_key" :placeholder="t('settingsAlerts.smsAccessKeyPlaceholder')" style="width: 300px" />
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.smsSecretKey')">
          <el-input v-model="smsForm.secret_key" type="password" show-password :placeholder="t('settingsAlerts.smsSecretKeyPlaceholder')" style="width: 300px" />
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.smsSignName')">
          <el-input v-model="smsForm.sign_name" :placeholder="t('settingsAlerts.smsSignNamePlaceholder')" style="width: 300px" />
        </el-form-item>
        <el-form-item :label="t('settingsAlerts.smsTemplateCode')">
          <el-input v-model="smsForm.template_code" :placeholder="t('settingsAlerts.smsTemplateCodePlaceholder')" style="width: 300px" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="testSms"><el-icon><Connection /></el-icon> {{ t('settingsAlerts.testSms') }}</el-button>
          <el-button @click="saveSms"><el-icon><Check /></el-icon> {{ t('settingsAlerts.save') }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card :shadow="never">
      <template #header>
        <h3>{{ t('settingsAlerts.testAlertTitle') }}</h3>
      </template>
      <div class="test-actions">
        <el-button type="primary" @click="sendTestAlert('webhook')" :loading="testLoading.webhook">
          <el-icon><Bell /></el-icon> {{ t('settingsAlerts.testWebhook') }}
        </el-button>
        <el-button type="success" @click="sendTestAlert('email')" :loading="testLoading.email">
          <el-icon><Message /></el-icon> {{ t('settingsAlerts.testEmailBtn') }}
        </el-button>
        <el-button type="warning" @click="sendTestAlert('sms')" :loading="testLoading.sms">
          <el-icon><Phone /></el-icon> {{ t('settingsAlerts.testSmsBtn') }}
        </el-button>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Connection, Check, Bell, Message, Phone } from '@element-plus/icons-vue'
import { api } from '@/api'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const testLoading = reactive({ webhook: false, email: false, sms: false })

const webhookForm = reactive({ enabled: false, url: '', type: 'generic' })
const emailForm = reactive({ enabled: false, smtp_host: '', smtp_port: 587, username: '', password: '', from: '', to: <string[]>[] })
const smsForm = reactive({ enabled: false, provider: 'aliyun', access_key: '', secret_key: '', sign_name: '', template_code: '' })

const loadConfig = async () => {
  try {
    const res = await api.alerts.config()
    Object.assign(webhookForm, res.channels?.webhook || {})
    if (!webhookForm.type) webhookForm.type = 'generic'
    Object.assign(emailForm, res.channels?.email || {})
    Object.assign(smsForm, res.channels?.sms || {})
  } catch (e) { console.error(e) }
}

const saveWebhook = async () => { try { await api.alerts.updateConfig({ channels: { webhook: webhookForm } }); ElMessage.success(t('settingsAlerts.saveSuccess')) } catch(e) { ElMessage.error(t('settingsAlerts.saveFailed')) } }
const saveEmail = async () => { try { await api.alerts.updateConfig({ channels: { email: emailForm } }); ElMessage.success(t('settingsAlerts.saveSuccess')) } catch(e) { ElMessage.error(t('settingsAlerts.saveFailed')) } }
const saveSms = async () => { try { await api.alerts.updateConfig({ channels: { sms: smsForm } }); ElMessage.success(t('settingsAlerts.saveSuccess')) } catch(e) { ElMessage.error(t('settingsAlerts.saveFailed')) } }

const testWebhook = async () => { testLoading.webhook = true; try { await api.alerts.test('webhook', { webhook: webhookForm }); ElMessage.success(t('settingsAlerts.testSent')) } catch(e) { ElMessage.error(t('settingsAlerts.testFailed')) } finally { testLoading.webhook = false } }
const testEmail = async () => { testLoading.email = true; try { await api.alerts.test('email', { email: emailForm }); ElMessage.success(t('settingsAlerts.testEmailSent')) } catch(e) { ElMessage.error(t('settingsAlerts.testFailed')) } finally { testLoading.email = false } }
const testSms = async () => { testLoading.sms = true; try { await api.alerts.test('sms', { sms: smsForm }); ElMessage.success(t('settingsAlerts.testSmsSent')) } catch(e) { ElMessage.error(t('settingsAlerts.testFailed')) } finally { testLoading.sms = false } }

const sendTestAlert = async (channel: string) => { const formMap: Record<string, any> = { webhook: webhookForm, email: emailForm, sms: smsForm }; testLoading[channel] = true; try { await api.alerts.test(channel, { [channel]: formMap[channel] }); ElMessage.success(t('settingsAlerts.testAlertSent', { channel })) } catch(e) { ElMessage.error(t('settingsAlerts.testFailed')) } finally { testLoading[channel] = false } }

onMounted(() => loadConfig())
</script>

<style scoped lang="scss">
.settings-page { .test-actions{display:flex;gap:12px;flex-wrap:wrap;} }
</style>