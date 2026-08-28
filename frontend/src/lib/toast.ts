import { reactive } from 'vue'

export type ToastVariant = 'success' | 'error' | 'info'

export interface ToastMessage {
  id: number
  message: string
  variant: ToastVariant
}

export const toasts = reactive<ToastMessage[]>([])
let nextID = 1

export function dismissToast(id: number) {
  const index = toasts.findIndex((item) => item.id === id)
  if (index >= 0) toasts.splice(index, 1)
}

function show(message: string, variant: ToastVariant, duration = 3500) {
  const id = nextID++
  toasts.push({ id, message, variant })
  window.setTimeout(() => dismissToast(id), duration)
  return id
}

export const toast = {
  success: (message: string) => show(message, 'success'),
  error: (message: string) => show(message, 'error', 5000),
  info: (message: string) => show(message, 'info'),
}
