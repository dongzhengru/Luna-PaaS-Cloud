export async function api<T = any>(path: string, options: RequestInit = {}): Promise<T> {
  const r = await fetch('/api' + path, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
    ...options,
  })
  if (r.status === 204) return undefined as T
  const body = await r.json().catch(() => ({}))
  if (!r.ok) throw new Error(body.error || `请求失败 (${r.status})`)
  return body
}
export const post = <T = any>(p: string, v: any = {}) =>
  api<T>(p, { method: 'POST', body: JSON.stringify(v) })
export const put = <T = any>(p: string, v: any) =>
  api<T>(p, { method: 'PUT', body: JSON.stringify(v) })
export const del = <T = any>(p: string) => api<T>(p, { method: 'DELETE' })
export const fmt = (v: string) => (v ? new Date(v).toLocaleString('zh-CN') : '—')
