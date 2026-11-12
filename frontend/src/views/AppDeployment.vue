<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../api'
import AppDetail from './AppDetail.vue'
import ComposeDetail from './ComposeDetail.vue'
const app = ref<any>(),
  error = ref('')
const route = useRoute()
const id = computed(() => String(route.params.id))
watch(
  id,
  async (appID) => {
    app.value = undefined
    error.value = ''
    try {
      const loaded = await api(`/apps/${appID}`)
      if (appID === id.value) app.value = loaded
    } catch (e: any) {
      if (appID === id.value) error.value = e.message
    }
  },
  { immediate: true },
)
</script>
<template>
  <p v-if="error" class="notice-error">{{ error }}</p>
  <ComposeDetail v-else-if="app?.deployment_source === 'compose'" :key="id" :id="id" />
  <AppDetail v-else-if="app" :key="id" />
  <p v-else class="text-muted-foreground">正在读取部署…</p>
</template>
