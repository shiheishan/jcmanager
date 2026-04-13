<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NConfigProvider,
  NDataTable,
  NDescriptions,
  NDescriptionsItem,
  NEmpty,
  NForm,
  NFormItem,
  NGradientText,
  NInput,
  NModal,
  NProgress,
  NSelect,
  NSpace,
  NSpin,
  NSwitch,
  NTag,
  NThing,
  createDiscreteApi,
  type DataTableColumns,
  type GlobalThemeOverrides
} from 'naive-ui'
import {
  ApiError,
  claimNode,
  createNode,
  getInstallCommand,
  getNode,
  getNodeConfig,
  getNodeConfigContent,
  listNodes,
  pushBatchConfig,
  pushNodeConfig
} from './api'
import { useTaskStream } from './composables/useTaskStream'
import StructuredValueEditor from './components/StructuredValueEditor.vue'
import { parseStructuredContent, serializeConfigContent } from './config-format'
import { zhCN } from './copy/zh-CN'
import {
  translateNodeStatus,
  translateTaskEvent,
  translateTaskStatus,
  translateTaskType
} from './copy/translate'
import { suggestNextNodeDisplayName } from './nodeDisplayName'
import type {
  ConfigPushRequest,
  ConfigTaskResponse,
  CreateNodeResponse,
  InstallCommandResponse,
  NodeConfigContentResponse,
  NodeConfigResponse,
  NodeDetailResponse,
  NodeSummaryResponse,
  TaskEventPayload,
  TaskNodeResponse
} from './types'

const storageKeys = {
  apiBaseUrl: 'jcmanager.web.apiBaseUrl',
  apiToken: 'jcmanager.web.apiToken'
}

const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#0f766e',
    primaryColorHover: '#0d9488',
    primaryColorPressed: '#115e59',
    successColor: '#15803d',
    errorColor: '#dc2626',
    borderRadius: '18px',
    fontFamily: '"Avenir Next", "Segoe UI Variable", "Helvetica Neue", sans-serif'
  },
  Card: {
    borderRadius: '24px'
  },
  DataTable: {
    thColor: 'rgba(255, 255, 255, 0.9)',
    tdColor: 'rgba(255, 255, 255, 0.74)',
    borderColor: 'rgba(148, 163, 184, 0.18)'
  },
  Input: {
    borderRadius: '16px'
  }
}

const { message } = createDiscreteApi(['message'])
const copy = zhCN

const apiBaseUrl = ref(readStoredValue(storageKeys.apiBaseUrl, import.meta.env.VITE_API_BASE_URL ?? ''))
const apiToken = ref(
  readStoredValue(storageKeys.apiToken, import.meta.env.VITE_API_TOKEN ?? '', 'session')
)

const nodes = ref<NodeSummaryResponse[]>([])
const loadingNodes = ref(false)
const nodesError = ref('')

const activeNodeId = ref<string | null>(null)
const activeNodeDetail = ref<NodeDetailResponse | null>(null)
const activeNodeConfig = ref<NodeConfigResponse | null>(null)
const loadingNodeDetail = ref(false)
const nodeDetailError = ref('')
const addNodeModalOpen = ref(false)
const addNodeDisplayName = ref('')
const creatingNode = ref(false)
const createdNode = ref<CreateNodeResponse | null>(null)
const universalInstallCommand = ref('')
const loadingInstallCommand = ref(false)

const selectedNodeIds = ref<string[]>([])
const submitting = ref(false)

const form = reactive({
  path: '',
  service_name: '',
  create_backup: true,
  restart_after_write: true,
  canary_mode: false
})

const configContent = ref<NodeConfigContentResponse | null>(null)
const loadingConfigContent = ref(false)
const configContentError = ref('')
const rawEditorContent = ref('')
const structuredEditorValue = ref<unknown>(null)
const editorMode = ref<'structured' | 'raw'>('raw')
let configContentRequestID = 0

const currentTask = ref<ConfigTaskResponse | null>(null)
const taskEvents = ref<TaskEventPayload[]>([])
const taskStreamError = ref('')
const taskEventLimit = 80

const heartbeatTick = ref(Date.now())
let heartbeatTimer: number | undefined
let nodeRefreshTimer: number | undefined

const apiConfig = computed(() => ({
  baseUrl: apiBaseUrl.value.trim(),
  token: apiToken.value.trim()
}))

watch(apiBaseUrl, (value) => {
  storeValue(storageKeys.apiBaseUrl, value)
})

watch(apiToken, (value) => {
  storeValue(storageKeys.apiToken, value, 'session')
  if (!value.trim()) {
    universalInstallCommand.value = ''
  }
})

const activeNodeSummary = computed(() =>
  nodes.value.find((node) => node.id === activeNodeId.value) ?? null
)

const selectedCount = computed(() => selectedNodeIds.value.length)
const connected = computed(() => apiConfig.value.token.length > 0)
const visibleNodes = computed(() => nodes.value.filter((node) => node.status !== 'unclaimed'))
const unclaimedNodes = computed(() => nodes.value.filter((node) => node.status === 'unclaimed'))
const nextAddNodeDisplayName = computed(() => suggestNextNodeDisplayName(nodes.value))

const nodeCountSummary = computed(() => {
  const total = nodes.value.length
  const online = nodes.value.filter((node) => node.online).length
  return {
    total,
    online,
    offline: total - online,
    unclaimed: unclaimedNodes.value.length
  }
})

const pathSuggestions = computed(() => activeNodeConfig.value?.allowed_paths ?? [])
const configPathSuggestions = computed(() => activeNodeConfig.value?.config_paths ?? [])
const pathOptions = computed(() =>
  dedupeStrings([...configPathSuggestions.value, ...pathSuggestions.value]).map((value) => ({
    label: value,
    value
  }))
)
const serviceSuggestions = computed(() => {
  const values = new Set<string>()
  for (const flavor of activeNodeConfig.value?.service_flavors ?? []) {
    if (flavor.trim()) {
      values.add(flavor.trim())
    }
  }
  for (const service of activeNodeDetail.value?.services ?? []) {
    if (service.name.trim()) {
      values.add(service.name.trim())
    }
  }
  return Array.from(values)
})

const taskCompletionPercent = computed(() => {
  const task = currentTask.value
  if (!task || task.total_nodes === 0) {
    return 0
  }
  const completed = task.succeeded_nodes + task.failed_nodes + task.skipped_nodes
  return Math.round((completed / task.total_nodes) * 100)
})

