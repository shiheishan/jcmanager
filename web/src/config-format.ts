import YAML from 'yaml'

export function serializeConfigContent(format: string, value: unknown, rawFallback: string): string {
  switch (format) {
    case 'json':
      return `${JSON.stringify(value, null, 2)}\n`
    case 'yaml':
      return YAML.stringify(value)
    default:
      return rawFallback
  }
}

export function parseStructuredContent(format: string, rawContent: string): unknown {
  switch (format) {
    case 'json':
      return JSON.parse(rawContent)
    case 'yaml':
      return YAML.parse(rawContent)
    default:
      throw new Error('Plain text config has no structured schema')
  }
}
