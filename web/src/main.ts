import { createApp } from 'vue'
import { createPinia } from 'pinia'
import './styles/fonts.css'
import './styles/base.css'
import App from './App.vue'
import router from './router'
import { useWsStore } from './stores/ws'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')

useWsStore().connect()
