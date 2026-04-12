export interface ApiServiceStatus {
  name: string
  active: boolean
  listening: boolean
  listen_port: number
  message: string
}

export interface NodeSummaryResponse {
  id: string
  hostname: string
  display_name: string
  status: string
  primary_ip: string
  os: string
  arch: string
  agent_version: string
  registered_at: string
  last_register_at: string
  last_heartbeat_at?: string
  last_agent_time_unix: number
  online: boolean
  cpu_percent: number
  memory_used_bytes: number
  memory_total_bytes: number
  disk_used_bytes: number
  disk_total_bytes: number
  load_1: number
  load_5: number
  load_15: number
  config_error: string
  service_flavors: string[]
  services: ApiServiceStatus[]
}

export interface NodeDetailResponse extends NodeSummaryResponse {
  kernel: string
  created_at: string
  updated_at: string
}

export interface NodeConfigResponse {
  id: string
  hostname: string
  display_name: string
  status: string
  primary_ip: string
  os: string
  arch: string
  kernel: string
  agent_version: string
  registered_at: string
  last_register_at: string
  service_flavors: string[]
  allowed_paths: string[]
  config_paths: string[]
}

export interface NodeConfigContentResponse {
  node_id: string
  path: string
  format: 'yaml' | 'json' | 'text' | string
  raw_content: string
  structured_content?: unknown
  structured_error?: string
  size_bytes: number
  mod_time_unix: number
  fetched_at: string
}

export interface ConfigPushRequest {
  path: string
  content: string
  service_name: string
  create_backup: boolean
  restart_after_write: boolean
}

export interface CreateNodeRequest {
  display_name: string
}

export interface CreateNodeResponse {
  id: string
  display_name: string
  install_secret: string
  install_command: string
  status: string
  expires_at: string
}

export interface ClaimNodeRequest {
  display_name?: string
}

export interface InstallCommandResponse {
  install_command: string
}

export interface BatchConfigRequest extends ConfigPushRequest {
  node_ids: string[]
  canary_mode: boolean
}

export interface TaskNodeResponse {
  node_id: string
  command_id?: string
  status: string
  result_status?: string
  message?: string
  backup_path?: string
  changed: boolean
  updated_at: string
}

export interface ConfigTaskResponse {
  id: string
  type: string
  status: string
  path: string
  service_name: string
  create_backup: boolean
  restart_after_write: boolean
  max_concurrency: number
  canary_mode: boolean
  canary_size: number
  created_at: string
  updated_at: string
  total_nodes: number
  pending_nodes: number
  in_flight_nodes: number
  succeeded_nodes: number
  failed_nodes: number
  skipped_nodes: number
  nodes: TaskNodeResponse[]
}

export interface TaskEventPayload {
  event: string
  task_id?: string
  time: string
  message?: string
  task?: ConfigTaskResponse
  node?: TaskNodeResponse
}