watch(
  [activeNodeId, () => form.path],
  ([nodeId, path]) => {
    if (!nodeId || !path.trim()) {
      configContent.value = null
      rawEditorContent.value = ''
      structuredEditorValue.value = null
      configContentError.value = ''
      return
    }
    void loadConfigContent(nodeId, path)
  }
)

const nodeColumns = computed<DataTableColumns<NodeSummaryResponse>>(() => [
  {
    type: 'selection'
  },
  {
    title: copy.nodes.columns.hostname,
    key: 'hostname',
    minWidth: 220,
    render: (row) =>
      h('div', { class: 'host-cell' }, [
        h('div', { class: 'host-primary' }, row.hostname || row.display_name || row.id),
        h(
          'div',
          { class: 'host-secondary' },
          row.display_name && row.display_name !== row.hostname ? row.display_name : row.id
        )
      ])
  },
  {
    title: copy.nodes.columns.ip,
    key: 'primary_ip',
    minWidth: 140,
    render: (row) => h('span', { class: 'mono-text' }, row.primary_ip || copy.common.notAvailable)
  },
  {
    title: copy.nodes.columns.protocol,
    key: 'protocol',
    minWidth: 170,
    render: (row) => renderProtocolTags(row)
  },
  {
    title: copy.nodes.columns.status,
    key: 'online',
    width: 110,
    render: (row) => {
      const statusLabel = translateNodeStatus(row.status, row.online)
      const statusType =
        row.status === 'pending_install'
          ? 'warning'
          : row.status === 'unclaimed'
            ? 'info'
            : row.online
              ? 'success'
              : 'error'

      return h(
        NTag,
        {
          type: statusType,
          round: true,
          bordered: false
        },
        {
          default: () => statusLabel
        }
      )
    }
  },
  {
    title: copy.nodes.columns.lastHeartbeat,
    key: 'last_heartbeat_at',
    minWidth: 180,
    render: (row) =>
      h('div', { class: 'heartbeat-cell' }, [
        h('div', null, formatRelativeTime(row.last_heartbeat_at, heartbeatTick.value)),
        h('div', { class: 'host-secondary' }, formatDateTime(row.last_heartbeat_at))
      ])
  }
])

const taskNodeColumns = computed<DataTableColumns<TaskNodeResponse>>(() => [
  {
    title: copy.progress.columns.node,
    key: 'node_id',
    minWidth: 150,
    render: (row) => h('span', { class: 'mono-text' }, row.node_id)
  },
  {
    title: copy.progress.columns.status,
    key: 'status',
    width: 120,
    render: (row) => renderTaskStatus(row.status)
  },
  {
    title: copy.progress.columns.changed,
    key: 'changed',
    width: 100,
    render: (row) =>
      h(
        NTag,
        {
          type: row.changed ? 'warning' : 'default',
          round: true,
          bordered: false
        },
        {
          default: () => (row.changed ? copy.progress.changed.yes : copy.progress.changed.no)
        }
      )
  },
  {
    title: copy.progress.columns.message,
    key: 'message',
    minWidth: 280,
    ellipsis: {
      tooltip: true
    }
  },
  {
    title: copy.progress.columns.updated,
    key: 'updated_at',
    width: 180,
    render: (row) => formatDateTime(row.updated_at)
  }
])

const { currentTaskId, streaming, start: startTaskStream, stop: stopTaskStream } = useTaskStream(
  () => apiConfig.value,
  handleTaskEvent,
  handleTaskStreamFailure
)

onMounted(() => {
  heartbeatTimer = window.setInterval(() => {
    heartbeatTick.value = Date.now()
  }, 30_000)

  nodeRefreshTimer = window.setInterval(() => {
    if (connected.value) {
      void loadNodes(false)
    }
  }, 15_000)

  if (connected.value) {
    void loadNodes(true)
  }
})

onBeforeUnmount(() => {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer)
  }
  if (nodeRefreshTimer) {
    clearInterval(nodeRefreshTimer)
  }
  stopTaskStream()
})

async function loadNodes(showToast = false) {
  if (!connected.value) {
    nodesError.value = copy.errors.loadNodesNeedsToken
    return
  }

  loadingNodes.value = true
  nodesError.value = ''

  try {
    nodes.value = await listNodes(apiConfig.value)
    await loadInstallCommand()
    if (showToast) {
      message.success(copy.messages.loadedNodes(nodes.value.length))
    }

    if (activeNodeId.value && !nodes.value.some((node) => node.id === activeNodeId.value)) {
      resetActiveNode()
    }
  } catch (error) {
    nodesError.value = toErrorMessage(error)
    message.error(nodesError.value)
  } finally {
    loadingNodes.value = false
  }
}

async function loadInstallCommand() {
  loadingInstallCommand.value = true
  try {
    const response: InstallCommandResponse = await getInstallCommand(apiConfig.value)
    universalInstallCommand.value = response.install_command
  } catch (error) {
    universalInstallCommand.value = ''
    throw error
  } finally {
    loadingInstallCommand.value = false
  }
}

function openAddNodeModal() {
  addNodeDisplayName.value = nextAddNodeDisplayName.value
  createdNode.value = null
  addNodeModalOpen.value = true
}

function closeAddNodeModal() {
  addNodeModalOpen.value = false
}

async function submitCreateNode() {
  if (!connected.value) {
    message.error(copy.errors.createNodeNeedsToken)
    return
  }
  if (!addNodeDisplayName.value.trim()) {
    message.error(copy.errors.displayNameRequired)
    return
  }

  creatingNode.value = true
  try {
    createdNode.value = await createNode(apiConfig.value, {
      display_name: addNodeDisplayName.value.trim()
    })
    await loadNodes(false)
    message.success(copy.messages.createdInstallCommand(createdNode.value.display_name))
  } catch (error) {
    message.error(toErrorMessage(error))
  } finally {
    creatingNode.value = false
  }
}

async function claimUnclaimedNode(node: NodeSummaryResponse) {
  if (!connected.value) {
    message.error(copy.errors.claimNeedsToken)
    return
  }

  try {
    const claimed = await claimNode(apiConfig.value, node.id, {
      display_name: node.display_name || node.hostname || node.id
    })
    await loadNodes(false)
    message.success(copy.messages.claimedNode(claimed.display_name || claimed.hostname || claimed.id))
    await openNode(claimed)
  } catch (error) {
    message.error(toErrorMessage(error))
  }
}

