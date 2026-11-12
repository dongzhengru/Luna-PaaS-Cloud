export const composeExample = `services:
  web:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "127.0.0.1:8081:80"
`
export function parseComposeEnvironment(text: string): Record<string, string> {
  const env: Record<string, string> = Object.create(null)
  for (const line of text.split('\n')) {
    if (!line.trim() || line.trimStart().startsWith('#')) continue
    const i = line.indexOf('=')
    const key = line.slice(0, i).trim()
    if (i < 1 || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key))
      throw new Error('环境变量需要使用 KEY=VALUE 格式')
    if (key in env) throw new Error(`环境变量重复：${key}`)
    env[key] = line.slice(i + 1)
  }
  return env
}
export function environmentText(env: Record<string, string> = {}) {
  return Object.entries(env || {})
    .map(([k, v]) => `${k}=${v}`)
    .join('\n')
}
export const composeActionLabels: Record<string, string> = {
  deploy: '部署配置',
  update_images: '更新镜像',
  rollback: '回退版本',
  start: '启动',
  stop: '停止',
  restart: '重启',
  rebuild: '重建',
  delete: '删除',
}
export const composeStatusLabels: Record<string, string> = {
  queued: '排队中',
  deploying: '部署中',
  starting: '启动中',
  stopping: '停止中',
  restarting: '重启中',
  rebuilding: '重建中',
  deleting: '删除中',
  pending: '待部署',
  running: '运行中',
  succeeded: '成功',
  failed: '失败',
  stopped: '已停止',
  partial: '部分服务已操作',
  operation_failed: '操作失败',
  unknown: '待核对',
}

export function formatContainerPorts(
  ports: Record<string, { HostIp?: string; HostPort: string }[] | null> | null | undefined,
): string[] {
  const result = new Set<string>()
  for (const [containerPort, bindings] of Object.entries(ports || {})) {
    const port = containerPort.replace(/\/tcp$/, '')
    if (!bindings?.length) result.add(port)
    else for (const binding of bindings) result.add(`${binding.HostPort}:${port}`)
  }
  return result.size ? [...result] : ['—']
}
