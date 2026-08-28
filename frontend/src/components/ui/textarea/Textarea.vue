<script setup lang="ts">
import { computed, type HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'
const props = defineProps<{
  defaultValue?: string
  modelValue?: string
  class?: HTMLAttributes['class']
}>()
const emits = defineEmits<{ (e: 'update:modelValue', payload: string): void }>()
const value = computed({
  get: () => props.modelValue ?? props.defaultValue ?? '',
  set: (v) => emits('update:modelValue', v),
})
const textareaClasses = [
  'flex min-h-24 w-full rounded-md border border-input bg-transparent px-3 py-2',
  'text-sm shadow-sm outline-none placeholder:text-muted-foreground',
  'focus-visible:ring-1 focus-visible:ring-ring',
  'disabled:cursor-not-allowed disabled:opacity-50',
].join(' ')
</script>
<template>
  <textarea v-model="value" :class="cn(textareaClasses, props.class)"></textarea>
</template>
