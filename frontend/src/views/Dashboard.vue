<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ArrowRight, Boxes, PackageCheck, Plus, Server } from 'lucide-vue-next'
import { api } from '../api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'

const apps = ref<any[]>([])
const nodes = ref<any[]>([])
onMounted(async () => {
  ;[apps.value, nodes.value] = await Promise.all([api('/apps'), api('/nodes')])
})
</script>

<template>
  <div class="page-header">
    <div>
      <h1 class="page-title">运行概览</h1>
      <p class="page-description">快速了解应用、节点与发布状态。</p>
    </div>
    <Button as-child
      ><RouterLink to="/apps/new"><Plus />新建部署</RouterLink></Button
    >
  </div>

  <div class="stats-grid">
    <Card
      ><CardContent class="flex items-center gap-4 p-5"
        ><div class="grid size-10 place-items-center rounded-lg bg-muted">
          <Boxes class="size-5" />
        </div>
        <div>
          <p class="stat-label">应用总数</p>
          <span class="stat-value">{{ apps.length }}</span>
        </div></CardContent
      ></Card
    >
    <Card
      ><CardContent class="flex items-center gap-4 p-5"
        ><div class="grid size-10 place-items-center rounded-lg bg-muted">
          <Server class="size-5" />
        </div>
        <div>
          <p class="stat-label">就绪节点</p>
          <span class="stat-value">{{ nodes.filter((n) => n.status === 'ready').length }}</span>
        </div></CardContent
      ></Card
    >
    <Card
      ><CardContent class="flex items-center gap-4 p-5"
        ><div class="grid size-10 place-items-center rounded-lg bg-muted">
          <PackageCheck class="size-5" />
        </div>
        <div>
          <p class="stat-label">运行版本</p>
          <span class="stat-value">{{ apps.filter((a) => a.active_release_id).length }}</span>
        </div></CardContent
      ></Card
    >
  </div>

  <Card class="mt-4">
    <CardHeader class="flex-row items-center justify-between space-y-0 pb-3"
      ><div>
        <h2 class="font-semibold">最近应用</h2>
        <p class="mt-1 text-sm text-muted-foreground">最近创建的部署项目。</p>
      </div>
      <Button variant="ghost" size="sm" as-child
        ><RouterLink to="/apps">查看全部<ArrowRight /></RouterLink></Button
    ></CardHeader>
    <CardContent class="p-0">
      <div v-if="apps.length" class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>类型</th>
              <th>仓库</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in apps.slice(0, 6)" :key="a.id">
              <td>
                <RouterLink class="font-medium hover:underline" :to="`/apps/${a.id}`">{{
                  a.name
                }}</RouterLink>
              </td>
              <td class="text-muted-foreground">{{ a.type }}</td>
              <td>{{ a.repo_owner }}/{{ a.repo_name }}</td>
              <td>
                <Badge :variant="a.active_release_id ? 'success' : 'warning'">{{
                  a.active_release_id ? '运行中' : '待发布'
                }}</Badge>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty-state">
        <Boxes class="size-8 text-muted-foreground/50" />
        <p>还没有应用，创建第一个部署吧。</p>
      </div>
    </CardContent>
  </Card>
</template>
