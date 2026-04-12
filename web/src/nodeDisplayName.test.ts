import { describe, expect, it } from 'vitest'

import { suggestNextNodeDisplayName } from './nodeDisplayName'
import type { NodeSummaryResponse } from './types'

function makeNode(displayName: string): NodeSummaryResponse {
  return {
    id: displayName.toLowerCase(),
    hostname: displayName.toLowerCase(),
    display_name: displayName,
    status: 'pending_install',
    primary_ip: '',
    os: '',
    arch: '',
    agent_version: '',
    registered_at: '',
    last_register_at: '',
    last_heartbeat_at: '',
    last_agent_time_unix: 0,
    online: false,
    cpu_percent: 0,
    memory_used_bytes: 0,
    memory_total_bytes: 0,
    disk_used_bytes: 0,
    disk_total_bytes: 0,
    load_1: 0,
    load_5: 0,
    load_15: 0,
    config_error: '',
    service_flavors: [],
    services: []
  }
}

describe('suggestNextNodeDisplayName', () => {
  it('returns HK-01 when there are no existing nodes', () => {
    expect(suggestNextNodeDisplayName([])).toBe('HK-01')
  })

  it('increments the largest HK suffix from existing nodes', () => {
    const nodes = [makeNode('edge-a'), makeNode('HK-01'), makeNode('HK-09')]

    expect(suggestNextNodeDisplayName(nodes)).toBe('HK-10')
  })
})
