<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Activity,
  ArrowLeft,
  ExternalLink,
  GitBranch,
  Package,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Rocket,
  RotateCcw,
  Save,
  Trash2,
  X,
} from 'lucide-vue-next'
import { api, post, put, del, fmt } from '../api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { toast } from '@/lib/toast'

const router = useRouter(),
  id = useRoute().params.id as string
const app = ref<any>(),
  builds = ref<any[]>([]),
  releases = ref<any[]>([]),
  nodes = ref<any[]>([]),
  error = ref(''),
  tab = ref('builds'),
  editing = ref(false)
const busyAction = ref('')
const versions: Record<string, string[]> = {
  vue: ['16', '18', '20', '22', '24'],
  python: ['3.9', '3.10', '3.11', '3.12', '3.13'],
  java: ['8', '11', '17', '21', '22'],
  go: ['go.mod', '1.20', '1.21', '1.22', '1.23', '1.24'],
}
const form = reactive<any>({
  node_id: '',
  runtime_version: '',
  host_port: 0,
  container_port: 0,
  restart_policy: 'unless-stopped',
  host_access_enabled: false,
  environment: [],
  volumes: [],
  health: {},
})
let timer: ReturnType<typeof setInterval>
let metricsTimer: ReturnType<typeof setInterval> | undefined
let logSource: EventSource | undefined
const metrics = ref<any>(),
  metricsError = ref(''),
  metricsRefreshing = ref(false),
  logs = ref(''),
  logError = ref(''),
  logStatus = ref('disconnected'),
  followLogs = ref(true),
  buildLogsOpen = ref(false),
  buildLogs = ref(''),
  buildLogsError = ref(''),
  buildLogsLoading = ref(false),
  selectedBuild = ref<any>(),
  logBox = ref<HTMLElement | null>(null)