async function copyToClipboard(value: string, successMessage: string) {
  if (typeof navigator === 'undefined' || !navigator.clipboard) {
    message.error(copy.errors.clipboardUnavailable)
    return
  }
  try {
    await navigator.clipboard.writeText(value)
    message.success(successMessage)
  } catch {
    message.error(copy.errors.clipboardFailed)
  }
}

async function openNode(node: NodeSummaryResponse) {
  if (!connected.value) {
    message.error(copy.errors.loadNodeNeedsToken)
    return
  }

  activeNodeId.value = node.id
  loadingNodeDetail.value = true
  nodeDetailError.value = ''

  try {
    const [detail, config] = await Promise.all([
      getNode(apiConfig.value, node.id),
      getNodeConfig(apiConfig.value, node.id)
    ])

    if (activeNodeId.value !== node.id) {
      return
    }

    activeNodeDetail.value = detail
    activeNodeConfig.value = config
    seedFormFromNode(detail, config)
  } catch (error) {
    nodeDetailError.value = toErrorMessage(error)
    message.error(nodeDetailError.value)
  } finally {
    if (activeNodeId.value === node.id) {
      loadingNodeDetail.value = false
    }
  }
}

function resetActiveNode() {
  configContentRequestID += 1
  activeNodeId.value = null
  activeNodeDetail.value = null
  activeNodeConfig.value = null
  configContent.value = null
  rawEditorContent.value = ''
  structuredEditorValue.value = null
  loadingNodeDetail.value = false
  nodeDetailError.value = ''
  configContentError.value = ''
}

function seedFormFromNode(detail: NodeDetailResponse, config: NodeConfigResponse) {
  const preferredPath = config.config_paths[0] ?? config.allowed_paths[0] ?? ''
  if (preferredPath && form.path !== preferredPath) {
    form.path = preferredPath
  }

  if (form.service_name.trim() === '' || !serviceSuggestions.value.includes(form.service_name)) {
    form.service_name =
      detail.services.find((service) => service.name.trim())?.name ??
      config.service_flavors[0] ??
      ''
  }
}

async function loadConfigContent(nodeId: string, path: string) {
  const requestID = ++configContentRequestID
  loadingConfigContent.value = true
  configContentError.value = ''

  try {
    const response = await getNodeConfigContent(apiConfig.value, nodeId, path)
    if (requestID !== configContentRequestID || activeNodeId.value !== nodeId || form.path !== path) {
      return
    }

    configContent.value = response
    rawEditorContent.value = response.raw_content
    structuredEditorValue.value = response.structured_content ?? null
    editorMode.value = response.structured_content != null ? 'structured' : 'raw'
  } catch (error) {
    if (requestID !== configContentRequestID) {
      return
    }
    configContent.value = null
    structuredEditorValue.value = null
    rawEditorContent.value = ''
    editorMode.value = 'raw'
    configContentError.value = toErrorMessage(error)
  } finally {
    if (requestID === configContentRequestID) {
      loadingConfigContent.value = false
    }
  }
}

function validateForm(): string | null {
  if (!form.path.trim()) {
    return copy.errors.configPathRequired
  }
  if (form.restart_after_write && !form.service_name.trim()) {
    return copy.errors.serviceNameRequired
  }
  return null
}

function syncEditorMode(nextMode: 'structured' | 'raw') {
  if (nextMode === editorMode.value) {
    return
  }

  if (nextMode === 'raw') {
    rawEditorContent.value = buildConfigContent()
    editorMode.value = 'raw'
    return
  }

  try {
    structuredEditorValue.value = parseStructuredContent(
      configContent.value?.format ?? 'text',
      rawEditorContent.value
    )
    configContentError.value = ''
    editorMode.value = 'structured'
  } catch (error) {
    message.error(toErrorMessage(error))
  }
}

function buildConfigContent(): string {
  if (editorMode.value === 'structured' && structuredEditorValue.value != null && configContent.value) {
    return serializeConfigContent(
      configContent.value.format,
      structuredEditorValue.value,
      rawEditorContent.value
    )
  }
  return rawEditorContent.value
}

function makePayload(): ConfigPushRequest {
  return {
    path: form.path.trim(),
    content: buildConfigContent(),
    service_name: form.service_name.trim(),
    create_backup: form.create_backup,
    restart_after_write: form.restart_after_write
  }
}

async function applyToActiveNode() {
  if (!activeNodeId.value) {
    message.error(copy.errors.selectNodeFirst)
    return
  }

  const validationError = validateForm()
  if (validationError) {
    message.error(validationError)
    return
  }

  submitting.value = true
  try {
    const task = await pushNodeConfig(apiConfig.value, activeNodeId.value, makePayload())
    attachTask(
      task,
      copy.messages.queuedSingleNode(activeNodeSummary.value?.hostname || activeNodeId.value)
    )
  } catch (error) {
    message.error(toErrorMessage(error))
  } finally {
    submitting.value = false
  }
}

async function applyToSelection() {
  if (selectedNodeIds.value.length === 0) {
    message.error(copy.errors.selectBatchNodes)
    return
  }

  const validationError = validateForm()
  if (validationError) {
    message.error(validationError)
    return
  }

  submitting.value = true
  try {
    const task = await pushBatchConfig(apiConfig.value, {
      ...makePayload(),
      node_ids: selectedNodeIds.value,
      canary_mode: form.canary_mode
    })
    attachTask(task, copy.messages.queuedBatchNodes(selectedNodeIds.value.length))
  } catch (error) {
    message.error(toErrorMessage(error))
  } finally {
    submitting.value = false
  }
}

function attachTask(task: ConfigTaskResponse, infoMessage: string) {
  currentTask.value = task
  taskEvents.value = [
    {
      event: 'task_created',
      task_id: task.id,
      time: new Date().toISOString(),
      message: infoMessage,
      task
    }
  ]
  taskStreamError.value = ''
  void startTaskStream(task.id)
  message.success(infoMessage)
}

function handleTaskEvent(event: TaskEventPayload) {
  taskEvents.value = [event, ...taskEvents.value].slice(0, taskEventLimit)
  if (event.task) {
    currentTask.value = event.task
  }

  if (event.event === 'task_complete' || event.event === 'task_halted') {
    stopTaskStream()
    void loadNodes(false)
  }
}

