<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Save, ShieldCheck } from 'lucide-vue-next'
import { api, put } from '../api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { toast } from '@/lib/toast'

const state = ref<any>()
const ok = ref('')
const error = ref('')
const saving = ref(false)
const f = reactive({ github_token: '', acr_username: '', acr_password: '', dingtalk_webhook: '' })
onMounted(async () => (state.value = await api('/settings')))
async function save() {
  saving.value = true
  try {
    state.value = await put('/settings', f)
    f.github_token = f.acr_password = f.dingtalk_webhook = ''
    ok.value = '设置已加密保存'
    error.value = ''
    toast.success(ok.value)
  } catch (e: any) {
    ok.value = ''
    error.value = e.message
    toast.error(`保存失败：${e.message}`)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page-header">
    <div>
      <h1 class="page-title">系统设置</h1>
      <p class="page-description">配置代码仓库、镜像仓库与通知集成。</p>
    </div>
  </div>
  <Card class="max-w-4xl">
    <CardHeader class="border-b"
      ><div class="flex items-center gap-3">
        <div class="grid size-9 place-items-center rounded-lg bg-muted">
          <ShieldCheck class="size-5" />
        </div>
        <div>
          <h2 class="font-semibold">安全与集成</h2>
          <p class="text-sm text-muted-foreground">敏感信息将加密保存，不会通过接口返回。</p>
        </div>
      </div></CardHeader
    >
    <CardContent class="pt-6"
      ><form @submit.prevent="save">
        <p v-if="ok" class="notice-success">{{ ok }}</p>
        <p v-if="error" class="notice-error">{{ error }}</p>
        <div class="form-grid">
          <div class="field field-full">
            <div class="flex items-center justify-between gap-3">
              <Label>GitHub Fine-grained PAT</Label
              ><Badge :variant="state?.github_configured ? 'success' : 'secondary'">{{
                state?.github_configured ? '已配置' : '未配置'
              }}</Badge>
            </div>
            <Input v-model="f.github_token" type="password" placeholder="留空保持现有值" />
          </div>
          <div class="field">
            <Label>ACR 用户名</Label><Input v-model="f.acr_username" placeholder="zhengru" />
          </div>
          <div class="field">
            <div class="flex items-center justify-between gap-3">
              <Label>ACR 密码</Label
              ><Badge :variant="state?.acr_configured ? 'success' : 'secondary'">{{
                state?.acr_configured ? '已配置' : '未配置'
              }}</Badge>
            </div>
            <Input v-model="f.acr_password" type="password" placeholder="留空保持现有值" />
          </div>
          <div class="field field-full">
            <div class="flex items-center justify-between gap-3">
              <Label>钉钉机器人 Webhook</Label
              ><Badge :variant="state?.notification_configured ? 'success' : 'secondary'">{{
                state?.notification_configured ? '已配置' : '未配置'
              }}</Badge>
            </div>
            <Input
              v-model="f.dingtalk_webhook"
              type="password"
              placeholder="https://oapi.dingtalk.com/robot/send?access_token=…"
            />
            <p class="field-help">留空保持现有地址。</p>
          </div>
          <div class="field field-full">
            <Label>镜像地址</Label
            ><Input :model-value="`${state?.registry || ''}/${state?.namespace || ''}`" disabled />
          </div>
          <div class="field field-full">
            <Label>公网回调地址</Label><Input :model-value="state?.public_url" disabled />
          </div>
        </div>
        <div class="form-actions">
          <Button type="submit" :disabled="saving"
            ><span
              v-if="saving"
              class="size-4 animate-spin rounded-full border-2 border-primary-foreground/30 border-t-primary-foreground"
            /><Save v-else />{{ saving ? '正在保存…' : '保存设置' }}</Button
          >
        </div>
      </form></CardContent
    >
  </Card>
</template>