let logDecoder = new TextDecoder()
async function load() {
  try {
    ;[app.value, builds.value, releases.value, nodes.value] = await Promise.all([
      api(`/apps/${id}`),
      api(`/apps/${id}/builds`),
      api(`/apps/${id}/releases`),
      api('/nodes'),
    ])
  } catch (e: any) {
    error.value = e.message
  }
}
async function release(build: any) {
  if (!confirm(`确认发布应用 ${app.value.name} 的构建版本 ${build.commit_sha?.slice(0, 8)}？`))
    return
  busyAction.value = `release:${build.id}`
  error.value = ''
  try {
    await post(`/apps/${id}/releases`, { build_id: build.id })
    await load()
    toast.success('发布任务已提交')
  } catch (e: any) {
    error.value = e.message
    toast.error(`发布失败：${e.message}`)
  } finally {
    busyAction.value = ''
  }
}
async function rollback(rid: string) {
  if (!confirm('确认回滚到这个版本？')) return
  busyAction.value = `rollback:${rid}`
  error.value = ''
  try {
    await post(`/apps/${id}/releases/${rid}/rollback`)
    await load()
    toast.success('回滚任务已提交')
  } catch (e: any) {
    error.value = e.message
    toast.error(`回滚失败：${e.message}`)
  } finally {
    busyAction.value = ''
  }
}
async function removeApp() {
  const value = prompt(
    `删除后将停止并移除节点上的应用容器，构建和发布记录也会删除。\n请输入应用名称 ${app.value.name} 确认：`,
  )
  if (value !== app.value.name) return
  busyAction.value = 'delete'
  error.value = ''
  try {
    await del(`/apps/${id}`)
    toast.success('应用已删除')
    await router.push('/apps')
  } catch (e: any) {
    error.value = e.message
    toast.error(`删除失败：${e.message}`)
  } finally {
    busyAction.value = ''
  }
}
async function retry() {
  busyAction.value = 'retry'
  error.value = ''
  try {
    await post(`/apps/${id}/initialize`)
    await load()
    toast.success('构建工作流更新任务已提交')
  } catch (e: any) {
    error.value = e.message
    toast.error(`提交失败：${e.message}`)
  } finally {
    busyAction.value = ''
  }
}
async function sync() {
  busyAction.value = 'sync'
  error.value = ''
  try {
    await post(`/apps/${id}/sync`)
    await load()
    toast.success('GitHub 构建记录已同步')
  } catch (e: any) {
    error.value = e.message
    toast.error(`同步失败：${e.message}`)
  } finally {
    busyAction.value = ''
  }
}
async function showBuildLogs(build: any) {
  selectedBuild.value = build
  buildLogs.value = ''
  buildLogsError.value = ''
  buildLogsOpen.value = true
  buildLogsLoading.value = true
  try {
    const result = await api<{ logs: string; truncated: boolean }>(
      `/apps/${id}/builds/${build.id}/logs`,
    )
    buildLogs.value = result.logs
    if (result.truncated) toast.error('构建日志过大，已显示前 8 MiB')
  } catch (e: any) {
    buildLogsError.value = e.message
  } finally {
    buildLogsLoading.value = false
  }
}
function cloneArray(value: any) {
  try {
    const parsed =
      typeof value === 'string'
        ? JSON.parse(value || '[]')
        : JSON.parse(JSON.stringify(value ?? []))
    return Array.isArray(parsed) ? parsed : []
  } catch {
    return []
  }
}
function cloneObject(value: any) {
  try {
    const parsed =
      typeof value === 'string'
        ? JSON.parse(value || '{}')
        : JSON.parse(JSON.stringify(value ?? {}))
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {}
  } catch {
    return {}
  }
}
function edit() {
  const current = app.value
  error.value = ''
  form.node_id = current.node_id
  form.runtime_version = current.runtime_version
  form.host_port = current.host_port
  form.container_port = current.container_port
  form.restart_policy = current.restart_policy
  form.host_access_enabled = Boolean(current.host_access_enabled)
  form.environment = cloneArray(current.environment)
  form.volumes = cloneArray(current.volumes_json)
  form.health = { command: '', ...cloneObject(current.health_json) }
  editing.value = true
}
async function save() {
  busyAction.value = 'save'
  error.value = ''
  try {
    const runtimeChanged = form.runtime_version !== app.value.runtime_version
    app.value = await put(`/apps/${id}`, form)
    editing.value = false
    if (runtimeChanged) await post(`/apps/${id}/initialize`)
    toast.success(runtimeChanged ? '配置已保存，构建工作流更新任务已提交' : '应用配置已保存')
  } catch (e: any) {
    error.value = e.message
    toast.error(`保存失败：${e.message}`)
  } finally {
    busyAction.value = ''
  }
}
function addEnv() {
  form.environment.push({ key: '', value: '', secret: false })
}
function addVolume() {
  form.volumes.push({ type: 'named', source: '', target: '', read_only: false })
}
function statusVariant(status: string) {
  return status === 'succeeded' || status === 'ready'
    ? 'success'
    : status === 'failed' || status === 'error'
      ? 'destructive'
      : ('warning' as const)
}
async function loadMetrics(notify = false) {
  if (notify) metricsRefreshing.value = true
  try {
    metrics.value = await api(`/apps/${id}/stats`)
    metricsError.value = ''
    if (notify) toast.success('容器资源已刷新')
  } catch (e: any) {
    metricsError.value = e.message
    if (notify) toast.error(`刷新失败：${e.message}`)
  } finally {
    if (notify) metricsRefreshing.value = false
  }
}
function startMetrics() {
  if (metricsTimer) return
  loadMetrics()
  metricsTimer = setInterval(loadMetrics, 3000)
}
function stopMetrics() {
  if (metricsTimer) clearInterval(metricsTimer)
  metricsTimer = undefined
}
function stopLogs() {
  logSource?.close()
  logSource = undefined
  logStatus.value = 'disconnected'
}
function startLogs() {
  stopLogs()
  logs.value = ''
  logDecoder = new TextDecoder()
  logError.value = ''
  logStatus.value = 'connecting'
  const source = new EventSource(`/api/apps/${id}/logs?tail=300`)
  logSource = source
  source.addEventListener('ready', () => {
    if (logSource === source) logStatus.value = 'connected'
  })
  source.onmessage = async (event) => {
    try {
      const raw = atob(event.data),
        bytes = Uint8Array.from(raw, (c) => c.charCodeAt(0))
      logs.value += logDecoder.decode(bytes, { stream: true })
      if (logs.value.length > 500_000) logs.value = logs.value.slice(-500_000)
      if (followLogs.value) {
        await nextTick()
        if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
      }
    } catch {
      logError.value = '日志数据解码失败'
    }
  }
  source.addEventListener('stream-error', (event: Event) => {
    const message = event as MessageEvent
    try {
      logError.value = JSON.parse(message.data)
    } catch {
      logError.value = message.data || '日志流已中断'
    }
    if (logSource === source) stopLogs()
  })
  source.onerror = () => {
    if (logSource === source) {
      logError.value ||= '日志连接已断开'
      stopLogs()
    }
  }
}
function toggleLogs() {
  logSource ? stopLogs() : startLogs()
}
function clearLogs() {
  logs.value = ''
  toast.info('日志显示已清空')
}
function metricPercent(value: string) {
  const n = Number.parseFloat(value)
  return Number.isFinite(n) ? Math.min(100, Math.max(0, n)) : 0
}
watch(tab, (value) => {
  value === 'monitoring' ? startMetrics() : stopMetrics()
  value === 'logs' ? startLogs() : stopLogs()
})
onMounted(() => {
  load()
  timer = setInterval(load, 5000)
})
onUnmounted(() => {
  clearInterval(timer)
  stopMetrics()
  stopLogs()
})
</script>

