import { computed, ref } from 'vue'
import { api } from './api'

export const currentUser = ref<{ id: string; phone: string; role: string } | null>(null)
export const isAdmin = computed(() => currentUser.value?.role === 'admin')
export async function loadUser() {
  currentUser.value = await api('/auth/me')
}