function handleTaskStreamFailure(error: unknown) {
  taskStreamError.value = toErrorMessage(error)
}

function clearSelection() {
  selectedNodeIds.value = []
}

function updateSelection(keys: Array<string | number>) {
  selectedNodeIds.value = keys.map(String)
}

function renderProtocolTags(row: NodeSummaryResponse) {
  const protocols = deriveProtocols(row)
  if (protocols.length === 0) {
    return h(
      NTag,
      {
        bordered: false,
        round: true
      },
      {
        default: () => copy.common.unknown
      }
    )
  }

  return h(
    NSpace,
    {
      size: [6, 6]
    },
    {
      default: () =>
        protocols.slice(0, 3).map((protocol) =>
          h(
            NTag,
            {
              key: `${row.id}-${protocol}`,
              bordered: false,
              round: true,
              type: 'info'
            },
            {
              default: () => protocol
            }
          )
        )
    }
  )
}

function renderTaskStatus(status: string) {
  const normalized = status.trim().toLowerCase()
  const tagType =
    normalized === 'succeeded'
      ? 'success'
      : normalized === 'failed'
        ? 'error'
        : normalized === 'queued' || normalized === 'running'
          ? 'warning'
          : 'default'

  return h(
    NTag,
    {
      type: tagType,
      bordered: false,
      round: true
    },
    {
      default: () => translateTaskStatus(normalized)
    }
  )
}

function deriveProtocols(node: NodeSummaryResponse): string[] {
  const values = node.service_flavors.filter(Boolean)
  if (values.length > 0) {
    return values
  }
  return Array.from(new Set(node.services.map((service) => service.name).filter(Boolean)))
}

function formatDateTime(value?: string) {
  if (!value) {
    return copy.relativeTime.never
  }

  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }

  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'short'
  }).format(date)
}

function formatRelativeTime(value: string | undefined, nowMs: number) {
  if (!value) {
    return copy.relativeTime.noHeartbeat
  }

  const date = new Date(value)
  const deltaMs = nowMs - date.getTime()
  if (Number.isNaN(deltaMs)) {
    return value
  }

  const deltaMinutes = Math.round(deltaMs / 60_000)
  if (deltaMinutes <= 0) {
    return copy.relativeTime.justNow
  }
  if (deltaMinutes < 60) {
    return copy.relativeTime.minutesAgo(deltaMinutes)
  }

  const deltaHours = Math.round(deltaMinutes / 60)
  if (deltaHours < 24) {
    return copy.relativeTime.hoursAgo(deltaHours)
  }

  const deltaDays = Math.round(deltaHours / 24)
  return copy.relativeTime.daysAgo(deltaDays)
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return '0 B'
  }

  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let current = value
  let unitIndex = 0
  while (current >= 1024 && unitIndex < units.length - 1) {
    current /= 1024
    unitIndex += 1
  }

  const digits = current >= 10 || unitIndex === 0 ? 0 : 1
  return `${current.toFixed(digits)} ${units[unitIndex]}`
}

function dedupeStrings(values: string[]) {
  return Array.from(new Set(values.map((value) => value.trim()).filter(Boolean)))
}

function readStoredValue(key: string, fallback: string, storage: 'local' | 'session' = 'local'): string {
  const selectedStorage =
    storage === 'session' ? globalThis.sessionStorage : globalThis.localStorage
  if (typeof selectedStorage === 'undefined') {
    return fallback
  }

  const stored = selectedStorage.getItem(key)
  if (stored && stored.trim() !== '') {
    return stored
  }
  return fallback
}

function storeValue(key: string, value: string, storage: 'local' | 'session' = 'local') {
  const selectedStorage =
    storage === 'session' ? globalThis.sessionStorage : globalThis.localStorage
  if (typeof selectedStorage === 'undefined') {
    return
  }
  selectedStorage.setItem(key, value)
}

function toErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    return error.message
  }
  if (error instanceof Error) {
    return error.message
  }
  return copy.common.unexpectedError
}
</script>

