export function statusLabel(status?: string, runtimeState?: string) {
  if (runtimeState === 'interrupted') return '重启后待清理'
  return status === 'running' ? '正在运行'
    : status === 'degraded' ? '运行异常'
      : status === 'stopped' ? '已停止'
        : '无法连接'
}
