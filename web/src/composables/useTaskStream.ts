import { onBeforeUnmount, ref } from 'vue'
import { streamTaskEvents, type ApiClientConfig } from '../api'
import type { TaskEventPayload } from '../types'

export function useTaskStream(
  getConfig: () => ApiClientConfig,
  onEvent: (event: TaskEventPayload) => void,
  onError: (error: unknown) => void
) {
  const currentTaskId = ref<string | null>(null)
  const streaming = ref(false)
  let controller: AbortController | null = null

  async function start(taskId: string) {
    stop()

    currentTaskId.value = taskId
    const nextController = new AbortController()
    controller = nextController
    streaming.value = true

    try {
      await streamTaskEvents(getConfig(), taskId, onEvent, nextController.signal)
    } catch (error) {
      if (!nextController.signal.aborted) {
        onError(error)
      }
    } finally {
      if (!nextController.signal.aborted) {
        streaming.value = false
      }
    }
  }

  function stop() {
    controller?.abort()
    controller = null
    streaming.value = false
  }

  onBeforeUnmount(() => {
    stop()
  })

  return {
    currentTaskId,
    streaming,
    start,
    stop
  }
}
