import type { components } from './generated'

export type RunEvent = components['schemas']['RunEvent']

function abortError() {
  return new DOMException('操作已取消', 'AbortError')
}

export async function readNDJSON(
  response: Response,
  onEvent: (event: RunEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  if (!response.body) throw new Error('响应不支持流式读取')
  if (signal?.aborted) throw abortError()

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let aborted = false
  const onAbort = () => {
    aborted = true
    void reader.cancel(abortError())
  }
  signal?.addEventListener('abort', onAbort, { once: true })

  const consume = (line: string) => {
    const trimmed = line.trim()
    if (trimmed) onEvent(JSON.parse(trimmed) as RunEvent)
  }

  try {
    while (true) {
      const { done, value } = await reader.read()
      if (aborted) throw abortError()
      buffer += decoder.decode(value, { stream: !done })
      let newline = buffer.indexOf('\n')
      while (newline >= 0) {
        consume(buffer.slice(0, newline).replace(/\r$/, ''))
        buffer = buffer.slice(newline + 1)
        newline = buffer.indexOf('\n')
      }
      if (done) break
    }
    consume(buffer)
  } finally {
    signal?.removeEventListener('abort', onAbort)
    reader.releaseLock()
  }
}
