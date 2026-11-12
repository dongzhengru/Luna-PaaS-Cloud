<script setup lang="ts">
import { isAdmin } from '@/auth'
import LogPanel from '@/components/LogPanel.vue'
import { LogDecoder } from '@/lib/logs'
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import {
  ArrowLeft,
  Download,
  RefreshCw,
  Save,
  Play,
  Square,
  RotateCcw,
  Trash2,
} from 'lucide-vue-next'
import { api, fmt, post, put } from '../api'
import { Button } from '@/components/ui/button'
import { Card, CardHeader, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import ComposeEditor from '@/components/ComposeEditor.vue'
import ContainerStatus from '@/components/ContainerStatus.vue'
import ComposeRegistries, { type RegistryCredential } from '@/components/ComposeRegistries.vue'
import {
  composeActionLabels,
  composeStatusLabels,
  environmentText,
  formatContainerPorts,
  parseComposeEnvironment,
} from '@/lib/compose'
import { toast } from '@/lib/toast'
const registries = ref<RegistryCredential[]>([])
const props = defineProps<{ id: string }>()
const router = useRouter()
const app = ref<any>(),
  nodes = ref<any[]>([]),
  containers = ref<any[]>([]),
  versions = ref<any[]>([]),
  tasks = ref<any[]>([])
const yaml = ref(''),
  env = ref(''),
  saved = ref(''),
  active = ref<any>(),
  tab = ref('overview'),
  error = ref(''),
  containerError = ref(''),
  containersLoaded = ref(false),
  busy = ref(''),
  preview = ref<any>(),
  reviewing = ref(false)
const tabs = [
  { id: 'overview', label: '概览' },
  { id: 'containers', label: '容器列表' },
  { id: 'monitoring', label: '资源监控' },
  { id: 'logs', label: '实时日志' },
  { id: 'config', label: '编排配置' },
  { id: 'history', label: '操作记录' },
]
const logsConnected = ref(false)
const service = ref(''),
  logs = ref(''),
  logError = ref(''),
  logStatus = ref('未连接'),
  search = ref(''),
  autoScroll = ref(true),
  logBox = ref<HTMLElement>()
const runningTask = computed(() =>
  tasks.value.find((t) => ['queued', 'running'].includes(t.status)),
)
const disabled = computed(() => !!busy.value || !!runningTask.value)
const serviceNames = computed(() => [...new Set(containers.value.map((c) => c.service))].sort())
const runningCount = computed(() => containers.value.filter((c) => c.state.Running).length)
const visibleLogs = computed(() =>
  search.value
    ? logs.value
        .split('\n')
        .filter((line) => line.toLowerCase().includes(search.value.toLowerCase()))
        .join('\n')
    : logs.value,
)
const configInput = () => ({
  yaml: yaml.value,
  environment: parseComposeEnvironment(env.value),
  registries: registries.value.map((registry) => ({ ...registry })),
})
const dirty = computed(
  () =>
    JSON.stringify({ yaml: yaml.value, env: env.value, registries: registries.value }) !==
    saved.value,
)
const envChanges = computed(() => {
  try {
    const before = active.value?.environment || {},
      after = parseComposeEnvironment(env.value)
    return [...new Set([...Object.keys(before), ...Object.keys(after)])].filter(
      (k) => before[k] !== after[k],
    )
  } catch {
    return []
  }
})
const points = ref<Record<string, { cpu: number[]; memory: number[] }>>({})
let timer: ReturnType<typeof setTimeout> | undefined,
  source: EventSource | undefined,
  disposed = false,
  polling = false,
  refreshingContainers = false
let decoder = new LogDecoder()
async function load() {
  if (polling || disposed) return
  polling = true
  try {
    const [a, v, t] = await Promise.all([
      api(`/apps/${props.id}`),
      api(`/apps/${props.id}/compose/versions`),
      api(`/apps/${props.id}/compose/tasks`),
    ])
    const previous = app.value?.compose_active_version_id
    app.value = a
    versions.value = v
    tasks.value = t
    if (a.compose_active_version_id && a.compose_active_version_id !== previous)
      active.value = await api(`/apps/${props.id}/compose/versions/${a.compose_active_version_id}`)
  } catch (e: any) {
    error.value = e.message
  } finally {
    polling = false
  }
}
async function loadContainers() {
  if (refreshingContainers || disposed) return
  refreshingContainers = true
  try {
    const list = await api(`/apps/${props.id}/containers`)
    containers.value = list
    containersLoaded.value = true
    containerError.value = ''
    for (const c of list) {
      const history = points.value[c.id] || { cpu: [], memory: [] }
      history.cpu = [...history.cpu, parseFloat(c.stats?.CPUPerc || '0') || 0].slice(-60)
      history.memory = [...history.memory, parseFloat(c.stats?.MemPerc || '0') || 0].slice(-60)
      points.value[c.id] = history
    }
    for (const id of Object.keys(points.value))
      if (!list.some((c: any) => c.id === id)) delete points.value[id]
  } catch (e: any) {
    containerError.value = e.message
  } finally {
    refreshingContainers = false
  }
}
async function poll() {
  if (busy.value === 'delete' || disposed) return
  await load()
  if (['overview', 'containers', 'monitoring'].includes(tab.value)) await loadContainers()
  if (!disposed) timer = setTimeout(poll, 5000)
}
async function save() {
  busy.value = 'save'
  error.value = ''
  try {
    const fingerprint = JSON.stringify({
      yaml: yaml.value,
      env: env.value,
      registries: registries.value,
    })
    await put(`/apps/${props.id}/compose`, configInput())
    saved.value = fingerprint
    toast.success('草稿已保存')
  } catch (e: any) {
    error.value = e.message
  } finally {
    busy.value = ''
  }
}
async function validate() {
  busy.value = 'validate'
  error.value = ''
  preview.value = undefined
  reviewing.value = false
  try {
    const input = configInput()
    const response = await post('/compose/validate', {
      app_id: props.id,
      node_id: app.value.node_id,
      ...input,
    })
    if (JSON.stringify(input) !== JSON.stringify(configInput())) {
      toast.info('配置已变化，请重新校验')
      return
    }
    preview.value = response
    reviewing.value = true
  } catch (e: any) {
    error.value = e.message
  } finally {
    busy.value = ''
  }
}
async function action(kind: string, selected = '', version = '') {
  if (kind === 'delete') {
    if (
      prompt(`删除编排将移除容器和项目网络，保留数据卷。请输入应用名称 ${app.value.name}：`) !==
      app.value.name
    )
      return
  } else if (['stop', 'restart', 'rebuild', 'update_images', 'rollback'].includes(kind)) {
    if (
      !confirm(
        `确认${composeActionLabels[kind]}${selected ? `服务 ${selected}` : '整个编排'}？${kind === 'rollback' ? '数据卷内容不会回退。' : ''}`,
      )
    )
      return
  }
  busy.value = kind
  error.value = ''
  try {
    const fingerprint = JSON.stringify({
      yaml: yaml.value,
      env: env.value,
      registries: registries.value,
    })
    const body: any = { action: kind, service: selected, version_id: version }
    if (kind === 'deploy') body.config = configInput()
    const response = await post(`/apps/${props.id}/compose/actions`, body)
    reviewing.value = false
    if (kind === 'deploy') saved.value = fingerprint
    if (kind === 'delete') {
      closeLogs()
      // The retained task record remains readable after the app is removed.
      const waitDeletion = async () => {
        const task = await api(`/tasks/${response.task_id}`)
        if (task.status === 'failed') throw new Error(task.error)
        if (task.status === 'succeeded') {
          disposed = true
          if (timer) clearTimeout(timer)
          toast.success('编排已删除，数据卷已保留')
          router.push('/apps')
          return
        }
        if (!disposed)
          timer = setTimeout(
            () =>
              waitDeletion().catch((e: any) => {
                error.value = e.message
                busy.value = ''
                poll()
              }),
            1500,
          )
      }
      if (timer) clearTimeout(timer)
      await waitDeletion()
      return
    }
    await load()
    toast.success('操作已提交，可在操作记录查看进度')
  } catch (e: any) {
    error.value = e.message
  } finally {
    if (kind !== 'delete') busy.value = ''
    else if (error.value) busy.value = ''
  }
}
async function useVersion(v: any) {
  if (dirty.value && !confirm('加载历史配置会覆盖当前未保存的编辑，是否继续？')) return
  try {
    const input = await api(`/apps/${props.id}/compose/versions/${v.id}`)
    yaml.value = input.yaml
    env.value = environmentText(input.environment)
    registries.value = input.registries || []
    tab.value = 'config'
    preview.value = undefined
  } catch (e: any) {
    error.value = e.message
  }
}
function showServiceLogs(name: string) {
  service.value = name
  tab.value = 'logs'
}
function closeLogs() {
  source?.close()
  source = undefined
  logsConnected.value = false
  logStatus.value = '已暂停'
}
function connectLogs() {
  closeLogs()
  logError.value = ''
  logStatus.value = '连接中'
  logs.value = ''
  decoder = new LogDecoder()
  const stream = new EventSource(
    `/api/apps/${props.id}/logs?tail=300&service=${encodeURIComponent(service.value)}`,
  )
  source = stream
  logsConnected.value = true
  stream.addEventListener('ready', () => {
    logStatus.value = '已连接'
  })
  stream.onmessage = async (event) => {
    try {
      const bytes = Uint8Array.from(atob(event.data), (c) => c.charCodeAt(0))
      logs.value = (logs.value + decoder.decode(bytes)).slice(-1024 * 1024)
      if (autoScroll.value) {
        await nextTick()
        if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
      }
    } catch {
      logError.value = '日志解码失败'
    }
  }
  stream.addEventListener('stream-error', (event) => {
    logError.value = JSON.parse((event as MessageEvent).data)
    closeLogs()
  })
  stream.onerror = () => {
    logError.value = '日志连接已断开，请点击连接重试'
    closeLogs()
  }
}
function downloadLogs() {
  const url = URL.createObjectURL(
    new Blob([visibleLogs.value], { type: 'text/plain;charset=utf-8' }),
  )
  const link = document.createElement('a')
  link.href = url
  link.download = `${app.value.name}-${service.value || 'all'}.log`
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
function sparkline(values: number[]) {
  const maximum = Math.max(100, ...values)
  return values
    .map((v, i) => `${(i * 300) / Math.max(values.length - 1, 1)},${60 - (v / maximum) * 56}`)
    .join(' ')
}
function status(s: string) {
  return composeStatusLabels[s] || s
}
watch(tab, (value) => {
  if (value === 'logs') connectLogs()
  else closeLogs()
  if (['containers', 'monitoring'].includes(value)) loadContainers()
})
watch(service, () => {
  if (tab.value === 'logs') connectLogs()
})
watch(
  [yaml, env, registries],
  () => {
    preview.value = undefined
    reviewing.value = false
  },
  { deep: true },
)
onMounted(async () => {
  try {
    const [draft, n] = await Promise.all([api(`/apps/${props.id}/compose`), api('/nodes')])
    nodes.value = n
    yaml.value = draft.yaml
    env.value = environmentText(draft.environment)
    registries.value = draft.registries || []
    saved.value = JSON.stringify({ yaml: yaml.value, env: env.value, registries: registries.value })
  } catch (e: any) {
    error.value = e.message
  }
  await poll()
})
onUnmounted(() => {
  disposed = true
  if (timer) clearTimeout(timer)
  closeLogs()
})
</script>

<template>
  <p v-if="error" class="notice-error" role="alert">{{ error }}</p>
  <div v-if="app">
    <div class="page-header">
      <div>
        <Button variant="ghost" size="sm" as-child class="-ml-3 mb-2"
          ><RouterLink to="/apps"><ArrowLeft />返回应用</RouterLink></Button
        >
        <div class="flex items-center gap-3">
          <h1 class="page-title">{{ app.name }}</h1>
          <span class="rounded-full bg-muted px-3 py-1 text-xs">{{ status(app.status) }}</span>
        </div>
        <p class="page-description">
          Docker Compose · {{ nodes.find((n) => n.id === app.node_id)?.name || app.node_id }}
        </p>
      </div>
      <div class="flex flex-wrap gap-2">
        <Button variant="outline" :disabled="disabled" @click="action('start')"><Play />启动</Button
        ><Button variant="outline" :disabled="disabled" @click="action('stop')"
          ><Square />停止</Button
        ><Button variant="outline" :disabled="disabled" @click="action('restart')"
          ><RotateCcw />重启</Button
        ><Button
          variant="outline"
          :disabled="disabled || !app.compose_active_version_id"
          @click="action('update_images')"
          ><RefreshCw />更新镜像</Button
        ><Button variant="outline" :disabled="disabled" @click="action('rebuild')">重建</Button
        ><Button v-if="isAdmin" variant="destructive" :disabled="disabled" @click="action('delete')"
          ><Trash2 />删除</Button
        >
      </div>
    </div>
    <p v-if="runningTask" class="mb-4 rounded-lg border bg-muted/30 p-4 text-sm">
      {{ composeActionLabels[runningTask.kind.replace('compose:', '')] }}：{{
        status(runningTask.status)
      }}。可在操作记录查看结果。
    </p>
    <p v-if="app.last_error" class="notice-error">最近操作：{{ app.last_error }}</p>
    <nav class="mb-6 flex flex-wrap gap-1 border-b" aria-label="编排详情">
      <button
        v-for="item in tabs"
        :key="item.id"
        class="border-b-2 px-4 py-3 text-sm"
        :class="
          tab === item.id
            ? 'border-primary text-primary font-medium'
            : 'border-transparent text-muted-foreground'
        "
        @click="tab = item.id"
      >
        {{ item.label }}
      </button>
    </nav>
    <template v-if="tab === 'overview'">
      <div v-if="!containersLoaded" class="empty-state" role="status" aria-live="polite">
        <p v-if="containerError" class="notice-error">{{ containerError }}</p>
        <template v-else>
          <div class="size-5 animate-spin rounded-full border-2 border-muted border-t-foreground" />
          <p>正在读取容器资源…</p>
        </template>
      </div>
      <template v-else>
        <div class="mb-6 grid gap-4 sm:grid-cols-3">
          <Card
            ><CardContent class="pt-6"
              ><p class="text-sm text-muted-foreground">运行容器 / 全部容器</p>
              <p class="mt-2 text-3xl font-semibold">
                {{ runningCount }} / {{ containers.length }}
              </p></CardContent
            ></Card
          ><Card
            ><CardContent class="pt-6"
              ><p class="text-sm text-muted-foreground">服务数量</p>
              <p class="mt-2 text-3xl font-semibold">{{ serviceNames.length }}</p></CardContent
            ></Card
          ><Card
            ><CardContent class="pt-6"
              ><p class="text-sm text-muted-foreground">当前生效版本</p>
              <p class="mt-2 font-mono">
                {{ app.compose_active_version_id?.slice(0, 8) || '尚未部署成功' }}
              </p></CardContent
            ></Card
          >
        </div>
        <p v-if="containerError" class="notice-error">{{ containerError }}</p>
        <Card
          ><CardHeader
            ><h2 class="font-semibold">运行状态</h2>
            <p class="text-sm text-muted-foreground">
              容器状态来自部署节点，每 5 秒刷新。配置修改不会自动生效，需校验并部署。
            </p></CardHeader
          ><CardContent
            ><div
              v-for="c in containers"
              :key="c.id"
              class="flex flex-wrap justify-between gap-2 border-b py-3"
            >
              <span
                >{{ c.service }}
                <span
                  class="ml-2 inline-block max-w-xs truncate align-bottom text-xs text-muted-foreground"
                  :title="c.name"
                  >{{ c.name }}</span
                ></span
              ><ContainerStatus :status="c.state.Status" :health="c.state.Health?.Status" />
            </div>
            <p v-if="!containers.length" class="text-sm text-muted-foreground">
              暂无容器。可在操作记录查看部署进度或错误。
            </p></CardContent
          ></Card
        >
      </template>
    </template>
    <template v-if="tab === 'containers' || tab === 'monitoring'">
      <div v-if="!containersLoaded" class="empty-state" role="status" aria-live="polite">
        <p v-if="containerError" class="notice-error">{{ containerError }}</p>
        <template v-else>
          <div class="size-5 animate-spin rounded-full border-2 border-muted border-t-foreground" />
          <p>正在读取容器资源…</p>
        </template>
      </div>
      <template v-else>
        <div class="mb-4 flex items-center justify-between">
          <p class="text-sm text-muted-foreground">每 5 秒读取一次；趋势保留最近 60 次采样。</p>
          <Button
            variant="outline"
            size="sm"
            :disabled="refreshingContainers"
            @click="loadContainers"
            ><RefreshCw />刷新</Button
          >
        </div>
        <p v-if="containerError" class="notice-error">{{ containerError }}</p>
        <Card v-if="tab === 'containers'"
          ><CardContent class="p-0"
            ><div class="table-wrap">
              <table class="data-table min-w-[1000px] table-fixed">
                <thead>
                  <tr>
                    <th class="w-[22%]">服务 / 容器</th>
                    <th class="w-[23%]">镜像</th>
                    <th class="w-[13%]">状态</th>
                    <th class="w-[13%]">端口</th>
                    <th class="w-[8%]">重启次数</th>
                    <th class="w-[21%]">操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="c in containers" :key="c.id">
                    <td>
                      <p class="truncate font-medium" :title="c.service">{{ c.service }}</p>
                      <p class="truncate text-xs text-muted-foreground" :title="c.name">
                        {{ c.name }}
                      </p>
                    </td>
                    <td class="font-mono text-xs">
                      <p class="truncate" :title="c.image">{{ c.image }}</p>
                    </td>
                    <td>
                      <ContainerStatus
                        :status="c.state.Status"
                        :health="c.state.Health?.Status"
                        class="flex-col items-start"
                      />
                      <p v-if="c.state.OOMKilled" class="text-destructive">OOM</p>
                      <p v-if="!c.state.Running" class="text-xs">退出码 {{ c.state.ExitCode }}</p>
                    </td>
                    <td>
                      <p
                        v-for="port in formatContainerPorts(c.ports)"
                        :key="port"
                        class="break-all font-mono text-xs"
                      >
                        {{ port }}
                      </p>
                    </td>
                    <td class="tabular-nums">{{ c.restart_count ?? 0 }}</td>
                    <td>
                      <div class="flex flex-wrap gap-1">
                        <Button
                          v-for="op in ['start', 'stop', 'restart', 'rebuild']"
                          :key="op"
                          size="sm"
                          variant="ghost"
                          :disabled="disabled"
                          @click="action(op, c.service)"
                          >{{ composeActionLabels[op] }}</Button
                        ><Button size="sm" variant="ghost" @click="showServiceLogs(c.service)"
                          >日志</Button
                        >
                      </div>
                      <p class="text-xs text-muted-foreground">操作作用于服务的全部副本</p>
                    </td>
                  </tr>
                </tbody>
              </table>
              <p v-if="!containers.length" class="p-8 text-center text-muted-foreground">
                暂无容器
              </p>
            </div></CardContent
          ></Card
        >
        <div v-else class="grid gap-4 lg:grid-cols-2">
          <Card v-for="c in containers" :key="c.id"
            ><CardHeader
              ><h2 class="font-semibold">{{ c.service }} · {{ c.name }}</h2>
              <ContainerStatus
                :status="c.state.Status"
                :health="c.state.Health?.Status" /></CardHeader
            ><CardContent
              ><div class="grid grid-cols-2 gap-4">
                <div>
                  <p class="text-xs text-muted-foreground">CPU</p>
                  <p class="text-2xl font-semibold">{{ c.stats?.CPUPerc || '—' }}</p>
                  <svg
                    viewBox="0 0 300 64"
                    class="mt-2 h-16 w-full"
                    role="img"
                    aria-label="CPU 最近采样趋势"
                  >
                    <polyline
                      fill="none"
                      stroke="currentColor"
                      stroke-width="2"
                      class="text-primary"
                      :points="sparkline(points[c.id]?.cpu || [])"
                    />
                  </svg>
                </div>
                <div>
                  <p class="text-xs text-muted-foreground">内存</p>
                  <p class="text-2xl font-semibold">{{ c.stats?.MemPerc || '—' }}</p>
                  <svg
                    viewBox="0 0 300 64"
                    class="mt-2 h-16 w-full"
                    role="img"
                    aria-label="内存最近采样趋势"
                  >
                    <polyline
                      fill="none"
                      stroke="currentColor"
                      stroke-width="2"
                      class="text-primary"
                      :points="sparkline(points[c.id]?.memory || [])"
                    />
                  </svg>
                </div>
              </div>
              <div class="mt-4 grid gap-2 text-sm">
                <p>内存使用：{{ c.stats?.MemUsage || '—' }}</p>
                <p>网络 I/O：{{ c.stats?.NetIO || '—' }}</p>
                <p>磁盘 I/O：{{ c.stats?.BlockIO || '—' }}</p>
                <p>进程数：{{ c.stats?.PIDs || '—' }}</p>
              </div></CardContent
            ></Card
          >
          <p v-if="!containers.length" class="text-muted-foreground">暂无容器指标</p>
        </div>
      </template>
    </template>
    <template v-if="tab === 'logs'">
      <LogPanel :title="`${app.name} · 实时日志`">
        <template #toolbar>
          <select v-model="service" class="native-select max-w-xs" aria-label="日志服务">
            <option value="">全部服务</option>
            <option v-for="s in serviceNames" :key="s" :value="s">{{ s }}</option></select
          ><Input v-model="search" class="max-w-xs" placeholder="搜索日志" /><Button
            variant="outline"
            @click="logsConnected ? closeLogs() : connectLogs()"
            >{{ logsConnected ? '暂停接收' : '连接日志' }}</Button
          ><Button variant="outline" @click="logs = ''">清空</Button
          ><Button variant="outline" @click="downloadLogs"><Download />下载</Button
          ><label class="flex items-center gap-2 text-sm"
            ><input v-model="autoScroll" type="checkbox" />自动滚动</label
          ><span class="text-xs text-muted-foreground"
            >{{ logStatus }} · 最多保留约 100 万字符</span
          >
        </template>
        <p v-if="logError" class="notice-error">{{ logError }}</p>
        <pre
          ref="logBox"
          class="code-block h-[540px] overflow-auto whitespace-pre-wrap break-all"
          >{{ visibleLogs || '暂无日志' }}</pre>
      </LogPanel>
    </template>
    <template v-if="tab === 'config'">
      <Card
        ><CardHeader
          ><h2 class="font-semibold">
            编排配置 <span v-if="dirty" class="text-sm text-muted-foreground">· 有未保存修改</span>
          </h2>
          <p class="text-sm text-muted-foreground">
            保存草稿后不会影响运行中的容器。部署前请校验配置并检查差异。YAML 和变量整体加密存储。
          </p></CardHeader
        ><CardContent class="space-y-5"
          ><ComposeEditor v-model="yaml" />
          <div class="field">
            <Label>环境变量</Label
            ><Textarea v-model="env" class="font-mono" placeholder="KEY=VALUE" />
          </div>
          <ComposeRegistries v-model="registries" />
          <div class="flex gap-3">
            <Button variant="outline" :disabled="!!busy" @click="save"><Save />保存草稿</Button
            ><Button :disabled="disabled" @click="validate">{{
              busy === 'validate' ? '正在校验…' : '校验并查看变更'
            }}</Button>
          </div></CardContent
        ></Card
      >
      <Card v-if="reviewing && preview" class="mt-6"
        ><CardHeader
          ><h2 class="font-semibold">部署前确认</h2>
          <p class="text-sm text-muted-foreground">
            移除的服务将删除对应容器，命名数据卷保留。Compose 更新可能短暂中断服务。
          </p></CardHeader
        ><CardContent class="space-y-4"
          ><p v-for="(warning, i) in preview.warnings" :key="i" class="notice-error">
            {{ warning }}
          </p>
          <div class="grid gap-4 lg:grid-cols-2">
            <div>
              <h3 class="mb-2 text-sm font-medium">当前生效 YAML</h3>
              <pre class="code-block max-h-80 overflow-auto whitespace-pre-wrap">{{
                active?.yaml || '无生效版本'
              }}</pre>
            </div>
            <div>
              <h3 class="mb-2 text-sm font-medium">本次部署 YAML</h3>
              <pre class="code-block max-h-80 overflow-auto whitespace-pre-wrap">{{ yaml }}</pre>
            </div>
          </div>
          <p class="text-sm">新增服务：{{ preview.added?.join('、') || '无' }}</p>
          <p class="text-sm" :class="preview.removed?.length ? 'text-destructive' : ''">
            移除服务（将删除对应容器）：{{ preview.removed?.join('、') || '无' }}
          </p>
          <p class="text-sm">变更的环境变量：{{ envChanges.join('、') || '无' }}（此处隐藏值）</p>
          <div class="table-wrap">
            <table class="data-table">
              <thead>
                <tr>
                  <th>服务</th>
                  <th>镜像</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="s in preview.services" :key="s.name">
                  <td>{{ s.name }}</td>
                  <td class="break-all font-mono text-xs">{{ s.image }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <Button :disabled="disabled" @click="action('deploy')"
            >确认部署此配置</Button
          ></CardContent
        ></Card
      >
    </template>
    <template v-if="tab === 'history'">
      <Card class="mb-6"
        ><CardHeader
          ><h2 class="font-semibold">配置版本</h2>
          <p class="text-sm text-muted-foreground">
            回退恢复配置和镜像 digest，数据卷内容保持当前状态。
          </p></CardHeader
        ><CardContent
          ><div
            v-for="v in versions"
            :key="v.id"
            class="flex flex-wrap items-center justify-between gap-3 border-b py-3"
          >
            <div>
              <p class="font-mono text-sm">
                {{ v.id.slice(0, 8) }}
                <span v-if="v.id === app.compose_active_version_id" class="text-primary"
                  >当前生效</span
                >
              </p>
              <p class="mt-1 text-xs text-muted-foreground">
                {{ fmt(v.created_at) }} · {{ composeActionLabels[v.action] }} ·
                {{ status(v.status) }}
              </p>
            </div>
            <div class="flex gap-2">
              <Button variant="outline" size="sm" :disabled="!!busy" @click="useVersion(v)"
                >加载为草稿</Button
              ><Button
                v-if="v.status === 'succeeded' && v.id !== app.compose_active_version_id"
                variant="outline"
                size="sm"
                :disabled="disabled"
                @click="action('rollback', '', v.id)"
                >回退</Button
              >
            </div>
          </div>
          <p v-if="!versions.length" class="text-muted-foreground">暂无版本</p></CardContent
        ></Card
      >
      <Card
        ><CardHeader><h2 class="font-semibold">操作记录</h2></CardHeader
        ><CardContent
          ><details
            v-for="t in tasks"
            :key="t.id"
            class="border-b py-3"
            :open="t.status === 'running' || t.status === 'failed'"
          >
            <summary class="cursor-pointer text-sm">
              {{ composeActionLabels[t.kind.replace('compose:', '')] }} · {{ status(t.status) }} ·
              {{ fmt(t.created_at) }}
            </summary>
            <p v-if="t.error" class="notice-error mt-3">{{ t.error }}</p>
            <pre class="code-block mt-3 max-h-96 overflow-auto whitespace-pre-wrap break-all">{{
              t.logs || (t.status === 'running' ? '操作执行中，完成后展示完整输出…' : '暂无输出')
            }}</pre>
          </details></CardContent
        ></Card
      >
    </template>
  </div>
  <p v-else-if="!error" class="text-muted-foreground">正在读取编排部署…</p>
</template>
