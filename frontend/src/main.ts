import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import Dashboard from './views/Dashboard.vue'
import Apps from './views/Apps.vue'
import AppCreate from './views/AppCreate.vue'
import AppDetail from './views/AppDetail.vue'
import Nodes from './views/Nodes.vue'
import Settings from './views/Settings.vue'
import './style.css'
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: Dashboard },
    { path: '/apps', component: Apps },
    { path: '/apps/new', component: AppCreate },
    { path: '/apps/:id', component: AppDetail },
    { path: '/nodes', component: Nodes },
    { path: '/settings', component: Settings },
  ],
})
createApp(App).use(createPinia()).use(router).mount('#app')
