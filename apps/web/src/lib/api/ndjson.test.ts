import { describe, expect, it, vi } from 'vitest'

import { readNDJSON } from './ndjson'

function chunkedResponse(chunks: string[]) {
  const encoder = new TextEncoder()
  return new Response(
    new ReadableStream({
      start(controller) {
        for (const chunk of chunks) controller.enqueue(encoder.encode(chunk))
        controller.close()
      },
    }),
    { status: 200, headers: { 'Content-Type': 'application/x-ndjson' } },
  )
}

describe('readNDJSON', () => {
  it('解析跨 chunk、CRLF、空行和末尾无换行的事件', async () => {
    const response = chunkedResponse(['{"type":"run.', 'started"}\r\n\r\n{"type":"run.completed"}'])
    const events: Array<{ type: string }> = []
    await readNDJSON(response, (event) => events.push(event))
    expect(events.map((event) => event.type)).toEqual(['run.started', 'run.completed'])
  })

  it('尊重 AbortSignal', async () => {
    const controller = new AbortController()
    controller.abort()
    await expect(readNDJSON(chunkedResponse(['{"type":"run.started"}\n']), vi.fn(), controller.signal)).rejects.toMatchObject({
      name: 'AbortError',
    })
  })
})
