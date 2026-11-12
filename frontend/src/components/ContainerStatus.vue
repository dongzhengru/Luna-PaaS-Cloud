<script setup lang="ts">
import { CircleCheck, CirclePlay, Circle, HeartPulse, TriangleAlert } from 'lucide-vue-next'
defineProps<{ status: string; health?: string }>()
</script>
<template>
  <div class="flex flex-wrap gap-x-3 gap-y-1 text-xs">
    <span
      class="inline-flex items-center gap-1"
      :class="
        status === 'running' ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'
      "
    >
      <component
        :is="status === 'running' ? CirclePlay : Circle"
        class="size-3.5 shrink-0"
        aria-hidden="true"
      />
      {{ status }}
    </span>
    <span
      class="inline-flex items-center gap-1"
      :class="
        health === 'healthy'
          ? 'text-emerald-600 dark:text-emerald-400'
          : health === 'unhealthy'
            ? 'text-destructive'
            : 'text-muted-foreground'
      "
    >
      <component
        :is="
          health === 'healthy' ? CircleCheck : health === 'unhealthy' ? TriangleAlert : HeartPulse
        "
        class="size-3.5 shrink-0"
        aria-hidden="true"
      />
      {{ health || '无健康检查' }}
    </span>
  </div>
</template>
