<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, post } from '../api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import ComposeEditor from './ComposeEditor.vue'
import ComposeRegistries, { type RegistryCredential } from './ComposeRegistries.vue'
import { composeExample, parseComposeEnvironment } from '@/lib/compose'
import { toast } from '@/lib/toast'
const registries = ref<RegistryCredential[]>([])
const router = useRouter()
const name = ref(''),
  node = ref(''),
  yaml = ref(composeExample),
  env = ref(''),
  nodes = ref<any[]>([])
const busy = ref(''),
  error = ref(''),
  preview = ref<any>()
onMounted(async () => {
  try {
    nodes.value = await api('/nodes')
  } catch (e: any) {
    error.value = e.message
  }
})
async function act(kind: string) {
  busy.value = kind
  error.value = ''
  preview.value = undefined
  try {
    const input = {
      name: name.value,
      node_id: node.value,
      yaml: yaml.value,
      environment: parseComposeEnvironment(env.value),
      registries: registries.value,
    }
    if (kind === 'validate') preview.value = await post('/compose/validate', input)
    else {
      const x = await post('/compose/apps', input)
      toast.success('编排已创建，正在部署')
      router.push(`/apps/${x.app.id}`)
    }
  } catch (e: any) {
    error.value = e.message
  } finally {
    busy.value = ''
  }
}
</script>
<template>
  <form class="max-w-5xl space-y-6" @submit.prevent="act('deploy')">
    <p v-if="error" class="notice-error" role="alert">{{ error }}</p>
    <Card
      ><CardHeader class="border-b"
        ><h2 class="font-semibold">容器编排部署</h2>
        <p class="text-sm text-muted-foreground">
          粘贴 Docker Compose YAML，在一个节点部署多个服务。支持公共镜像和自定义私有镜像仓库。
        </p></CardHeader
      >
      <CardContent class="space-y-5 pt-6"
        ><div class="form-grid">
          <div class="field">
            <Label>应用名称</Label
            ><Input
              v-model="name"
              required
              maxlength="50"
              pattern="[a-z][a-z0-9-]{0,49}"
              placeholder="my-stack"
            />
          </div>
          <div class="field">
            <Label>部署节点</Label
            ><select v-model="node" required class="native-select">
              <option value="" disabled>请选择节点</option>
              <option v-for="n in nodes" :key="n.id" :value="n.id">{{ n.name }}</option>
            </select>
          </div>
        </div>
        <div class="field">
          <Label>Compose YAML</Label><ComposeEditor v-model="yaml" />
          <p class="field-help">
            支持 image 镜像部署；暂不支持 build、外部文件、configs/secrets
            或自定义容器名。挂载路径需在节点白名单内。
          </p>
        </div>
        <div class="field">
          <Label>环境变量</Label
          ><Textarea
            v-model="env"
            class="font-mono"
            placeholder="APP_PORT=8081&#10;DATABASE_PASSWORD=secret"
          />
          <p class="field-help">
            每行 KEY=VALUE，在 YAML 中通过 ${VARIABLE} 引用。YAML 和环境变量整体加密保存。
          </p>
        </div>
        <ComposeRegistries v-model="registries" /> </CardContent
    ></Card>
    <Card v-if="preview"
      ><CardHeader><h2 class="font-semibold">配置校验通过</h2></CardHeader
      ><CardContent
        ><p v-for="(warning, i) in preview.warnings" :key="i" class="notice-error">{{ warning }}</p>
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr>
                <th>服务</th>
                <th>镜像</th>
                <th>端口 / 挂载</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="s in preview.services" :key="s.name">
                <td>{{ s.name }}</td>
                <td class="font-mono">{{ s.image }}</td>
                <td>
                  <pre class="whitespace-pre-wrap text-xs">{{
                    JSON.stringify({ ports: s.ports, volumes: s.volumes }, null, 2)
                  }}</pre>
                </td>
              </tr>
            </tbody>
          </table>
        </div></CardContent
      ></Card
    >
    <div class="flex gap-3">
      <Button
        type="button"
        variant="outline"
        :disabled="!!busy || !node"
        @click="act('validate')"
        >{{ busy === 'validate' ? '正在校验…' : '校验配置' }}</Button
      ><Button type="submit" :disabled="!!busy">{{
        busy === 'deploy' ? '正在创建…' : '创建并部署'
      }}</Button>
    </div>
  </form>
</template>
