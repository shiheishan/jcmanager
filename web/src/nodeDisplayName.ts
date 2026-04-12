import type { NodeSummaryResponse } from './types'

const defaultPrefix = 'HK-'
const defaultWidth = 2

export function suggestNextNodeDisplayName(nodes: NodeSummaryResponse[]): string {
  let maxSuffix = 0

  for (const node of nodes) {
    const value = node.display_name.trim()
    const match = /^HK-(\d+)$/.exec(value)
    if (!match) {
      continue
    }

    const suffix = Number.parseInt(match[1], 10)
    if (Number.isNaN(suffix)) {
      continue
    }
    maxSuffix = Math.max(maxSuffix, suffix)
  }

  return `${defaultPrefix}${String(maxSuffix + 1).padStart(defaultWidth, '0')}`
}
