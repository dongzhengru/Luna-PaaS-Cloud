<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Boxes, ChevronRight, Plus } from 'lucide-vue-next'
import { api, fmt } from '../api'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'

const apps = ref<any[]>([])
const error = ref('')
const router = useRouter()
function openApp(id: string) {
  router.push(`/apps/${id}`)
}
onMounted(async () => {
  try {
    apps.value = await api('/apps')
  } catch (e: any) {
    error.value = e.message
  }
})
</script>

<template>
  <div class="page-header">
    <div>
      <h1 class="page-title">应用</h1>
      <p class="page-description">管理代码仓库、构建与发布。</p>
    </div>
    <Button as-child
      ><RouterLink to="/apps/new"><Plus />新建部署</RouterLink></Button
    >
  </div>
  <p v-if="error" class="notice-error">{{ error }}</p>
  <Card
    ><CardContent class="p-0"
      ><div v-if="apps.length" class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>应用</th>
              <th>仓库 / 分支</th>
              <th>端口</th>
              <th>创建时间</th>
              <th class="w-12"></th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="a in apps"
              :key="a.id"
              class="cursor-pointer focus-visible:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
              tabindex="0"
              role="link"
              :aria-label="`查看应用 ${a.name}`"
              @click="openApp(a.id)"
              @keydown.enter="openApp(a.id)"
            >
              <td>
                <p class="font-medium">{{ a.name }}</p>
                <p class="mt-0.5 text-xs text-muted-foreground">{{ a.type }}</p>
              </td>
              <td>
                {{ a.repo_owner }}/{{ a.repo_name
                }}<span class="text-muted-foreground"> · {{ a.branch || '默认分支' }}</span>
              </td>
              <td class="font-mono text-xs">
                127.0.0.1:{{ a.host_port }} → {{ a.container_port }}
              </td>
              <td class="text-muted-foreground">{{ fmt(a.created_at) }}</td>
              <td>
                <Button variant="ghost" size="icon" as-child
                  ><RouterLink :to="`/apps/${a.id}`" :aria-label="`查看 ${a.name}`" @click.stop
                    ><ChevronRight /></RouterLink
                ></Button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty-state">
        <Boxes class="size-8 text-muted-foreground/50" />
        <p>暂无应用</p>
        <Button size="sm" as-child><RouterLink to="/apps/new">创建第一个应用</RouterLink></Button>
      </div></CardContent
    ></Card
  >
</template>
