<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, post, put } from '@/api'
import { toast } from '@/lib/toast'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

const users = ref<any[]>([])
const apps = ref<any[]>([])
const selected = ref<any>(null)
const grants = ref<string[]>([])
const phone = ref('')
const role = ref('user')
const busy = ref(false)
const loadingGrants = ref(false)
let selectionVersion = 0
async function load() {
  ;[users.value, apps.value] = await Promise.all([api('/users'), api('/apps')])
}
async function select(user: any) {
  const version = ++selectionVersion
  selected.value = null
  grants.value = []
  loadingGrants.value = true
  try {
    const ids = await api<string[]>(`/users/${user.id}/apps`)
    if (version === selectionVersion) {
      grants.value = ids
      selected.value = { ...user }
    }
  } catch (e: any) {
    toast.error(e.message)
  } finally {
    if (version === selectionVersion) loadingGrants.value = false
  }
}
async function add() {
  busy.value = true
  try {
    await post('/users', { phone: phone.value, role: role.value })
    phone.value = ''
    await load()
    toast.success('用户已添加')
  } catch (e: any) {
    toast.error(e.message)
  } finally {
    busy.value = false
  }
}
async function saveUser() {
  busy.value = true
  try {
    await put(`/users/${selected.value.id}`, {
      role: selected.value.role,
      enabled: selected.value.enabled,
    })
    await load()
    toast.success('用户信息已保存')
  } catch (e: any) {
    toast.error(e.message)
  } finally {
    busy.value = false
  }
}
async function saveGrants() {
  busy.value = true
  try {
    await put(`/users/${selected.value.id}/apps`, { app_ids: grants.value })
    toast.success('应用权限已保存')
  } catch (e: any) {
    toast.error(e.message)
  } finally {
    busy.value = false
  }
}
onMounted(() => load().catch((e) => toast.error(e.message)))
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-semibold">用户与权限</h1>
      <p class="mt-2 text-sm text-muted-foreground">
        用户只能管理管理员分配的应用，删除应用仅限管理员。
      </p>
    </div>
    <form class="flex flex-wrap items-center gap-3 rounded-xl border p-4" @submit.prevent="add">
      <Input v-model="phone" class="w-60" placeholder="允许登录的手机号" required />
      <select v-model="role" class="rounded-md border bg-background p-2">
        <option value="user">用户</option>
        <option value="admin">管理员</option>
      </select>
      <Button :disabled="busy">添加用户</Button>
    </form>
    <div class="grid gap-6 md:grid-cols-2">
      <div class="space-y-2 rounded-xl border p-4">
        <p v-if="!users.length" class="text-sm text-muted-foreground">暂无用户</p>
        <button
          v-for="user in users"
          :key="user.id"
          class="flex w-full items-center justify-between rounded-md border p-3 text-left hover:bg-muted"
          :disabled="busy"
          @click="select(user)"
        >
          <span>{{ user.phone }}</span
          ><span class="text-sm text-muted-foreground"
            >{{ user.role === 'admin' ? '管理员' : '用户' }} ·
            {{ user.enabled ? '已启用' : '已停用' }}</span
          >
        </button>
      </div>
      <div class="space-y-4 rounded-xl border p-5">
        <p v-if="loadingGrants">正在加载权限…</p>
        <template v-else-if="selected">
          <h2 class="font-semibold">{{ selected.phone }}</h2>
          <div class="flex flex-wrap items-center gap-3">
            <select v-model="selected.role" class="rounded-md border bg-background p-2">
              <option value="user">用户</option>
              <option value="admin">管理员</option>
            </select>
            <label class="flex items-center gap-2"
              ><input v-model="selected.enabled" type="checkbox" />启用登录</label
            >
            <Button variant="outline" :disabled="busy" @click="saveUser">保存用户</Button>
          </div>
          <p class="text-sm text-muted-foreground">
            管理员拥有全部应用权限。普通用户的授权包括配置、发布、回滚、日志和容器操作，不包含删除应用。
          </p>
          <div v-for="app in apps" :key="app.id">
            <label class="flex items-center gap-2"
              ><input
                v-model="grants"
                :value="app.id"
                type="checkbox"
                :disabled="busy || selected.role === 'admin'"
              />{{ app.name }}</label
            >
          </div>
          <p v-if="!apps.length" class="text-sm text-muted-foreground">暂无应用</p>
          <Button :disabled="busy || selected.role === 'admin'" @click="saveGrants"
            >保存应用权限</Button
          >
        </template>
        <p v-else class="text-sm text-muted-foreground">选择用户以配置角色和应用权限。</p>
      </div>
    </div>
  </div>
</template>
