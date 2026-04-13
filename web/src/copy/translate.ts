import { zhCN } from './zh-CN'

const copy = zhCN

export function translateNodeStatus(status: string, online: boolean) {
  const normalized = status.trim().toLowerCase()
  if (normalized === 'pending_install') {
    return copy.status.node.pendingInstall
  }
  if (normalized === 'unclaimed') {
    return copy.status.node.unclaimed
  }
  return online ? copy.status.node.online : copy.status.node.offline
}

export function translateTaskStatus(status: string) {
  const normalized = status.trim().toLowerCase()
  if (!normalized) {
    return copy.status.task.pending
  }
  switch (normalized) {
    case 'queued':
      return copy.status.task.queued
    case 'running':
      return copy.status.task.running
    case 'succeeded':
      return copy.status.task.succeeded
    case 'failed':
      return copy.status.task.failed
    case 'skipped':
      return copy.status.task.skipped
    case 'halted':
      return copy.status.task.halted
    default:
      return status
  }
}

export function translateTaskEvent(event: string) {
  const normalized = event.trim().toLowerCase()
  switch (normalized) {
    case 'task_created':
      return copy.status.event.taskCreated
    case 'task_complete':
      return copy.status.event.taskComplete
    case 'task_halted':
      return copy.status.event.taskHalted
    case 'node_started':
      return copy.status.event.nodeStarted
    case 'node_updated':
      return copy.status.event.nodeUpdated
    default:
      return copy.status.event.taskUpdated
  }
}

export function translateTaskType(type: string) {
  const normalized = type.trim().toLowerCase()
  return copy.taskType[normalized as keyof typeof copy.taskType] ?? type
}