<template>
  <n-config-provider :theme-overrides="themeOverrides">
    <main class="console-shell">
      <section class="hero-panel">
        <div>
          <p class="eyebrow">{{ copy.hero.eyebrow }}</p>
          <h1 class="hero-title">
            <n-gradient-text type="success">JCManager</n-gradient-text>
            {{ copy.hero.titleSuffix }}
          </h1>
          <p class="hero-copy">
            {{ copy.hero.copy }}
          </p>
        </div>
        <div class="hero-stats">
          <div class="stat-pill">
            <span class="stat-label">{{ copy.hero.stats.nodes }}</span>
            <strong>{{ nodeCountSummary.total }}</strong>
          </div>
          <div class="stat-pill">
            <span class="stat-label">{{ copy.hero.stats.online }}</span>
            <strong>{{ nodeCountSummary.online }}</strong>
          </div>
          <div class="stat-pill">
            <span class="stat-label">{{ copy.hero.stats.selected }}</span>
            <strong>{{ selectedCount }}</strong>
          </div>
          <div class="stat-pill">
            <span class="stat-label">{{ copy.hero.stats.unclaimed }}</span>
            <strong>{{ nodeCountSummary.unclaimed }}</strong>
          </div>
        </div>
      </section>

      <section class="connection-panel">
        <n-card :bordered="false" class="glass-card">
          <div class="connection-grid">
            <n-form label-placement="top" class="connection-form">
              <n-form-item :label="copy.connection.apiBaseUrlLabel">
                <n-input
                  v-model:value="apiBaseUrl"
                  :placeholder="copy.connection.apiBaseUrlPlaceholder"
                />
              </n-form-item>
              <n-form-item :label="copy.connection.tokenLabel">
                <n-input
                  v-model:value="apiToken"
                  type="password"
                  show-password-on="click"
                  placeholder="JCMANAGER_API_TOKEN"
                />
              </n-form-item>
            </n-form>
            <div class="connection-actions">
              <div class="connection-hint">
                <strong>{{ copy.connection.target }}:</strong>
                <span>{{ apiBaseUrl.trim() || copy.common.sameOriginProxy }}</span>
              </div>
              <div class="connection-hint">
                <strong>{{ copy.connection.tokenStorage }}:</strong>
                <span>{{ copy.connection.tokenStorageValue }}</span>
              </div>
              <div class="install-command-panel">
                <div class="install-command-panel__head">
                  <strong>{{ copy.connection.installCommand }}</strong>
                  <n-button
                    secondary
                    size="small"
                    :disabled="!universalInstallCommand || loadingInstallCommand"
                    @click="copyToClipboard(universalInstallCommand, copy.messages.copiedUniversalInstallCommand)"
                  >
                    {{ copy.common.copy }}
                  </n-button>
                </div>
                <n-input
                  :value="universalInstallCommand"
                  readonly
                  type="textarea"
                  :autosize="{ minRows: 2, maxRows: 4 }"
                  :placeholder="copy.connection.installCommandPlaceholder"
                />
              </div>
              <n-space>
                <n-button type="primary" :loading="loadingNodes" @click="loadNodes(true)">
                  {{ copy.connection.connect }}
                </n-button>
                <n-button secondary :disabled="!connected" @click="clearSelection">
                  {{ copy.connection.clearSelection }}
                </n-button>
              </n-space>
            </div>
          </div>

          <n-alert v-if="nodesError" :title="copy.connection.nodeListError" type="error" class="section-alert">
            {{ nodesError }}
          </n-alert>
        </n-card>
      </section>

      <section class="workspace-grid">
        <n-card :bordered="false" class="glass-card table-card">
          <template #header>
            <div class="card-head">
              <div>
                <h2 class="section-title">{{ copy.nodes.title }}</h2>
                <p class="section-subtitle">{{ copy.nodes.subtitle }}</p>
              </div>
              <n-space align="center">
                <n-tag round :bordered="false" type="info">
                  {{ copy.nodes.selectedTag(selectedCount) }}
                </n-tag>
                <n-button secondary :disabled="!connected" @click="openAddNodeModal">
                  {{ copy.nodes.addNode }}
                </n-button>
                <n-button
                  type="primary"
                  secondary
                  :disabled="selectedCount === 0 || submitting"
                  :loading="submitting"
                  @click="applyToSelection"
                >
                  {{ copy.nodes.applyToSelected }}
                </n-button>
              </n-space>
            </div>
          </template>

          <n-spin :show="loadingNodes">
            <n-data-table
              remote
              max-height="680"
              :columns="nodeColumns"
              :data="visibleNodes"
              :checked-row-keys="selectedNodeIds"
              :row-key="(row: NodeSummaryResponse) => row.id"
              :row-props="
                (row: NodeSummaryResponse) => ({
                  class: row.id === activeNodeId ? 'node-row node-row--active' : 'node-row',
                  onClick: () => openNode(row)
                })
              "
              @update:checked-row-keys="updateSelection"
            />
          </n-spin>
        </n-card>

        <div class="side-stack">
          <n-card :bordered="false" class="glass-card unclaimed-card">
            <template #header>
              <div class="card-head">
                <div>
                  <h2 class="section-title">{{ copy.unclaimed.title }}</h2>
                  <p class="section-subtitle">{{ copy.unclaimed.subtitle }}</p>
                </div>
                <n-tag round :bordered="false" type="warning">
                  {{ unclaimedNodes.length }}
                </n-tag>
              </div>
            </template>

            <n-empty
              v-if="unclaimedNodes.length === 0"
              :description="copy.unclaimed.empty"
              class="empty-state"
            />

            <div v-else class="unclaimed-list">
              <div
                v-for="node in unclaimedNodes"
                :key="node.id"
                class="unclaimed-item"
              >
                <button type="button" class="unclaimed-item__body" @click="openNode(node)">
                  <div class="unclaimed-item__title">
                    {{ node.display_name || node.hostname || node.id }}
                  </div>
                  <div class="unclaimed-item__meta">
                    {{ node.hostname || copy.unclaimed.unknownHost }} · {{ node.primary_ip || copy.unclaimed.noIpYet }}
                  </div>
                </button>
                <div class="unclaimed-item__actions">
                  <n-tag round :bordered="false" :type="node.online ? 'success' : 'warning'">
                    {{ node.online ? copy.unclaimed.online : copy.unclaimed.waiting }}
                  </n-tag>
                  <n-button size="small" type="primary" secondary @click="claimUnclaimedNode(node)">
                    {{ copy.unclaimed.claim }}
                  </n-button>
                </div>
              </div>
            </div>
          </n-card>

          <n-card :bordered="false" class="glass-card detail-card">
            <template #header>
              <div class="card-head">
                <div>
                  <h2 class="section-title">{{ copy.editor.title }}</h2>
                  <p class="section-subtitle">{{ copy.editor.subtitle }}</p>
                </div>
                <n-space>
                  <n-button
                    type="primary"
                    :disabled="!activeNodeId || submitting"
                    :loading="submitting"
                    @click="applyToActiveNode"
                  >
                    {{ copy.editor.applyToNode }}
                  </n-button>
                </n-space>
              </div>
            </template>

            <div v-if="loadingNodeDetail" class="empty-state">
              <n-spin size="large" />
            </div>

            <n-empty
              v-else-if="!activeNodeId"
              :description="copy.editor.empty"
              class="empty-state"
            />

            <div v-else class="detail-layout">
              <n-alert
                v-if="nodeDetailError"
                :title="copy.editor.nodeDetailError"
                type="error"
                class="section-alert"
              >
                {{ nodeDetailError }}
              </n-alert>

              <div v-if="activeNodeDetail && activeNodeConfig" class="node-summary-shell">
                <n-thing>
                  <template #header>
                    <div class="node-title-row">
                      <div>
                        <div class="node-title">
                          {{ activeNodeDetail.hostname || activeNodeDetail.display_name }}
                        </div>
                        <div class="node-subtitle">
                          {{ activeNodeDetail.display_name }} · {{ activeNodeDetail.primary_ip }}
                        </div>
                      </div>
                      <n-tag
                        :type="activeNodeDetail.online ? 'success' : 'error'"
                        round
                        :bordered="false"
                      >
                        {{ activeNodeDetail.online ? copy.status.node.online : copy.status.node.offline }}
                      </n-tag>
                    </div>
                  </template>
                  <template #description>
                    <n-descriptions bordered label-placement="top" :column="2" size="small">
                      <n-descriptions-item :label="copy.editor.descriptions.os">
                        {{ activeNodeDetail.os }} / {{ activeNodeDetail.arch }}
                      </n-descriptions-item>
                      <n-descriptions-item :label="copy.editor.descriptions.agent">
                        {{ activeNodeDetail.agent_version || 'dev' }}
                      </n-descriptions-item>
                      <n-descriptions-item :label="copy.editor.descriptions.heartbeat">
                        {{ formatDateTime(activeNodeDetail.last_heartbeat_at) }}
                      </n-descriptions-item>
                      <n-descriptions-item :label="copy.editor.descriptions.memory">
                        {{ formatBytes(activeNodeDetail.memory_used_bytes) }} /
                        {{ formatBytes(activeNodeDetail.memory_total_bytes) }}
                      </n-descriptions-item>
                    </n-descriptions>
                  </template>
                </n-thing>

                <div class="chip-group">
                  <span class="chip-label">{{ copy.editor.chipLabels.protocols }}</span>
                  <n-space size="small">
                    <n-tag
                      v-for="protocol in deriveProtocols(activeNodeDetail)"
                      :key="protocol"
                      :bordered="false"
                      round
                      type="info"
                    >
                      {{ protocol }}
                    </n-tag>
                  </n-space>
                </div>

                <div class="chip-group">
                  <span class="chip-label">{{ copy.editor.chipLabels.allowedPaths }}</span>
                  <n-space size="small">
                    <n-tag
                      v-for="path in pathSuggestions"
                      :key="path"
                      checkable
                      :checked="form.path === path"
                      @update:checked="form.path = path"
                    >
                      <span class="mono-text">{{ path }}</span>
                    </n-tag>
                  </n-space>
                </div>

                <div class="chip-group">
                  <span class="chip-label">{{ copy.editor.chipLabels.services }}</span>
                  <n-space size="small">
                    <n-tag
                      v-for="service in serviceSuggestions"
                      :key="service"
                      checkable
                      :checked="form.service_name === service"
                      @update:checked="form.service_name = service"
                    >
                      {{ service }}
                    </n-tag>
                  </n-space>
                </div>

                <div class="service-list">
                  <h3 class="mini-title">{{ copy.editor.observedServices }}</h3>
                  <div class="service-items">
                    <div
                      v-for="service in activeNodeDetail.services"
                      :key="`${service.name}-${service.listen_port}`"
                      class="service-item"
                    >
                      <div class="service-item__title">
                        <strong>{{ service.name }}</strong>
                        <n-tag
                          :type="service.active && service.listening ? 'success' : 'warning'"
                          :bordered="false"
                          round
                          size="small"
                        >
                          {{ service.active && service.listening ? copy.editor.serviceHealth.healthy : copy.editor.serviceHealth.degraded }}
                        </n-tag>
                      </div>
                      <div class="service-item__meta">
                        端口 {{ service.listen_port || copy.common.notAvailable }} · {{ service.message || '暂无消息' }}
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              <n-form label-placement="top" class="editor-form">
                <n-form-item :label="copy.editor.form.path">
                  <n-select
                    v-model:value="form.path"
                    filterable
                    tag
                    :options="pathOptions"
                    placeholder="/etc/XrayR/config.yml"
                  />
                </n-form-item>
                <n-form-item :label="copy.editor.form.serviceName">
                  <n-input
                    v-model:value="form.service_name"
                    :disabled="!form.restart_after_write"
                    placeholder="xrayr"
                  />
                </n-form-item>
                <div class="toggle-row">
                  <div class="toggle-card">
                    <span>{{ copy.editor.form.createBackup }}</span>
                    <n-switch v-model:value="form.create_backup" />
                  </div>
                  <div class="toggle-card">
                    <span>{{ copy.editor.form.restartAfterWrite }}</span>
                    <n-switch v-model:value="form.restart_after_write" />
                  </div>
                  <div class="toggle-card">
                    <span>{{ copy.editor.form.canaryMode }}</span>
                    <n-switch v-model:value="form.canary_mode" :disabled="selectedCount <= 1" />
                  </div>
                </div>

                <n-alert
                  v-if="configContentError"
                  :title="copy.editor.loadError"
                  type="warning"
                  class="section-alert"
                >
                  {{ configContentError }}
                </n-alert>

                <div class="editor-toolbar">
                  <div class="editor-toolbar__meta">
                    <span v-if="configContent">
                      {{ configContent.format.toUpperCase() }} · {{ configContent.size_bytes }} {{ copy.editor.metadataByteUnit }} ·
                      {{ formatDateTime(new Date(configContent.mod_time_unix * 1000).toISOString()) }}
                    </span>
                    <span v-else>{{ copy.editor.noRemoteConfig }}</span>
                  </div>
                  <n-space>
                    <n-button
                      size="small"
                      secondary
                      :disabled="!activeNodeId || !form.path"
                      :loading="loadingConfigContent"
                      @click="activeNodeId && loadConfigContent(activeNodeId, form.path)"
                    >
                      {{ copy.common.reload }}
                    </n-button>
                    <n-button
                      size="small"
                      :type="editorMode === 'structured' ? 'primary' : 'default'"
                      secondary
                      :disabled="!configContent || structuredEditorValue == null"
                      @click="syncEditorMode('structured')"
                    >
                      {{ copy.editor.modes.structured }}
                    </n-button>
                    <n-button
                      size="small"
                      :type="editorMode === 'raw' ? 'primary' : 'default'"
                      secondary
                      @click="syncEditorMode('raw')"
                    >
                      {{ copy.editor.modes.raw }}
                    </n-button>
                  </n-space>
                </div>

                <n-form-item :label="copy.editor.form.content">
                  <div v-if="loadingConfigContent" class="config-loading-shell">
                    <n-spin size="large" />
                  </div>
                  <div v-else-if="editorMode === 'structured' && structuredEditorValue != null" class="structured-shell">
                    <StructuredValueEditor
                      v-model:model-value="structuredEditorValue"
                      :label="copy.editor.structuredRoot"
                    />
                    <p v-if="configContent?.structured_error" class="host-secondary">
                      {{ copy.editor.structuredWarning }}：{{ configContent.structured_error }}
                    </p>
                  </div>
                  <n-input
                    v-else
                    v-model:value="rawEditorContent"
                    type="textarea"
                    :autosize="{ minRows: 14, maxRows: 28 }"
                    :placeholder="copy.editor.rawPlaceholder"
                  />
                </n-form-item>
              </n-form>
            </div>
          </n-card>

          <n-card :bordered="false" class="glass-card progress-card">
            <template #header>
              <div class="card-head">
                <div>
                  <h2 class="section-title">{{ copy.progress.title }}</h2>
                  <p class="section-subtitle">{{ copy.progress.subtitle }}</p>
                </div>
                <n-space align="center">
                  <n-tag v-if="currentTaskId" round :bordered="false" type="info">
                    {{ streaming ? copy.progress.streamStatus.streaming : copy.progress.streamStatus.idle }}
                  </n-tag>
                  <n-button
                    v-if="currentTask"
                    secondary
                    size="small"
                    :disabled="!currentTask"
                    @click="startTaskStream(currentTask.id)"
                  >
                    {{ copy.progress.reattach }}
                  </n-button>
                </n-space>
              </div>
            </template>

            <n-empty
              v-if="!currentTask"
              :description="copy.progress.empty"
              class="empty-state"
            />

            <div v-else class="progress-shell">
              <n-alert v-if="taskStreamError" :title="copy.progress.streamIssue" type="warning" class="section-alert">
                {{ taskStreamError }}
              </n-alert>

              <div class="task-summary-row">
                <div>
                  <div class="task-title">{{ currentTask.id }}</div>
                  <div class="task-meta">
                    {{ translateTaskType(currentTask.type) }} · {{ translateTaskStatus(currentTask.status) }} ·
                    {{ currentTask.path }}
                  </div>
                </div>
                <n-tag
                  :type="currentTask.status === 'succeeded' ? 'success' : currentTask.status === 'failed' ? 'error' : 'warning'"
                  :bordered="false"
                  round
                >
                  {{ translateTaskStatus(currentTask.status) }}
                </n-tag>
              </div>

              <n-progress
                type="line"
                :percentage="taskCompletionPercent"
                :indicator-placement="'inside'"
                processing
              />

              <div class="progress-stats-grid">
                <div class="stat-mini">
                  <span>{{ copy.progress.stats.total }}</span>
                  <strong>{{ currentTask.total_nodes }}</strong>
                </div>
                <div class="stat-mini">
                  <span>{{ copy.progress.stats.pending }}</span>
                  <strong>{{ currentTask.pending_nodes }}</strong>
                </div>
                <div class="stat-mini">
                  <span>{{ copy.progress.stats.inFlight }}</span>
                  <strong>{{ currentTask.in_flight_nodes }}</strong>
                </div>
                <div class="stat-mini">
                  <span>{{ copy.progress.stats.succeeded }}</span>
                  <strong>{{ currentTask.succeeded_nodes }}</strong>
                </div>
                <div class="stat-mini">
                  <span>{{ copy.progress.stats.failed }}</span>
                  <strong>{{ currentTask.failed_nodes }}</strong>
                </div>
                <div class="stat-mini">
                  <span>{{ copy.progress.stats.skipped }}</span>
                  <strong>{{ currentTask.skipped_nodes }}</strong>
                </div>
              </div>

              <n-data-table
                size="small"
                max-height="260"
                :columns="taskNodeColumns"
                :data="currentTask.nodes"
                :row-key="(row: TaskNodeResponse) => `${row.node_id}-${row.command_id || row.updated_at}`"
              />

              <div class="event-log">
                <h3 class="mini-title">{{ copy.progress.recentEvents }}</h3>
                <div class="event-log__items">
                  <div v-for="event in taskEvents" :key="`${event.time}-${event.event}-${event.node?.node_id || 'task'}`" class="event-item">
                    <div class="event-item__head">
                      <n-tag size="small" :bordered="false" round type="default">
                        {{ translateTaskEvent(event.event) }}
                      </n-tag>
                      <span class="host-secondary">{{ formatDateTime(event.time) }}</span>
                    </div>
                    <div class="event-item__body">
                      {{ event.message || event.node?.message || translateTaskStatus(event.task?.status || '') || copy.progress.eventFallback }}
                    </div>
                    <div v-if="event.node" class="event-item__meta mono-text">
                      {{ event.node.node_id }} · {{ translateTaskStatus(event.node.status) }}
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </n-card>
        </div>
      </section>

      <n-modal
        v-model:show="addNodeModalOpen"
        preset="card"
        :title="copy.modal.title"
        class="install-modal"
        :mask-closable="!creatingNode"
      >
        <div class="install-modal__body">
          <n-form label-placement="top">
            <n-form-item :label="copy.modal.displayName">
              <n-input
                v-model:value="addNodeDisplayName"
                :placeholder="nextAddNodeDisplayName"
                :disabled="creatingNode || createdNode !== null"
              />
            </n-form-item>
          </n-form>

          <n-space justify="end">
            <n-button secondary @click="closeAddNodeModal">{{ copy.common.close }}</n-button>
            <n-button
              type="primary"
              :loading="creatingNode"
              :disabled="createdNode !== null"
              @click="submitCreateNode"
            >
              {{ copy.modal.generateCommand }}
            </n-button>
          </n-space>

          <div v-if="createdNode" class="install-command-panel install-command-panel--modal">
            <div class="install-command-panel__head">
              <strong>{{ createdNode.display_name }}</strong>
              <n-button
                secondary
                size="small"
                @click="copyToClipboard(createdNode.install_command, copy.messages.copiedInstallCommand)"
              >
                {{ copy.common.copy }}
              </n-button>
            </div>
            <n-input
              :value="createdNode.install_command"
              readonly
              type="textarea"
              :autosize="{ minRows: 3, maxRows: 5 }"
            />
            <p class="host-secondary">
              {{ copy.modal.expiresAt(formatDateTime(createdNode.expires_at)) }}
            </p>
          </div>
        </div>
      </n-modal>
    </main>
  </n-config-provider>
