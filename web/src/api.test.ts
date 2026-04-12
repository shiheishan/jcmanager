import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  claimNode,
  createNode,
  getInstallCommand,
  parseEventChunk,
  pushBatchConfig,
  streamTaskEvents,
  type ApiClientConfig
} from './api'

const apiConfig: ApiClientConfig = {
  baseUrl: 'http://127.0.0.1:8080',
  token: 'secret-token'
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('parseEventChunk', () => {
  it('parses SSE event payloads with comments and multiline data', () => {
    const parsed = parseEventChunk(
      [
        ': ping',
        'event: node_update',
        'data: {"event":"node_update",',
        'data: "time":"2026-04-11T10:00:00Z",',
        'data: "message":"ok"}'
      ].join('\n')
    )

    expect(parsed).toEqual({
      event: 'node_update',
      data: {
        event: 'node_update',
        time: '2026-04-11T10:00:00Z',
        message: 'ok'
      }
    })
  })
})

describe('pushBatchConfig', () => {
  it('sends auth headers and JSON payloads to the batch endpoint', async () => {
    const fetchMock = vi.fn(async () =>
      new Response(
        JSON.stringify({
          id: 'task-1',
          type: 'batch_config',
          status: 'queued',
          path: '/etc/xrayr/config.yml',
          service_name: 'xrayr',
          create_backup: true,
          restart_after_write: true,
          max_concurrency: 20,
          canary_mode: true,
          canary_size: 2,
          created_at: '2026-04-11T10:00:00Z',
          updated_at: '2026-04-11T10:00:00Z',
          total_nodes: 2,
          pending_nodes: 2,
          in_flight_nodes: 0,
          succeeded_nodes: 0,
          failed_nodes: 0,
          skipped_nodes: 0,
          nodes: []
        }),
        {
          status: 202,
          headers: {
            'Content-Type': 'application/json'
          }
        }
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    await pushBatchConfig(apiConfig, {
      node_ids: ['node-1', 'node-2'],
      path: '/etc/xrayr/config.yml',
      content: 'new-config',
      service_name: 'xrayr',
      create_backup: true,
      restart_after_write: true,
      canary_mode: true
    })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const call = fetchMock.mock.calls[0] as unknown as [string, RequestInit] | undefined
    if (!call) {
      throw new Error('fetch was not called')
    }
    const url = call[0]
    const init = call[1]
    expect(url).toBe('http://127.0.0.1:8080/api/batch/config')
    expect(init.method).toBe('POST')
    expect(init.headers).toMatchObject({
      Authorization: 'Bearer secret-token',
      'Content-Type': 'application/json'
    })
    expect(init.body).toBe(
      JSON.stringify({
        node_ids: ['node-1', 'node-2'],
        path: '/etc/xrayr/config.yml',
        content: 'new-config',
        service_name: 'xrayr',
        create_backup: true,
        restart_after_write: true,
        canary_mode: true
      })
    )
  })
})

describe('createNode', () => {
  it('posts the display name to the create endpoint', async () => {
    const fetchMock = vi.fn(async () =>
      new Response(
        JSON.stringify({
          id: 'node-1',
          display_name: 'HK-01',
          install_secret: 'secret',
          install_command: 'curl -fsSL http://127.0.0.1:8080/install.sh?secret=secret | bash',
          status: 'pending_install',
          expires_at: '2026-04-18T10:00:00Z'
        }),
        {
          status: 200,
          headers: {
            'Content-Type': 'application/json'
          }
        }
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    await createNode(apiConfig, {
      display_name: 'HK-01'
    })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const call = fetchMock.mock.calls[0] as unknown as [string, RequestInit] | undefined
    if (!call) {
      throw new Error('fetch was not called')
    }
    const url = call[0]
    const init = call[1]
    expect(url).toBe('http://127.0.0.1:8080/api/nodes/create')
    expect(init.method).toBe('POST')
    expect(init.body).toBe(JSON.stringify({ display_name: 'HK-01' }))
  })
})

describe('claimNode', () => {
  it('posts to the claim endpoint', async () => {
    const fetchMock = vi.fn(async () =>
      new Response(JSON.stringify({ id: 'node-1', status: 'active' }), {
        status: 200,
        headers: {
          'Content-Type': 'application/json'
        }
      })
    )
    vi.stubGlobal('fetch', fetchMock)

    await claimNode(apiConfig, 'node-1', {
      display_name: 'HK-01'
    })

    const call = fetchMock.mock.calls[0] as unknown as [string, RequestInit] | undefined
    if (!call) {
      throw new Error('fetch was not called')
    }
    expect(call[0]).toBe('http://127.0.0.1:8080/api/nodes/node-1/claim')
    expect(call[1].method).toBe('POST')
    expect(call[1].body).toBe(JSON.stringify({ display_name: 'HK-01' }))
  })
})

describe('getInstallCommand', () => {
  it('loads the authenticated universal install command', async () => {
    const fetchMock = vi.fn(async () =>
      new Response(JSON.stringify({ install_command: 'curl -fsSL https://panel/install.sh | bash -s -- --token secret' }), {
        status: 200,
        headers: {
          'Content-Type': 'application/json'
        }
      })
    )
    vi.stubGlobal('fetch', fetchMock)

    const response = await getInstallCommand(apiConfig)

    expect(response.install_command).toContain('/install.sh')
    const call = fetchMock.mock.calls[0] as unknown as [string, RequestInit] | undefined
    if (!call) {
      throw new Error('fetch was not called')
    }
    expect(call[0]).toBe('http://127.0.0.1:8080/api/install-command')
  })
})

describe('streamTaskEvents', () => {
  it('emits parsed task events from an SSE response body', async () => {
    const fetchMock = vi.fn(async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            controller.enqueue(
              new TextEncoder().encode(
                [
                  'event: snapshot',
                  'data: {"event":"snapshot","time":"2026-04-11T10:00:00Z","message":"queued"}',
                  '',
                  'event: task_complete',
                  'data: {"event":"task_complete","time":"2026-04-11T10:00:05Z","message":"done"}',
                  ''
                ].join('\n')
              )
            )
            controller.close()
          }
        }),
        {
          status: 200,
          headers: {
            'Content-Type': 'text/event-stream'
          }
        }
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    const seen: string[] = []
    await streamTaskEvents(apiConfig, 'task-1', (event) => {
      seen.push(`${event.event}:${event.message}`)
    })

    expect(seen).toEqual(['snapshot:queued', 'task_complete:done'])
    const call = fetchMock.mock.calls[0] as unknown as [string, RequestInit] | undefined
    if (!call) {
      throw new Error('fetch was not called')
    }
    const url = call[0]
    const init = call[1]
    expect(url).toBe('http://127.0.0.1:8080/api/events?task_id=task-1')
    expect(init.headers).toMatchObject({
      Authorization: 'Bearer secret-token',
      Accept: 'text/event-stream'
    })
  })
})