<template>
  <div v-if="app">
    <div class="page-header">
      <div>
        <Button variant="ghost" size="sm" as-child class="-ml-3 mb-2 text-muted-foreground"
          ><RouterLink to="/apps"><ArrowLeft />返回应用</RouterLink></Button
        >
        <div class="flex flex-wrap items-center gap-3">
          <h1 class="page-title">{{ app.name }}</h1>
          <Badge :variant="statusVariant(app.status)">{{ app.status }}</Badge>
        </div>
        <p class="page-description">
          {{ app.type }} {{ app.runtime_version }} ·
          <a
            class="hover:underline"
            :href="`https://github.com/${app.repo_owner}/${app.repo_name}`"
            target="_blank"
            rel="noopener noreferrer"
            >{{ app.repo_owner }}/{{ app.repo_name }}</a
          >
        </p>
      </div>
      <div class="flex flex-wrap gap-2">
        <Button variant="outline" :disabled="busyAction === 'retry'" @click="retry"
          ><RefreshCw :class="busyAction === 'retry' && 'animate-spin'" />{{
            busyAction === 'retry' ? '正在提交…' : '更新构建工作流'
          }}</Button
        ><Button variant="destructive" :disabled="busyAction === 'delete'" @click="removeApp"
          ><Trash2 />{{ busyAction === 'delete' ? '正在删除…' : '删除应用' }}</Button
        >
      </div>
    </div>
    <p v-if="app.last_error" class="notice-error">{{ app.last_error }}</p>
    <p v-if="error" class="notice-error">{{ error }}</p>

    <div class="stats-grid">
      <Card
        ><CardContent class="p-5"
          ><p class="stat-label">监听地址</p>
          <span class="mt-2 block font-mono text-lg font-semibold"
            >127.0.0.1:{{ app.host_port }}</span
          ></CardContent
        ></Card
      >
      <Card
        ><CardContent class="p-5"
          ><p class="stat-label">分支</p>
          <span class="mt-2 flex items-center gap-2 text-lg font-semibold"
            ><GitBranch class="size-4" />{{ app.branch || '默认分支' }}</span
          ></CardContent
        ></Card
      >
      <Card
        ><CardContent class="p-5"
          ><p class="stat-label">构建数</p>
          <span class="stat-value">{{ builds.length }}</span></CardContent
        ></Card
      >
    </div>

    <Card class="mt-4">
      <CardHeader class="overflow-x-auto border-b pb-4"
        ><div class="tab-list w-fit">
          <button
            class="tab-trigger"
            :class="{ 'tab-trigger-active': tab === 'monitoring' }"
            @click="tab = 'monitoring'"
          >
            资源监控</button
          ><button
            class="tab-trigger"
            :class="{ 'tab-trigger-active': tab === 'logs' }"
            @click="tab = 'logs'"
          >
            实时日志</button
          ><button
            class="tab-trigger"
            :class="{ 'tab-trigger-active': tab === 'builds' }"
            @click="tab = 'builds'"
          >
            构建版本</button
          ><button
            class="tab-trigger"
            :class="{ 'tab-trigger-active': tab === 'releases' }"
            @click="tab = 'releases'"
          >
            发布历史</button
          ><button
            class="tab-trigger"
            :class="{ 'tab-trigger-active': tab === 'config' }"
            @click="tab = 'config'"
          >
            配置
          </button>
        </div></CardHeader
      >
      <CardContent class="p-0">
        <template v-if="tab === 'monitoring'">
          <div class="flex items-center justify-between border-b px-6 py-3">
            <p class="text-sm text-muted-foreground">每 3 秒从部署节点获取一次容器快照</p>
            <Button
              variant="outline"
              size="sm"
              :disabled="metricsRefreshing"
              @click="loadMetrics(true)"
              ><RefreshCw :class="metricsRefreshing && 'animate-spin'" />{{
                metricsRefreshing ? '刷新中' : '立即刷新'
              }}</Button
            >
          </div>
          <div v-if="metrics" class="p-6">
            <div class="mb-5 flex flex-wrap items-center gap-3">
              <Activity class="size-5" /><span class="font-semibold">{{
                metrics.container_name
              }}</span
              ><Badge :variant="metrics.running ? 'success' : 'destructive'">{{
                metrics.status
              }}</Badge
              ><Badge
                v-if="metrics.health"
                :variant="
                  metrics.health === 'healthy'
                    ? 'success'
                    : metrics.health === 'unhealthy'
                      ? 'destructive'
                      : 'warning'
                "
                >{{ metrics.health }}</Badge
              ><span class="text-sm text-muted-foreground"
                >重启 {{ metrics.restart_count }} 次</span
              >
            </div>
            <p v-if="metricsError" class="notice-error">{{ metricsError }}</p>
            <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              <div class="metric-card">
                <p class="stat-label">CPU</p>
                <p class="metric-value">{{ metrics.cpu_percent || '—' }}</p>
                <div class="metric-track">
                  <span :style="{ width: `${metricPercent(metrics.cpu_percent)}%` }" />
                </div>
              </div>
              <div class="metric-card">
                <p class="stat-label">内存</p>
                <p class="metric-value">{{ metrics.memory_percent || '—' }}</p>
                <p class="mt-1 text-xs text-muted-foreground">
                  {{ metrics.memory_usage || '容器未运行' }}
                </p>
                <div class="metric-track">
                  <span :style="{ width: `${metricPercent(metrics.memory_percent)}%` }" />
                </div>
              </div>
              <div class="metric-card">
                <p class="stat-label">进程数</p>
                <p class="metric-value">{{ metrics.pids || '—' }}</p>
              </div>
              <div class="metric-card">
                <p class="stat-label">网络 I/O</p>
                <p class="mt-2 font-mono text-sm font-semibold">{{ metrics.network_io || '—' }}</p>
              </div>
              <div class="metric-card">
                <p class="stat-label">磁盘 I/O</p>
                <p class="mt-2 font-mono text-sm font-semibold">{{ metrics.block_io || '—' }}</p>
              </div>
              <div class="metric-card">
                <p class="stat-label">启动时间</p>
                <p class="mt-2 text-sm font-semibold">
                  {{ metrics.started_at ? fmt(metrics.started_at) : '—' }}
                </p>
                <p v-if="metrics.oom_killed" class="mt-1 text-xs text-destructive">
                  最近一次因内存不足退出
                </p>
                <p v-else-if="!metrics.running" class="mt-1 text-xs text-muted-foreground">
                  退出码 {{ metrics.exit_code }}
                </p>
              </div>
            </div>
          </div>
          <div v-else-if="metricsError" class="empty-state">
            <Activity class="size-8 text-muted-foreground/50" />
            <p>{{ metricsError }}</p>
          </div>
          <div v-else class="empty-state">
            <div
              class="size-5 animate-spin rounded-full border-2 border-muted border-t-foreground"
            />
            <p>正在读取容器资源…</p>
          </div>
        </template>

        <template v-else-if="tab === 'logs'">
          <div class="flex flex-wrap items-center justify-between gap-3 border-b px-6 py-3">
            <div class="flex items-center gap-2 text-sm">
              <span
                :class="[
                  'size-2 rounded-full',
                  logStatus === 'connected'
                    ? 'bg-emerald-500'
                    : logStatus === 'connecting'
                      ? 'animate-pulse bg-amber-500'
                      : 'bg-muted-foreground/50',
                ]"
              />{{
                logStatus === 'connected'
                  ? '实时连接'
                  : logStatus === 'connecting'
                    ? '正在连接'
                    : '已断开'
              }}
            </div>
            <div class="flex gap-2">
              <Button variant="outline" size="sm" @click="followLogs = !followLogs">{{
                followLogs ? '停止滚动' : '自动滚动'
              }}</Button
              ><Button variant="outline" size="sm" @click="clearLogs"><Trash2 />清空</Button
              ><Button variant="outline" size="sm" @click="toggleLogs"
                ><Pause v-if="logSource" /><Play v-else />{{ logSource ? '暂停' : '连接' }}</Button
              >
            </div>
          </div>
          <p v-if="logError" class="m-4 notice-error">{{ logError }}</p>
          <pre ref="logBox" class="log-console">{{
            logs || (logStatus === 'connecting' ? '正在连接容器日志…' : '暂无日志')
          }}</pre>
        </template>

        <template v-else-if="tab === 'builds'">
          <div class="flex items-center justify-between border-b px-6 py-3">
            <p class="text-sm text-muted-foreground">GitHub Actions 构建记录</p>
            <Button variant="outline" size="sm" :disabled="busyAction === 'sync'" @click="sync"
              ><RefreshCw :class="busyAction === 'sync' && 'animate-spin'" />{{
                busyAction === 'sync' ? '同步中' : '同步 GitHub'
              }}</Button
            >
          </div>
          <div v-if="builds.length" class="table-wrap">
            <table class="data-table">
              <thead>
                <tr>
                  <th>构建名称</th>
                  <th>版本</th>
                  <th>状态</th>
                  <th>时间</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="b in builds" :key="b.id">
                  <td class="max-w-80 truncate" :title="b.title || b.commit_sha">
                    {{ b.title || `提交 ${b.commit_sha?.slice(0, 8) || '—'}` }}
                  </td>
                  <td>
                    <a
                      class="inline-flex items-center gap-2 font-medium hover:underline"
                      :href="`https://github.com/${app.repo_owner}/${app.repo_name}/actions/runs/${b.run_id}`"
                      target="_blank"
                      rel="noopener noreferrer"
                      ><code>{{ b.commit_sha?.slice(0, 8) }}</code
                      ><span class="text-xs text-muted-foreground">#{{ b.run_id }}</span
                      ><ExternalLink class="size-3"
                    /></a>
                  </td>
                  <td>
                    <Badge :variant="statusVariant(b.status)">{{ b.status }}</Badge>
                  </td>
                  <td class="text-muted-foreground">{{ fmt(b.created_at) }}</td>
                  <td class="text-right">
                    <div class="flex justify-end gap-2">
                      <Button variant="outline" size="sm" @click="showBuildLogs(b)">
                        查看日志
                      </Button>
                      <Button
                        v-if="b.status === 'succeeded'"
                        size="sm"
                        :disabled="busyAction === `release:${b.id}`"
                        @click="release(b)"
                        ><Rocket />{{
                          busyAction === `release:${b.id}` ? '提交中' : '发布'
                        }}</Button
                      >
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-else class="empty-state">
            <Package class="size-8 text-muted-foreground/50" />
            <p>等待 GitHub Actions 回调…</p>
          </div>
        </template>

        <template v-else-if="tab === 'releases'">
          <div v-if="releases.length" class="table-wrap">
            <table class="data-table">
              <thead>
                <tr>
                  <th>镜像</th>
                  <th>状态</th>
                  <th>时间</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                <template v-for="r in releases" :key="r.id"
                  ><tr>
                    <td>
                      <code>{{ r.image?.split(':').pop() }}</code>
                    </td>
                    <td>
                      <Badge :variant="statusVariant(r.status)">{{ r.status }}</Badge>
                    </td>
                    <td class="text-muted-foreground">{{ fmt(r.created_at) }}</td>
                    <td class="text-right">
                      <Button
                        v-if="r.status === 'succeeded'"
                        variant="outline"
                        size="sm"
                        :disabled="busyAction === `rollback:${r.id}`"
                        @click="rollback(r.id)"
                        ><RotateCcw />{{
                          busyAction === `rollback:${r.id}` ? '提交中' : '回滚至此'
                        }}</Button
                      >
                    </td>
                  </tr>
                  <tr v-if="r.logs">
                    <td colspan="4" class="bg-muted/20">
                      <details>
                        <summary class="cursor-pointer text-sm font-medium">查看部署日志</summary>
                        <pre class="code-block mt-3">{{ r.logs }}</pre>
                      </details>
                    </td>
                  </tr></template
                >
              </tbody>
            </table>
          </div>
          <div v-else class="empty-state">
            <Package class="size-8 text-muted-foreground/50" />
            <p>暂无发布记录</p>
          </div>
        </template>

        <template v-else>
          <div v-if="!editing" class="grid gap-8 p-6 md:grid-cols-2">
            <div>
              <div class="mb-4 flex items-center justify-between">
                <h2 class="font-semibold">部署配置</h2>
                <Button variant="outline" size="sm" @click="edit"><Pencil />编辑</Button>
              </div>
              <dl class="grid gap-4 text-sm">
                <div class="flex justify-between gap-6">
                  <dt class="text-muted-foreground">构建版本</dt>
                  <dd>
                    <code>{{ app.runtime_version }}</code>
                  </dd>
                </div>
                <div class="flex justify-between gap-6">
                  <dt class="text-muted-foreground">Dockerfile</dt>
                  <dd>
                    <code>{{ app.dockerfile_path }}</code>
                  </dd>
                </div>
                <div class="flex justify-between gap-6">
                  <dt class="text-muted-foreground">构建上下文</dt>
                  <dd>
                    <code>{{ app.build_context }}</code>
                  </dd>
                </div>
                <div class="flex justify-between gap-6">
                  <dt class="text-muted-foreground">端口</dt>
                  <dd>{{ app.host_port }} → {{ app.container_port }}</dd>
                </div>
                <div class="flex justify-between gap-6">
                  <dt class="text-muted-foreground">宿主机访问</dt>
                  <dd>{{ app.host_access_enabled ? '已启用' : '未启用' }}</dd>
                </div>
              </dl>
            </div>
            <div>
              <h2 class="mb-4 font-semibold">环境变量</h2>
              <div v-if="app.environment?.length" class="space-y-2">
                <div
                  v-for="e in app.environment"
                  :key="e.key"
                  class="flex items-center justify-between rounded-md border px-3 py-2 text-sm"
                >
                  <code>{{ e.key }}</code
                  ><span class="text-muted-foreground">{{ e.secret ? '••••••' : e.value }}</span>
                </div>
              </div>
              <p v-else class="text-sm text-muted-foreground">未配置环境变量。</p>
            </div>
          </div>

          <form v-else class="p-6" @submit.prevent="save">
            <div class="form-grid">
              <div class="field">
                <Label>构建运行时版本</Label
                ><select v-model="form.runtime_version" class="native-select">
                  <option v-for="version in versions[app.type]" :key="version" :value="version">
                    {{ version }}
                  </option>
                </select>
              </div>
              <div class="field">
                <Label>部署节点</Label
                ><select v-model="form.node_id" class="native-select">
                  <option v-for="n in nodes" :key="n.id" :value="n.id">{{ n.name }}</option>
                </select>
              </div>
              <div class="field">
                <Label>重启策略</Label
                ><select v-model="form.restart_policy" class="native-select">
                  <option>unless-stopped</option>
                  <option>always</option>
                  <option>on-failure</option>
                  <option>no</option>
                </select>
              </div>
              <div class="field">
                <Label>宿主机端口</Label><Input v-model.number="form.host_port" type="number" />
              </div>
              <div class="field">
                <Label>容器端口</Label><Input v-model.number="form.container_port" type="number" />
              </div>
              <label class="field field-full flex-row items-start gap-3 rounded-lg border p-4">
                <input
                  v-model="form.host_access_enabled"
                  type="checkbox"
                  class="mt-0.5 size-4 rounded border-input"
                />
                <span>
                  <span class="block text-sm font-medium">允许访问宿主机服务</span>
                  <span class="field-help">
                    下次发布时添加 host.docker.internal 映射，用于访问宿主机上的服务。
                  </span>
                </span>
              </label>
              <div class="field field-full">
                <div class="flex items-center justify-between">
                  <Label>环境变量</Label
                  ><Button type="button" variant="outline" size="sm" @click="addEnv"
                    ><Plus />添加变量</Button
                  >
                </div>
                <div class="space-y-2">
                  <div
                    v-for="(e, i) in form.environment"
                    :key="i"
                    class="grid gap-2 rounded-lg border p-3 sm:grid-cols-[1fr_1fr_auto_auto] sm:items-center"
                  >
                    <Input v-model="e.key" placeholder="KEY" /><Input
                      v-model="e.value"
                      :placeholder="e.secret ? '留空保持原密文' : 'VALUE'"
                    /><label class="flex items-center gap-2 whitespace-nowrap text-sm"
                      ><input
                        v-model="e.secret"
                        type="checkbox"
                        class="size-4 rounded border-input"
                      />敏感</label
                    ><Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      class="text-destructive"
                      @click="form.environment.splice(i, 1)"
                      ><Trash2
                    /></Button>
                  </div>
                </div>
              </div>
              <div class="field field-full">
                <div class="flex items-center justify-between">
                  <Label>持久卷</Label
                  ><Button type="button" variant="outline" size="sm" @click="addVolume"
                    ><Plus />添加卷</Button
                  >
                </div>
                <div class="space-y-2">
                  <div
                    v-for="(v, i) in form.volumes"
                    :key="i"
                    class="grid gap-2 rounded-lg border p-3 sm:grid-cols-[140px_1fr_1fr_auto] sm:items-center"
                  >
                    <select v-model="v.type" class="native-select">
                      <option>named</option>
                      <option>bind</option></select
                    ><Input v-model="v.source" placeholder="source" /><Input
                      v-model="v.target"
                      placeholder="/target"
                    /><Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      class="text-destructive"
                      @click="form.volumes.splice(i, 1)"
                      ><Trash2
                    /></Button>
                  </div>
                </div>
              </div>
              <div class="field field-full">
                <Label>健康检查命令</Label><Input v-model="form.health.command" />
              </div>
            </div>
            <div class="form-actions">
              <Button type="submit" :disabled="busyAction === 'save'"
                ><Save />{{ busyAction === 'save' ? '正在保存…' : '保存配置' }}</Button
              ><Button
                type="button"
                variant="outline"
                :disabled="busyAction === 'save'"
                @click="editing = false"
                ><X />取消</Button
              >
            </div>
          </form>
        </template>
      </CardContent>
    </Card>
    <div
      v-if="buildLogsOpen"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      @click.self="buildLogsOpen = false"
    >
      <section
        class="flex max-h-[90vh] w-full max-w-5xl flex-col overflow-hidden rounded-lg border bg-background shadow-xl"
      >
        <header class="flex items-center justify-between border-b px-5 py-3">
          <div>
            <h2 class="font-semibold">构建日志</h2>
            <p class="text-xs text-muted-foreground">
              {{ selectedBuild?.title || selectedBuild?.commit_sha }}
            </p>
          </div>
          <Button
            variant="ghost"
            size="icon"
            aria-label="关闭构建日志"
            @click="buildLogsOpen = false"
            ><X
          /></Button>
        </header>
        <div class="overflow-auto p-5">
          <p v-if="buildLogsLoading" class="text-sm text-muted-foreground">
            正在从 GitHub 获取日志…
          </p>
          <p v-else-if="buildLogsError" class="notice-error">{{ buildLogsError }}</p>
          <pre v-else class="code-block max-h-[70vh]">{{ buildLogs || '暂无日志' }}</pre>
        </div>
      </section>
    </div>
  </div>
</template>
