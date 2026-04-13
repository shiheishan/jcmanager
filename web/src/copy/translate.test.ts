import { describe, expect, it } from 'vitest'

import {
  translateNodeStatus,
  translateTaskEvent,
  translateTaskStatus,
  translateTaskType
} from './translate'

describe('translateNodeStatus', () => {
  it('maps known node statuses to Chinese labels', () => {
    expect(translateNodeStatus('pending_install', false)).toBe('待安装')
    expect(translateNodeStatus('unclaimed', false)).toBe('待认领')
    expect(translateNodeStatus('active', true)).toBe('在线')
    expect(translateNodeStatus('active', false)).toBe('离线')
  })
})

describe('translateTaskStatus', () => {
  it('maps known task statuses and preserves unknown ones', () => {
    expect(translateTaskStatus('queued')).toBe('排队中')
    expect(translateTaskStatus('running')).toBe('进行中')
    expect(translateTaskStatus('succeeded')).toBe('成功')
    expect(translateTaskStatus('failed')).toBe('失败')
    expect(translateTaskStatus('skipped')).toBe('已跳过')
    expect(translateTaskStatus('halted')).toBe('已停止')
    expect(translateTaskStatus('')).toBe('待处理')
    expect(translateTaskStatus('custom_status')).toBe('custom_status')
  })
})

describe('translateTaskEvent', () => {
  it('maps task event names and falls back to generic updates', () => {
    expect(translateTaskEvent('task_created')).toBe('任务已创建')
    expect(translateTaskEvent('task_complete')).toBe('任务已完成')
    expect(translateTaskEvent('task_halted')).toBe('任务已停止')
    expect(translateTaskEvent('node_started')).toBe('节点开始执行')
    expect(translateTaskEvent('node_updated')).toBe('节点状态更新')
    expect(translateTaskEvent('snapshot')).toBe('任务更新')
  })
})

describe('translateTaskType', () => {
  it('maps known task types and preserves unknown ones', () => {
    expect(translateTaskType('config_push')).toBe('配置下发')
    expect(translateTaskType('batch_config')).toBe('批量配置下发')
    expect(translateTaskType('other_type')).toBe('other_type')
  })
})
