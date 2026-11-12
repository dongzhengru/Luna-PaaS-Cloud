<script setup lang="ts">
import { nextTick, onUnmounted, ref } from 'vue'
import { Maximize2, X } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'

defineProps<{ title: string }>()
const inlineHost = ref<HTMLElement>()
const modalHost = ref<HTMLElement>()
const dialog = ref<HTMLDialogElement>()
const expanded = ref(false)
let previousOverflow = ''

function open() {
  if (!dialog.value || !modalHost.value) return
  previousOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  dialog.value.showModal()
  expanded.value = true
}
function close() {
  if (!expanded.value) return
  expanded.value = false
  dialog.value?.close()
  document.body.style.overflow = previousOverflow
  nextTick(() => inlineHost.value?.querySelector<HTMLButtonElement>('[data-log-expand]')?.focus())
}
onUnmounted(() => {
  if (expanded.value) document.body.style.overflow = previousOverflow
})
</script>

<template>
  <div ref="inlineHost" class="min-w-0" />
  <Teleport to="body">
    <dialog
      ref="dialog"
      :aria-label="title"
      class="log-dialog m-auto h-[92dvh] max-h-[92dvh] w-[96vw] max-w-none overflow-hidden rounded-xl border bg-background p-0 text-foreground shadow-2xl backdrop:bg-black/60"
      @cancel.prevent="close"
      @close="close"
      @click.self="close"
    >
      <div class="flex h-full min-h-0 flex-col">
        <header class="flex shrink-0 items-center justify-between gap-4 border-b px-5 py-3">
          <h2 class="truncate font-semibold">{{ title }}</h2>
          <Button variant="ghost" size="icon" aria-label="关闭实时日志弹窗" @click="close">
            <X />
          </Button>
        </header>
        <div ref="modalHost" class="flex min-h-0 flex-1 flex-col" />
      </div>
    </dialog>
  </Teleport>
  <Teleport v-if="inlineHost && modalHost" :to="expanded ? modalHost : inlineHost">
    <div :class="['log-panel min-w-0', expanded && 'flex min-h-0 flex-1 flex-col']">
      <div class="flex shrink-0 flex-wrap items-center gap-3 border-b px-5 py-3">
        <slot name="toolbar" />
        <Button
          v-if="!expanded"
          data-log-expand
          variant="outline"
          size="sm"
          class="ml-auto"
          @click="open"
        >
          <Maximize2 />大屏查看
        </Button>
      </div>
      <div :class="['log-panel-body min-w-0', expanded && 'flex min-h-0 flex-1 flex-col']">
        <slot />
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.log-panel-body :deep(pre) {
  min-width: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: normal;
}
.log-panel.flex .log-panel-body :deep(pre) {
  flex: 1;
  min-height: 0;
  height: auto;
  max-height: none;
}
</style>
