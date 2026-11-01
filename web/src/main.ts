import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import './style.css'
import App from './App.vue'
import { router } from './router'

const app = createApp(App).use(router).use(ElementPlus, { locale: zhCn })
// Initial /me and redirects settle before App installs global auth listeners.
void router.isReady().then(() => app.mount('#app'))