</template>

<style scoped>
.console-shell {
  max-width: 1600px;
  margin: 0 auto;
  padding: 32px 20px 56px;
}

.hero-panel {
  display: flex;
  justify-content: space-between;
  gap: 24px;
  align-items: flex-end;
  margin-bottom: 20px;
}

.eyebrow {
  margin: 0 0 10px;
  text-transform: uppercase;
  letter-spacing: 0.18em;
  font-size: 12px;
  color: #0f766e;
}

.hero-title {
  margin: 0;
  max-width: 820px;
  font-size: clamp(32px, 5vw, 54px);
  line-height: 0.98;
  letter-spacing: -0.05em;
}

.hero-copy {
  max-width: 760px;
  margin: 16px 0 0;
  color: #3b556f;
  font-size: 16px;
}

.hero-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(110px, 1fr));
  gap: 12px;
  min-width: 480px;
}

.stat-pill,
.stat-mini {
  padding: 16px 18px;
  border-radius: 20px;
  background: rgba(255, 255, 255, 0.65);
  border: 1px solid rgba(148, 163, 184, 0.18);
  backdrop-filter: blur(14px);
}

.stat-pill strong,
.stat-mini strong {
  display: block;
  margin-top: 6px;
  font-size: 24px;
}

.stat-label,
.stat-mini span {
  color: #58708d;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.12em;
}

