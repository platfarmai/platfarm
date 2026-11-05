import { createApp } from 'vue'
import { createPinia } from 'pinia'
import router from './router'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import * as ElementPlusIcons from '@element-plus/icons-vue'
import i18n from './i18n'

import App from './App.vue'
import './styles/index.scss'

const app = createApp(App)


for (const [key, component] of Object.entries(ElementPlusIcons)) {
  app.component(key, component)
}

app.use(createPinia())
app.use(router)
app.use(ElementPlus)
app.use(i18n)

app.mount('#app')
