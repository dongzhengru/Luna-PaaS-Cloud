<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'
const props = defineProps<{
  variant?: 'default' | 'secondary' | 'success' | 'warning' | 'destructive' | 'outline'
  class?: HTMLAttributes['class']
}>()
const styles = {
  default: 'border-transparent bg-primary text-primary-foreground',
  secondary: 'border-transparent bg-secondary text-secondary-foreground',
  success:
    'border-transparent bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300',
  warning: 'border-transparent bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300',
  destructive: 'border-transparent bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300',
  outline: 'text-foreground',
}
</script>
<template>
  <span
    :class="
      cn(
        'inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors',
        styles[variant || 'default'],
        props.class,
      )
    "
    ><slot
  /></span>
</template>