.connection-panel,
.workspace-grid {
  margin-top: 20px;
}

.glass-card {
  background: rgba(255, 255, 255, 0.62);
  border: 1px solid rgba(148, 163, 184, 0.16);
  box-shadow: 0 20px 55px rgba(15, 23, 42, 0.08);
  backdrop-filter: blur(18px);
}

.connection-grid {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(280px, 0.9fr);
  gap: 24px;
}

.connection-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 16px;
}

.connection-actions {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  gap: 16px;
}

.connection-hint {
  color: #3b556f;
}

.install-command-panel {
  display: grid;
  gap: 10px;
  padding: 14px;
  border-radius: 18px;
  background: rgba(248, 250, 252, 0.78);
}

.install-command-panel__head {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: center;
}

.install-command-panel--modal {
  margin-top: 18px;
}

.workspace-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(380px, 0.9fr);
  gap: 20px;
  align-items: start;
}

.side-stack {
  display: grid;
  gap: 20px;
  position: sticky;
  top: 16px;
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.section-title {
  margin: 0;
  font-size: 22px;
  letter-spacing: -0.04em;
}

.section-subtitle {
  margin: 6px 0 0;
  color: #5d748f;
}

.empty-state {
  min-height: 240px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.detail-layout {
  display: grid;
  gap: 18px;
}

.node-summary-shell {
  display: grid;
  gap: 16px;
}

.node-title-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: flex-start;
}

.node-title {
  font-size: 24px;
  font-weight: 700;
  letter-spacing: -0.04em;
}

.node-subtitle,
.host-secondary {
  color: #607891;
  font-size: 13px;
}

.chip-group {
  display: grid;
  gap: 10px;
}

.chip-label,
.mini-title {
  margin: 0;
  color: #40566f;
  font-size: 13px;
  text-transform: uppercase;
  letter-spacing: 0.12em;
}

.service-list {
  display: grid;
  gap: 12px;
}

.unclaimed-list {
  display: grid;
  gap: 12px;
}

.unclaimed-item {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: center;
  padding: 14px 16px;
  border-radius: 18px;
  background: rgba(248, 250, 252, 0.84);
}

.unclaimed-item__body {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 4px;
  width: 100%;
  border: 0;
  background: transparent;
  text-align: left;
  cursor: pointer;
}

.unclaimed-item__actions {
  display: flex;
  gap: 10px;
  align-items: center;
}

.unclaimed-item__title {
  font-weight: 600;
}

.unclaimed-item__meta {
  margin-top: 4px;
  color: #607891;
  font-size: 13px;
}

.service-items {
  display: grid;
  gap: 10px;
}

.service-item {
  padding: 12px 14px;
  border-radius: 16px;
  background: rgba(248, 250, 252, 0.86);
}

.service-item__title {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}

.service-item__meta {
  margin-top: 6px;
  color: #546b84;
}

.editor-form {
  display: grid;
  gap: 6px;
}

.toggle-row {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 12px;
}

.toggle-card {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border-radius: 16px;
  background: rgba(248, 250, 252, 0.8);
}

.editor-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border-radius: 16px;
  background: rgba(248, 250, 252, 0.76);
}

