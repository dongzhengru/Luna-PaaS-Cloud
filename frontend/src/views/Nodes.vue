<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Plus, RefreshCw, Server, X } from 'lucide-vue-next'
import { api, post } from '../api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { toast } from '@/lib/toast'

const nodes = ref<any[]>([])
const error = ref('')
const show = ref(false)
const saving = ref(false)
const testingID = ref('')
const f = reactive({
  name: '',
  type: 'local',
  host: '',
  port: 22,
  username: '',
  auth_type: 'password',
  password: '',
  private_key: '',
  passphrase: '',
  host_key: '',
  deploy_root: '/opt/paas/apps',
  allowed_mount_roots: '/srv/paas',
})
async function load() {
  nodes.value = await api('/nodes')
}
async function save() {
  saving.value = true
  error.value = ''
  try {
    await post('/nodes', f)
    show.value = false
    await load()
    toast.success('部署节点已测试并保存')
  } catch (e: any) {
    error.value = e.message
    toast.error(`保存失败：${e.message}`)
  } finally {
    saving.value = false
  }
}
async function test(id: string) {
  testingID.value = id
  error.value = ''
  try {
    await post(`/nodes/${id}/test`)
    await load()
    toast.success('节点连接检测通过')
  } catch (e: any) {
    error.value = e.message
    toast.error(`检测失败：${e.message}`)
  } finally {
    testingID.value = ''
  }
}
onMounted(load)
function statusVariant(status: string) {
  return status === 'ready' ? 'success' : status === 'error' ? 'destructive' : ('warning' as const)
}
</script>

<template>
  <div class="page-header">
    <div>
      <h1 class="page-title">部署节点</h1>
      <p class="page-description">管理本机和 SSH 运行节点。</p>
    </div>
    <Button @click="show = !show"
      ><X v-if="show" /><Plus v-else />{{ show ? '收起表单' : '添加节点' }}</Button
    >
  </div>
  <p v-if="error" class="notice-error">{{ error }}</p>

  <Card v-if="show" class="mb-4">
    <CardHeader
      ><h2 class="font-semibold">添加部署节点</h2>
      <p class="text-sm text-muted-foreground">保存前会测试连接与运行环境。</p></CardHeader
    >
    <CardContent
      ><form @submit.prevent="save">
        <div class="form-grid">
          <div class="field">
            <Label>名称</Label><Input v-model="f.name" required placeholder="production-01" />
          </div>
          <div class="field">
            <Label>类型</Label
            ><select v-model="f.type" class="native-select">
              <option value="local">本机</option>
              <option value="ssh">SSH</option>
            </select>
          </div>
          <template v-if="f.type === 'ssh'">
            <div class="field">
              <Label>主机</Label><Input v-model="f.host" required placeholder="10.0.0.10" />
            </div>
            <div class="field"><Label>端口</Label><Input v-model="f.port" type="number" /></div>
            <div class="field"><Label>用户名</Label><Input v-model="f.username" /></div>
            <div class="field">
              <Label>认证方式</Label
              ><select v-model="f.auth_type" class="native-select">
                <option value="password">密码</option>
                <option value="private_key">私钥</option>
              </select>
            </div>
            <div v-if="f.auth_type === 'password'" class="field field-full">
              <Label>密码</Label><Input v-model="f.password" type="password" />
            </div>
            <div v-else class="field field-full">
              <Label>私钥 PEM</Label><Textarea v-model="f.private_key" class="font-mono" />
            </div>
            <div class="field field-full">
              <Label>SSH 主机指纹</Label><Input v-model="f.host_key" placeholder="SHA256:..." />
              <p class="field-help">首次测试失败时会显示观测到的指纹。</p>
            </div>
          </template>
          <div class="field"><Label>部署根目录</Label><Input v-model="f.deploy_root" /></div>
          <div class="field">
            <Label>允许挂载根目录</Label><Input v-model="f.allowed_mount_roots" />
            <p class="field-help">多个目录使用逗号分隔。</p>
          </div>
        </div>
        <div class="form-actions">
          <Button type="submit" :disabled="saving"
            ><span
              v-if="saving"
              class="size-4 animate-spin rounded-full border-2 border-primary-foreground/30 border-t-primary-foreground"
            />{{ saving ? '正在测试…' : '测试并保存' }}</Button
          ><Button type="button" variant="outline" :disabled="saving" @click="show = false"
            >取消</Button
          >
        </div>
      </form></CardContent
    >
  </Card>

  <Card
    ><CardContent class="p-0"
      ><div v-if="nodes.length" class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>节点</th>
              <th>连接</th>
              <th>部署目录</th>
              <th>状态</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="n in nodes" :key="n.id">
              <td>
                <p class="font-medium">{{ n.name }}</p>
                <p class="mt-0.5 text-xs text-muted-foreground">{{ n.type }}</p>
              </td>
              <td>{{ n.type === 'local' ? '本机' : `${n.username}@${n.host}:${n.port}` }}</td>
              <td>
                <code>{{ n.deploy_root }}</code>
              </td>
              <td>
                <Badge :variant="statusVariant(n.status)">{{ n.status }}</Badge>
              </td>
              <td class="text-right">
                <Button
                  variant="outline"
                  size="sm"
                  :disabled="testingID === n.id"
                  @click="test(n.id)"
                  ><RefreshCw :class="testingID === n.id && 'animate-spin'" />{{
                    testingID === n.id ? '检测中' : '重新检测'
                  }}</Button
                >
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty-state">
        <Server class="size-8 text-muted-foreground/50" />
        <p>暂无部署节点</p>
      </div></CardContent
    ></Card
  >
</template>
