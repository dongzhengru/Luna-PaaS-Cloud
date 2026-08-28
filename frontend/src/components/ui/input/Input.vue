<script setup lang="ts">
import { computed, type HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'
const props = defineProps<{
  defaultValue?: string | number
  modelValue?: string | number
  modelModifiers?: { number?: boolean }
  class?: HTMLAttributes['class']
}>()
const emits = defineEmits<{ (e: 'update:modelValue', payload: string | number): void }>()
const value = computed({
  get: () => props.modelValue ?? props.defaultValue ?? '',
  set: (v) => emits('update:modelValue', props.modelModifiers?.number ? Number(v) : v),
})
const inputClasses = [
  'flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1',
  'text-sm shadow-sm transition-colors outline-none',
  'placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring',
  'disabled:cursor-not-allowed disabled:opacity-50',
].join(' ')
</script>
<template>
  <input v-model="value" :class="cn(inputClasses, props.class)" />
</template>
