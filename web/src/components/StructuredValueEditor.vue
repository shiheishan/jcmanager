<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NCard, NInput, NInputNumber, NSelect, NSpace, NSwitch, NTag } from 'naive-ui'

defineOptions({
  name: 'StructuredValueEditor'
})

const props = withDefaults(
  defineProps<{
    modelValue: unknown
    label?: string
    depth?: number
  }>(),
  {
    label: '',
    depth: 0
  }
)

const emit = defineEmits<{
  'update:modelValue': [value: unknown]
}>()

const scalarTypeOptions = [
  { label: 'Text', value: 'string' },
  { label: 'Number', value: 'number' },
  { label: 'Boolean', value: 'boolean' },
  { label: 'Null', value: 'null' }
]

const valueKind = computed(() => detectValueKind(props.modelValue))
const objectEntries = computed(() =>
  valueKind.value === 'object'
    ? Object.entries(props.modelValue as Record<string, unknown>)
    : []
)
const arrayItems = computed(() =>
  valueKind.value === 'array'
    ? (props.modelValue as unknown[])
    : []
)
const scalarKind = computed(() =>
  valueKind.value === 'scalar' ? detectScalarKind(props.modelValue) : 'string'
)

function emitValue(value: unknown) {
  emit('update:modelValue', value)
}

function updateObjectField(key: string, nextValue: unknown) {
  const current = { ...((props.modelValue ?? {}) as Record<string, unknown>) }
  current[key] = nextValue
  emitValue(current)
}

function renameObjectField(previousKey: string, nextKey: string) {
  const trimmed = nextKey.trim()
  if (!trimmed || trimmed === previousKey) {
    return
  }

  const current = { ...((props.modelValue ?? {}) as Record<string, unknown>) }
  const fieldValue = current[previousKey]
  delete current[previousKey]
  current[trimmed] = fieldValue
  emitValue(current)
}

function removeObjectField(key: string) {
  const current = { ...((props.modelValue ?? {}) as Record<string, unknown>) }
  delete current[key]
  emitValue(current)
}

function addObjectField() {
  const current = { ...((props.modelValue ?? {}) as Record<string, unknown>) }
  const base = 'new_key'
  let candidate = base
  let index = 1
  for (;;) {
    if (!(candidate in current)) {
      current[candidate] = ''
      emitValue(current)
      return
    }
    index += 1
    candidate = `${base}_${index}`
  }
}

function updateArrayItem(index: number, nextValue: unknown) {
  const current = [...((props.modelValue ?? []) as unknown[])]
  current[index] = nextValue
  emitValue(current)
}

function removeArrayItem(index: number) {
  const current = [...((props.modelValue ?? []) as unknown[])]
  current.splice(index, 1)
  emitValue(current)
}

function addArrayItem() {
  const current = [...((props.modelValue ?? []) as unknown[])]
  current.push('')
  emitValue(current)
}

function updateScalarKind(nextKind: string) {
  switch (nextKind) {
    case 'number':
      emitValue(0)
      return
    case 'boolean':
      emitValue(false)
      return
    case 'null':
      emitValue(null)
      return
    default:
      emitValue(props.modelValue == null ? '' : String(props.modelValue))
  }
}

function detectValueKind(value: unknown): 'object' | 'array' | 'scalar' {
  if (Array.isArray(value)) {
    return 'array'
  }
  if (value !== null && typeof value === 'object') {
    return 'object'
  }
  return 'scalar'
}

function detectScalarKind(value: unknown): string {
  if (value === null) {
    return 'null'
  }
  switch (typeof value) {
    case 'number':
      return 'number'
    case 'boolean':
      return 'boolean'
    default:
      return 'string'
  }
}
</script>

<template>
  <div class="structured-editor" :style="{ '--depth': String(depth) }">
    <div v-if="valueKind === 'object'" class="branch-shell">
      <div class="branch-head">
        <div class="branch-label">
          <span>{{ label || 'Object' }}</span>
          <n-tag size="small" :bordered="false">object</n-tag>
        </div>
        <n-button tertiary size="small" @click="addObjectField">Add field</n-button>
      </div>

      <div class="branch-body">
        <n-card
          v-for="[entryKey, entryValue] in objectEntries"
          :key="entryKey"
          size="small"
          embedded
          class="entry-card"
        >
          <div class="entry-head">
            <n-input
              :value="entryKey"
              size="small"
              placeholder="field name"
              @update:value="renameObjectField(entryKey, $event)"
            />
            <n-button tertiary size="small" type="error" @click="removeObjectField(entryKey)">
              Remove
            </n-button>
          </div>

          <StructuredValueEditor
            :model-value="entryValue"
            :depth="depth + 1"
            @update:model-value="updateObjectField(entryKey, $event)"
          />
        </n-card>
      </div>
    </div>

    <div v-else-if="valueKind === 'array'" class="branch-shell">
      <div class="branch-head">
        <div class="branch-label">
          <span>{{ label || 'Array' }}</span>
          <n-tag size="small" :bordered="false">array</n-tag>
        </div>
        <n-button tertiary size="small" @click="addArrayItem">Add item</n-button>
      </div>

      <div class="branch-body">
        <n-card
          v-for="(item, index) in arrayItems"
          :key="`${index}-${typeof item}`"
          size="small"
          embedded
          class="entry-card"
        >
          <div class="entry-head">
            <span class="entry-index">Item {{ index + 1 }}</span>
            <n-button tertiary size="small" type="error" @click="removeArrayItem(index)">
              Remove
            </n-button>
          </div>

          <StructuredValueEditor
            :model-value="item"
            :depth="depth + 1"
            @update:model-value="updateArrayItem(index, $event)"
          />
        </n-card>
      </div>
    </div>

    <div v-else class="scalar-shell">
      <div class="scalar-head">
        <span v-if="label" class="branch-label">{{ label }}</span>
        <n-select
          :value="scalarKind"
          size="small"
          :options="scalarTypeOptions"
          class="scalar-type"
          @update:value="updateScalarKind"
        />
      </div>

      <n-input
        v-if="scalarKind === 'string'"
        :value="modelValue == null ? '' : String(modelValue)"
        placeholder="text value"
        @update:value="emitValue"
      />

      <n-input-number
        v-else-if="scalarKind === 'number'"
        :value="typeof modelValue === 'number' ? modelValue : 0"
        style="width: 100%"
        @update:value="emitValue($event ?? 0)"
      />

      <div v-else-if="scalarKind === 'boolean'" class="boolean-row">
        <span>{{ modelValue ? 'True' : 'False' }}</span>
        <n-switch :value="Boolean(modelValue)" @update:value="emitValue" />
      </div>

      <div v-else class="null-row">
        <n-tag :bordered="false">null</n-tag>
      </div>
    </div>
  </div>
</template>

<style scoped>
.structured-editor {
  display: grid;
  gap: 12px;
}

.branch-shell,
.scalar-shell {
  padding-left: calc(var(--depth) * 10px);
}

.branch-head,
.scalar-head,
.entry-head,
.boolean-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.branch-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}

.branch-body {
  display: grid;
  gap: 10px;
}

.entry-card {
  background: rgba(255, 255, 255, 0.6);
}

.entry-head {
  margin-bottom: 10px;
}

.entry-index {
  color: #50667f;
  font-size: 13px;
}

.scalar-type {
  width: 120px;
}

.null-row {
  display: flex;
  align-items: center;
}
</style>
