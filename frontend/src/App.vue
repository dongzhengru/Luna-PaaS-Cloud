<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { Boxes, Cloud, LayoutDashboard, LogOut, Menu, Server, Settings, X } from 'lucide-vue-next'
import { api, post } from './api'
import { Button } from '@/components/ui/button'
import Toaster from '@/components/Toaster.vue'

const user = ref<any>(null)
const loading = ref(true)
const mobileOpen = ref(false)
const route = useRoute()

const navigation = [
  { to: '/', label: '概览', icon: LayoutDashboard },
  { to: '/apps', label: '应用', icon: Boxes },
  { to: '/nodes', label: '部署节点', icon: Server },
  { to: '/settings', label: '系统设置', icon: Settings },
]

onMounted(async () => {
  try {
    user.value = await api('/auth/me')
  } catch {
    /* handled by login state */
  } finally {
    loading.value = false
  }
})

async function logout() {
  await post('/auth/logout')
  location.reload()
}
</script>

<template>
  <Toaster />
  <div v-if="loading" class="grid min-h-screen place-items-center bg-background">
    <div
      class="size-6 animate-spin rounded-full border-2 border-muted border-t-foreground"
      aria-label="正在加载"
    />
  </div>

  <div v-else-if="!user" class="grid min-h-screen place-items-center bg-muted/40 px-4">
    <div class="w-full max-w-sm rounded-xl border bg-card p-8 text-center shadow-lg shadow-black/5">
      <div
        class="mx-auto mb-6 grid size-12 place-items-center rounded-xl bg-primary text-primary-foreground shadow-sm"
      >
        <Cloud class="size-6" />
      </div>
      <h1 class="text-2xl font-semibold tracking-tight">登录 Luna PaaS</h1>
      <p class="mt-2 text-sm leading-6 text-muted-foreground">
        从 GitHub 构建镜像，在你的节点上可靠运行。
      </p>
      <Button as-child class="mt-7 w-full">
        <a href="/api/auth/login">使用 One Connect 登录</a>
      </Button>
    </div>
  </div>

  <div v-else class="min-h-screen bg-background">
    <header
      class="sticky top-0 z-30 flex h-14 items-center border-b bg-background/95 px-4 backdrop-blur lg:hidden"
    >
      <Button variant="ghost" size="icon" aria-label="打开导航" @click="mobileOpen = true"
        ><Menu
      /></Button>
      <div class="ml-3 flex items-center gap-2 font-semibold">
        <Cloud class="size-5" /> Luna PaaS
      </div>
    </header>

    <div
      v-if="mobileOpen"
      class="fixed inset-0 z-40 bg-black/40 lg:hidden"
      @click="mobileOpen = false"
    />
    <aside
      :class="[
        'fixed inset-y-0 left-0 z-50 flex w-64 flex-col border-r bg-sidebar transition-transform lg:translate-x-0',
        mobileOpen ? 'translate-x-0' : '-translate-x-full',
      ]"
    >
      <div class="flex h-16 items-center border-b px-5">
        <div class="grid size-8 place-items-center rounded-lg bg-primary text-primary-foreground">
          <Cloud class="size-4" />
        </div>
        <div class="ml-3 leading-tight">
          <p class="text-sm font-semibold">Luna PaaS</p>
          <p class="text-xs text-muted-foreground">Cloud Console</p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          class="ml-auto lg:hidden"
          aria-label="关闭导航"
          @click="mobileOpen = false"
          ><X
        /></Button>
      </div>
      <nav class="flex-1 space-y-1 p-3">
        <RouterLink
          v-for="item in navigation"
          :key="item.to"
          :to="item.to"
          :class="[
            'flex h-9 items-center gap-3 rounded-md px-3 text-sm font-medium text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
            (item.to === '/' ? route.path === '/' : route.path.startsWith(item.to)) &&
              'bg-sidebar-accent text-sidebar-accent-foreground',
          ]"
          @click="mobileOpen = false"
        >
          <component :is="item.icon" class="size-4" />{{ item.label }}
        </RouterLink>
      </nav>
      <div class="border-t p-3">
        <div class="flex items-center gap-3 rounded-lg px-3 py-2">
          <div
            class="grid size-8 shrink-0 place-items-center rounded-full bg-muted text-xs font-semibold"
          >
            {{ user.phone?.slice(-2) || 'U' }}
          </div>
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm font-medium">{{ user.phone || '当前用户' }}</p>
            <p class="text-xs text-muted-foreground">已登录</p>
          </div>
          <Button variant="ghost" size="icon" aria-label="退出登录" @click="logout"
            ><LogOut
          /></Button>
        </div>
      </div>
    </aside>

    <main class="min-h-screen px-4 py-6 sm:px-6 lg:ml-64 lg:px-10 lg:py-8">
      <div class="mx-auto w-full max-w-7xl"><RouterView /></div>
    </main>
  </div>
</template>
