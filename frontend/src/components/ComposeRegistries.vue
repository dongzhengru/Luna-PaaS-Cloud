<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
export type RegistryCredential = { server: string; username: string; password: string }
const registries = defineModel<RegistryCredential[]>({ default: () => [] })
</script>
<template>
  <div class="space-y-3">
    <div class="flex items-center justify-between">
      <Label>私有镜像仓库（可选）</Label
      ><Button
        type="button"
        variant="outline"
        size="sm"
        @click="registries.push({ server: '', username: '', password: '' })"
        >添加凭证</Button
      >
    </div>
    <p class="field-help">
      公共镜像无需填写。仓库地址如 registry.example.com；凭证随配置加密保存。使用系统 ACR
      时可沿用系统设置。
    </p>
    <div
      v-for="(registry, i) in registries"
      :key="i"
      class="grid gap-2 rounded-lg border p-3 sm:grid-cols-[1fr_1fr_1fr_auto]"
    >
      <Input
        v-model="registry.server"
        required
        placeholder="仓库地址（不含协议）"
        aria-label="镜像仓库地址"
        autocomplete="off"
      /><Input
        v-model="registry.username"
        required
        placeholder="用户名"
        aria-label="镜像仓库用户名"
        autocomplete="off"
      /><Input
        v-model="registry.password"
        required
        type="password"
        placeholder="密码或令牌"
        aria-label="镜像仓库密码"
        autocomplete="new-password"
      /><Button type="button" variant="ghost" @click="registries.splice(i, 1)">移除</Button>
    </div>
  </div>
</template>
