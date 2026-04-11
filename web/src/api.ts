import type {
  BatchConfigRequest,
  ConfigPushRequest,
  ConfigTaskResponse,
  NodeConfigContentResponse,
  NodeConfigResponse,
  NodeDetailResponse,
  NodeSummaryResponse,
  TaskEventPayload
} from './types'

export interface ApiClientConfig {
  baseUrl: string
  token: string
}

export class ApiError extends Error {
  status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

function normalizeBaseUrl(baseUrl: string): string {
  const trimmed = baseUrl.trim()
  if (trimmed === '' || trimmed === '/') {
    return ''
  }
  return trimmed.replace(/\/+$/, '')
}

function buildUrl(config: ApiClientConfig, path: string): string {
  return `${normalizeBaseUrl(config.baseUrl)}${path}`
}

function buildHeaders(config: ApiClientConfig, extra: HeadersInit = {}): HeadersInit {
  return {
    Authorization: `Bearer ${config.token.trim()}`,
    ...extra
  }
}

async function readError(response: Response): Promise<never> {
  const raw = await response.text()
  let message = response.statusText || 'Request failed'

  if (raw) {
    try {
      const parsed = JSON.parse(raw) as { error?: string }
      message = parsed.error?.trim() || message
    } catch {
      message = raw
    }
  }

  throw new ApiError(message, response.status)
}

async function requestJson<T>(config: ApiClientConfig, path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(buildUrl(config, path), {
    ...init,
    headers: buildHeaders(config, {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {})
    })
  })

  if (!response.ok) {
    await readError(response)
  }

  return (await response.json()) as T
}

export function listNodes(config: ApiClientConfig): Promise<NodeSummaryResponse[]> {
  return requestJson<NodeSummaryResponse[]>(config, '/api/nodes')
}

export function getNode(config: ApiClientConfig, nodeId: string): Promise<NodeDetailResponse> {
  return requestJson<NodeDetailResponse>(config, `/api/nodes/${encodeURIComponent(nodeId)}`)
}

export function getNodeConfig(config: ApiClientConfig, nodeId: string): Promise<NodeConfigResponse> {
  return requestJson<NodeConfigResponse>(config, `/api/nodes/${encodeURIComponent(nodeId)}/config`)
}

export function getNodeConfigContent(
  config: ApiClientConfig,
  nodeId: string,
  path: string
): Promise<NodeConfigContentResponse> {
  const query = new URLSearchParams({ path })
  return requestJson<NodeConfigContentResponse>(
    config,
    `/api/nodes/${encodeURIComponent(nodeId)}/config/content?${query.toString()}`
  )
}

export function pushNodeConfig(
  config: ApiClientConfig,
  nodeId: string,
  payload: ConfigPushRequest
): Promise<ConfigTaskResponse> {
  return requestJson<ConfigTaskResponse>(config, `/api/nodes/${encodeURIComponent(nodeId)}/config`, {
    method: 'POST',
    body: JSON.stringify(payload)
  })
}

export function pushBatchConfig(
  config: ApiClientConfig,
  payload: BatchConfigRequest
): Promise<ConfigTaskResponse> {
  return requestJson<ConfigTaskResponse>(config, '/api/batch/config', {
    method: 'POST',
    body: JSON.stringify(payload)
  })
}

interface ParsedEvent {
  event: string
  data: TaskEventPayload | null
}

export function parseEventChunk(chunk: string): ParsedEvent | null {
  const lines = chunk
    .replace(/\r/g, '')
    .split('\n')
    .filter((line) => line !== '')

  if (lines.length === 0) {
    return null
  }

  let event = 'message'
  const dataLines: string[] = []

  for (const line of lines) {
    if (line.startsWith(':')) {
      continue
    }
    if (line.startsWith('event:')) {
      event = line.slice('event:'.length).trim()
      continue
    }
    if (line.startsWith('data:')) {
      dataLines.push(line.slice('data:'.length).trim())
    }
  }

  const joined = dataLines.join('\n')
  if (!joined) {
    return { event, data: null }
  }

  return {
    event,
    data: JSON.parse(joined) as TaskEventPayload
  }
}

export async function streamTaskEvents(
  config: ApiClientConfig,
  taskId: string,
  onEvent: (event: TaskEventPayload) => void,
  signal?: AbortSignal
): Promise<void> {
  const response = await fetch(buildUrl(config, `/api/events?task_id=${encodeURIComponent(taskId)}`), {
    headers: buildHeaders(config, {
      Accept: 'text/event-stream'
    }),
    signal
  })

  if (!response.ok) {
    await readError(response)
  }
  if (!response.body) {
    throw new ApiError('Task stream is unavailable', 500)
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''

  while (true) {
    const { value, done } = await reader.read()
    if (done) {
      break
    }

    buffer += decoder.decode(value, { stream: true })
    buffer = buffer.replace(/\r\n/g, '\n')

    let boundary = buffer.indexOf('\n\n')
    while (boundary >= 0) {
      const chunk = buffer.slice(0, boundary)
      buffer = buffer.slice(boundary + 2)

      const parsed = parseEventChunk(chunk)
      if (parsed?.data) {
        onEvent(parsed.data)
      }

      boundary = buffer.indexOf('\n\n')
    }
  }

  const finalChunk = decoder.decode()
  if (finalChunk) {
    buffer += finalChunk
  }
  const parsed = parseEventChunk(buffer)
  if (parsed?.data) {
    onEvent(parsed.data)
  }
}
