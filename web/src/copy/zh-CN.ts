export const zhCN = {
  common: {
    copy: '复制',
    close: '关闭',
    reload: '重新加载',
    unknown: '未知',
    none: '无',
    notAvailable: '未提供',
    unexpectedError: '发生了未预期的错误',
    sameOriginProxy: '同源地址 / Vite 代理'
  },
  hero: {
    eyebrow: '节点管理面板',
    titleSuffix: '实时下发控制台',
    copy: '查看节点状态，向单节点或批量节点下发配置，并在当前页面通过认证 SSE 观察发布进度。',
    stats: {
      nodes: '节点数',
      online: '在线',
      selected: '已选',
      unclaimed: '待认领'
    }
  },
  connection: {
    apiBaseUrlLabel: 'API 地址',
    apiBaseUrlPlaceholder: '留空则使用同源地址或 Vite 代理',
    tokenLabel: 'Bearer Token',
    tokenPlaceholder: 'JCMANAGER_API_TOKEN',
    target: '目标地址',
    tokenStorage: '令牌存储',
    tokenStorageValue: '仅保存在当前浏览器标签页会话中',
    installCommand: '通用安装命令',
    installCommandPlaceholder: '连接面板后即可加载带认证的安装命令。',
    connect: '连接面板',
    clearSelection: '清空选择',
    nodeListError: '节点列表加载失败'
  },
  nodes: {
    title: '节点列表',
    subtitle: '点击任意节点即可加载配置目标路径和服务元数据。',
    selectedTag: (count: number) => `已选 ${count} 台`,
    addNode: '新增节点',
    applyToSelected: '下发到所选节点',
    columns: {
      hostname: '主机名',
      ip: 'IP',
      protocol: '协议',
      status: '状态',
      lastHeartbeat: '最后心跳'
    }
  },
  unclaimed: {
    title: '待认领节点',
    subtitle: '通过通用命令安装的节点会先出现在这里，认领后才能进入正式列表。',
    empty: '当前没有待认领节点。',
    unknownHost: '未知主机',
    noIpYet: '暂无 IP',
    online: '在线',
    waiting: '待认领',
    claim: '认领节点'
  },
  editor: {
    title: '配置编辑器',
    subtitle: '批量选择时会复用这里的配置内容进行下发。',
    applyToNode: '下发到当前节点',
    empty: '选择一个节点后，可查看它的配置入口和允许写入的路径。',
    nodeDetailError: '节点详情加载失败',
    descriptions: {
      os: '系统',
      agent: 'Agent 版本',
      heartbeat: '最后心跳',
      memory: '内存'
    },
    chipLabels: {
      protocols: '协议',
      allowedPaths: '允许写入路径',
      services: '服务'
    },
    observedServices: '已发现服务',
    serviceHealth: {
      healthy: '正常',
      degraded: '异常'
    },
    form: {
      path: '配置路径',
      serviceName: '重启服务',
      createBackup: '创建备份',
      restartAfterWrite: '写入后重启',
      canaryMode: '灰度发布',
      content: '配置内容'
    },
    loadError: '配置加载失败',
    noRemoteConfig: '还没有加载远程配置',
    modes: {
      structured: '结构化',
      raw: '原始'
    },
    structuredRoot: '根节点',
    structuredWarning: '结构化解析提示',
    rawPlaceholder: '粘贴将要写入目标路径的配置内容',
    metadataByteUnit: '字节'
  },
  progress: {
    title: '任务进度',
    subtitle: '当前下发任务的认证 SSE 事件流。',
    streamStatus: {
      streaming: '流式更新中',
      idle: '空闲'
    },
    reattach: '重新连接事件流',
    empty: '开始一次配置下发后，这里会显示任务快照、节点结果和进度。',
    streamIssue: 'SSE 事件流异常',
    stats: {
      total: '总数',
      pending: '待处理',
      inFlight: '执行中',
      succeeded: '成功',
      failed: '失败',
      skipped: '跳过'
    },
    recentEvents: '最近事件',
    eventFallback: '任务更新',
    columns: {
      node: '节点',
      status: '状态',
      changed: '已变更',
      message: '消息',
      updated: '更新时间'
    },
    changed: {
      yes: '是',
      no: '否'
    }
  },
  modal: {
    title: '新增节点',
    displayName: '显示名称',
    generateCommand: '生成命令',
    expiresAt: (time: string) => `命令将于 ${time} 过期，请在目标 VPS 上执行。`
  },
  status: {
    node: {
      pendingInstall: '待安装',
      unclaimed: '待认领',
      online: '在线',
      offline: '离线'
    },
    task: {
      pending: '待处理',
      queued: '排队中',
      running: '进行中',
      succeeded: '成功',
      failed: '失败',
      skipped: '已跳过',
      halted: '已停止'
    },
    event: {
      taskCreated: '任务已创建',
      taskUpdated: '任务更新',
      taskComplete: '任务已完成',
      taskHalted: '任务已停止',
      nodeStarted: '节点开始执行',
      nodeUpdated: '节点状态更新'
    }
  },
  taskType: {
    config_push: '配置下发',
    push_config: '配置下发',
    batch_config_push: '批量配置下发',
    batch_config: '批量配置下发'
  },
  messages: {
    loadedNodes: (count: number) => `已加载 ${count} 个节点。`,
    createdInstallCommand: (name: string) => `已为 ${name} 生成安装命令。`,
    claimedNode: (name: string) => `已认领 ${name}。`,
    copiedUniversalInstallCommand: '已复制通用安装命令。',
    copiedInstallCommand: '已复制安装命令。',
    queuedSingleNode: (name: string) => `已将 ${name} 的配置下发任务加入队列。`,
    queuedBatchNodes: (count: number) => `已将配置下发任务加入队列，共 ${count} 个节点。`
  },
  errors: {
    loadNodesNeedsToken: '请先填写 Bearer Token 再加载节点。',
    createNodeNeedsToken: '请先填写 Bearer Token 再新增节点。',
    displayNameRequired: '显示名称不能为空。',
    claimNeedsToken: '请先填写 Bearer Token 再认领节点。',
    clipboardUnavailable: '当前浏览器环境不支持剪贴板复制。',
    clipboardFailed: '复制失败，请在当前浏览器环境中重试。',
    loadNodeNeedsToken: '请先填写 Bearer Token 再加载节点详情。',
    configPathRequired: '配置路径不能为空。',
    serviceNameRequired: '开启写入后重启时，服务名不能为空。',
    selectNodeFirst: '请先选择一个节点。',
    selectBatchNodes: '请至少选择一个节点进行批量下发。'
  },
  relativeTime: {
    never: '从未',
    noHeartbeat: '暂无心跳',
    justNow: '刚刚',
    minutesAgo: (count: number) => `${count} 分钟前`,
    hoursAgo: (count: number) => `${count} 小时前`,
    daysAgo: (count: number) => `${count} 天前`
  },
  structuredEditor: {
    scalarTypes: {
      text: '文本',
      number: '数字',
      boolean: '布尔值',
      null: '空值'
    },
    labels: {
      object: '对象',
      array: '数组'
    },
    tags: {
      object: '对象',
      array: '数组',
      null: '空值'
    },
    actions: {
      addField: '新增字段',
      addItem: '新增项',
      remove: '删除'
    },
    placeholders: {
      fieldName: '字段名',
      textValue: '文本值'
    },
    itemLabel: (index: number) => `第 ${index} 项`,
    booleanValue: {
      true: '是',
      false: '否'
    },
    newKeyBase: 'new_key'
  }
} as const