.editor-toolbar__meta {
  color: #546b84;
  font-size: 13px;
}

.config-loading-shell,
.structured-shell {
  width: 100%;
  border-radius: 18px;
  background: rgba(248, 250, 252, 0.72);
}

.config-loading-shell {
  min-height: 260px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.structured-shell {
  display: grid;
  gap: 12px;
  padding: 14px;
}

.progress-shell {
  display: grid;
  gap: 16px;
}

.task-summary-row {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: center;
}

.task-title {
  font-weight: 700;
  font-size: 18px;
  letter-spacing: -0.03em;
}

.task-meta {
  color: #5d748f;
}

.progress-stats-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

.event-log {
  display: grid;
  gap: 10px;
}

.event-log__items {
  display: grid;
  gap: 10px;
  max-height: 260px;
  overflow: auto;
  padding-right: 4px;
}

.event-item {
  padding: 12px 14px;
  border-radius: 16px;
  background: rgba(248, 250, 252, 0.84);
}

.event-item__head {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: center;
}

.event-item__body {
  margin-top: 8px;
}

.event-item__meta {
  margin-top: 6px;
  color: #607891;
}

.section-alert {
  margin-bottom: 4px;
}

.host-cell,
.heartbeat-cell {
  display: grid;
  gap: 4px;
}

.host-primary {
  font-weight: 600;
}

.mono-text {
  font-family: "SFMono-Regular", "JetBrains Mono", "Menlo", monospace;
}

.install-modal {
  max-width: 720px;
}

.install-modal__body {
  display: grid;
  gap: 16px;
}

:deep(.node-row) {
  cursor: pointer;
}

:deep(.node-row--active td) {
  background: rgba(14, 165, 233, 0.12) !important;
}

@media (max-width: 1180px) {
  .workspace-grid {
    grid-template-columns: 1fr;
  }

  .side-stack {
    position: static;
  }
}

@media (max-width: 900px) {
  .hero-panel,
  .connection-grid {
    grid-template-columns: 1fr;
    display: grid;
  }

  .hero-stats,
  .connection-form,
  .toggle-row,
  .progress-stats-grid {
    grid-template-columns: 1fr;
  }

  .card-head,
  .task-summary-row,
  .node-title-row,
  .editor-toolbar {
    flex-direction: column;
    align-items: flex-start;
  }
}
</style>
