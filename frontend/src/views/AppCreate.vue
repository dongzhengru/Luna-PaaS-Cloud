<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, Rocket } from 'lucide-vue-next'
import { api, post } from '../api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { toast } from '@/lib/toast'

const router = useRouter(),
  nodes = ref<any[]>([]),
  error = ref(''),
  busy = ref(false)
const versions: Record<string, string[]> = {
  vue: ['16', '18', '20', '22', '24'],
  python: ['3.9', '3.10', '3.11', '3.12', '3.13'],
  java: ['8', '11', '17', '21', '22'],
  go: ['go.mod', '1.20', '1.21', '1.22', '1.23', '1.24'],
}
const defaults: Record<string, string> = { vue: '22', python: '3.13', java: '8', go: 'go.mod' }
const f = reactive({
  name: '',
  type: 'go',
  runtime_version: 'go.mod',
  repo_url: '',
  branch: '',
  dockerfile_path: 'Dockerfile',
  build_context: '.',
  node_id: '',
  host_port: 8081,
  container_port: 8080,
  restart_policy: 'unless-stopped',
  host_access_enabled: false,
  envText: '',
  volumeText: '',
  healthCommand: '',
})
const runtimeOptions = computed(() => versions[f.type])
watch(
  () => f.type,
  (type) => (f.runtime_version = defaults[type]),
)
onMounted(async () => (nodes.value = await api('/nodes')))
async function submit() {
  error.value = ''
  busy.value = true
  try {
    const environment = f.envText
      .split('\n')
      .filter(Boolean)
      .map((line) => {
        const i = line.indexOf('=')
        if (i < 1) throw new Error(`环境变量格式错误：${line}`)
        let key = line.slice(0, i),
          secret = false
        if (key.startsWith('!')) {
          key = key.slice(1)
          secret = true
        }
        return { key, value: line.slice(i + 1), secret }
      })
    const volumes = f.volumeText
      .split('\n')
      .filter(Boolean)
      .map((line) => {
        const [type, source, target, mode] = line.split(':')
        return { type, source, target, read_only: mode === 'ro' }
      })
    const x = await post('/apps', {
      ...f,
      environment,
      volumes,
      health: {
        command: f.healthCommand,
        interval: '10s',
        timeout: '3s',
        retries: 5,
        start_period: '10s',
      },
    })
    toast.success('应用已创建，正在初始化首次构建')
    router.push(`/apps/${x.app.id}`)
  } catch (e: any) {
    error.value = e.message
    toast.error(`创建失败：${e.message}`)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="page-header">
    <div>
      <Button variant="ghost" size="sm" as-child class="-ml-3 mb-2 text-muted-foreground"
        ><RouterLink to="/apps"><ArrowLeft />返回应用</RouterLink></Button
      >
      <h1 class="page-title">新建部署</h1>
      <p class="page-description">连接 GitHub 仓库并创建首次构建。</p>
    </div>
  </div>
  <form class="max-w-4xl" @submit.prevent="submit">
    <p v-if="error" class="notice-error">{{ error }}</p>
    <div class="content-grid">
      <Card
        ><CardHeader class="border-b"
          ><h2 class="font-semibold">基本信息</h2>
          <p class="text-sm text-muted-foreground">定义应用标识、语言运行时与源码。</p></CardHeader
        ><CardContent class="pt-6"
          ><div class="form-grid">
            <div class="field">
              <Label>应用名称</Label
              ><Input
                v-model="f.name"
                required
                maxlength="50"
                pattern="[a-z][a-z0-9-]{0,49}"
                placeholder="my-api"
                @input="f.name = f.name.toLowerCase().replace(/[^a-z0-9-]/g, '')"
              />
              <p class="field-help">小写字母开头，可使用数字和连字符。</p>
            </div>
            <div class="field">
              <Label>应用类型</Label
              ><select v-model="f.type" class="native-select">
                <option>vue</option>
                <option>python</option>
                <option>java</option>
                <option>go</option>
              </select>
            </div>
            <div class="field">
              <Label
                >{{
                  f.type === 'vue'
                    ? 'Node.js'
                    : f.type === 'java'
                      ? 'Java'
                      : f.type === 'python'
                        ? 'Python'
                        : 'Go'
                }}
                版本</Label
              ><select v-model="f.runtime_version" class="native-select">
                <option v-for="version in runtimeOptions" :key="version" :value="version">
                  {{ version }}
                </option>
              </select>
            </div>
            <div class="field">
              <Label>目标分支</Label><Input v-model="f.branch" placeholder="留空使用默认分支" />
            </div>
            <div class="field field-full">
              <Label>GitHub 仓库地址</Label
              ><Input v-model="f.repo_url" required placeholder="https://github.com/owner/repo" />
            </div></div></CardContent
      ></Card>

      <Card
        ><CardHeader class="border-b"
          ><h2 class="font-semibold">构建与运行</h2>
          <p class="text-sm text-muted-foreground">
            选择节点，设置 Docker 构建路径与端口映射。
          </p></CardHeader
        ><CardContent class="pt-6"
          ><div class="form-grid">
            <div class="field">
              <Label>部署节点</Label
              ><select v-model="f.node_id" class="native-select" required>
                <option value="" disabled>请选择</option>
                <option v-for="n in nodes" :key="n.id" :value="n.id">{{ n.name }}</option>
              </select>
            </div>
            <div class="field">
              <Label>重启策略</Label
              ><select v-model="f.restart_policy" class="native-select">
                <option>unless-stopped</option>
                <option>always</option>
                <option>on-failure</option>
                <option>no</option>
              </select>
            </div>
            <div class="field">
              <Label>Dockerfile 路径</Label><Input v-model="f.dockerfile_path" />
            </div>
            <div class="field"><Label>构建上下文</Label><Input v-model="f.build_context" /></div>
            <div class="field">
              <Label>宿主机端口</Label
              ><Input v-model.number="f.host_port" type="number" min="1" max="65535" />
            </div>
            <div class="field">
              <Label>容器端口</Label
              ><Input v-model.number="f.container_port" type="number" min="1" max="65535" />
            </div>
            <label class="field field-full flex-row items-start gap-3 rounded-lg border p-4">
              <input
                v-model="f.host_access_enabled"
                type="checkbox"
                class="mt-0.5 size-4 rounded border-input"
              />
              <span>
                <span class="block text-sm font-medium">允许访问宿主机服务</span>
                <span class="field-help">
                  发布时添加 host.docker.internal 映射，用于访问宿主机上的服务。
                </span>
              </span>
            </label>
          </div></CardContent
        ></Card
      >

      <Card
        ><CardHeader class="border-b"
          ><h2 class="font-semibold">高级配置</h2>
          <p class="text-sm text-muted-foreground">
            按需配置环境变量、持久卷与健康检查。
          </p></CardHeader
        ><CardContent class="pt-6"
          ><div class="form-grid">
            <div class="field field-full">
              <Label>环境变量</Label
              ><Textarea
                v-model="f.envText"
                class="font-mono"
                placeholder="APP_ENV=production&#10;!DATABASE_PASSWORD=secret"
              />
              <p class="field-help">每行 KEY=VALUE；敏感变量名前加 !。</p>
            </div>
            <div class="field field-full">
              <Label>持久卷</Label
              ><Textarea
                v-model="f.volumeText"
                class="font-mono"
                placeholder="named:app-data:/data&#10;bind:/srv/data:/data:ro"
              />
              <p class="field-help">每行 type:source:target[:ro]。</p>
            </div>
            <div class="field field-full">
              <Label>健康检查命令</Label
              ><Input
                v-model="f.healthCommand"
                placeholder="wget -qO- http://localhost:8080/health || exit 1"
              />
            </div></div></CardContent
      ></Card>
    </div>
    <div class="form-actions">
      <Button type="submit" :disabled="busy"
        ><span
          v-if="busy"
          class="size-4 animate-spin rounded-full border-2 border-primary-foreground/30 border-t-primary-foreground"
        /><Rocket v-else />{{ busy ? '正在初始化…' : '创建并首次构建' }}</Button
      ><Button type="button" variant="outline" as-child
        ><RouterLink to="/apps">取消</RouterLink></Button
      >
    </div>
  </form>
</template>
