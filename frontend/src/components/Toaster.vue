<script setup lang="ts">
import { CheckCircle2, Info, X, XCircle } from 'lucide-vue-next'
import { dismissToast, toasts } from '@/lib/toast'
</script>

<template>
  <Teleport to="body">
    <div
      class="pointer-events-none fixed right-4 top-4 z-[100] flex w-[calc(100%-2rem)] max-w-sm flex-col gap-2"
      aria-live="polite"
      aria-atomic="true"
    >
      <TransitionGroup name="toast">
        <div
          v-for="item in toasts"
          :key="item.id"
          :class="['toast-item', `toast-${item.variant}`]"
          role="status"
        >
          <CheckCircle2 v-if="item.variant === 'success'" class="size-5 shrink-0" />
          <XCircle v-else-if="item.variant === 'error'" class="size-5 shrink-0" />
          <Info v-else class="size-5 shrink-0" />
          <p class="min-w-0 flex-1 text-sm font-medium leading-5">{{ item.message }}</p>
          <button
            class="-mr-1 rounded p-1 opacity-60 transition-opacity hover:opacity-100"
            aria-label="关闭消息"
            @click="dismissToast(item.id)"
          >
            <X class="size-4" />
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-enter-active,
.toast-leave-active {
  transition:
    opacity 180ms ease,
    transform 180ms ease;
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateX(1rem);
}
</style>
