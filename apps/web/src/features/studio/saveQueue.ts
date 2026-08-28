import type { Graph, SaveDraftRequest, Workflow } from '../../lib/api/client'

export type SaveState = 'saved' | 'pending' | 'saving' | 'conflict' | 'error'
type Save = (request: SaveDraftRequest) => Promise<Pick<Workflow, 'draftRevision'>>

export class SaveQueue {
  private revision: number
  private readonly save: Save
  private readonly debounceMs: number
  private readonly onState?: (state: SaveState) => void
  private pending?: Graph
  private timer?: ReturnType<typeof setTimeout>
  private inFlight?: Promise<void>
  private stopped = false
  private failure?: unknown

  constructor(revision: number, save: Save, debounceMs = 800, onState?: (state: SaveState) => void) {
    this.revision = revision
    this.save = save
    this.debounceMs = debounceMs
    this.onState = onState
  }

  enqueue(graph: Graph) {
    if (this.stopped) return
    this.pending = structuredClone(graph)
    this.onState?.('pending')
    if (this.inFlight) return
    if (this.timer) clearTimeout(this.timer)
    this.timer = setTimeout(() => {
      this.timer = undefined
      this.start()
    }, this.debounceMs)
  }

  getRevision() {
    return this.revision
  }

  stop() {
    this.stopped = true
    this.pending = undefined
    this.failure = undefined
    if (this.timer) clearTimeout(this.timer)
    this.timer = undefined
  }

  adoptRevision(revision: number): void {
    if (this.timer || this.inFlight || this.pending || this.stopped) {
      throw new Error('save queue must be idle before adopting a revision')
    }
    if (revision <= this.revision) {
      throw new Error('adopted revision must advance')
    }
    this.revision = revision
    this.failure = undefined
    this.onState?.('saved')
  }

  async flush() {
    if (this.timer) {
      clearTimeout(this.timer)
      this.timer = undefined
    }
    if (!this.inFlight && this.pending && !this.stopped) this.start()
    while (this.inFlight) await this.inFlight
    if (this.failure) throw this.failure
  }

  private start() {
    if (this.inFlight || !this.pending || this.stopped) return
    const graph = this.pending
    this.pending = undefined
    this.onState?.('saving')
    this.inFlight = this.save({ draftRevision: this.revision, graph })
      .then((workflow) => {
        this.revision = workflow.draftRevision
        this.failure = undefined
        this.onState?.(this.pending ? 'pending' : 'saved')
      })
      .catch((error: unknown) => {
        this.failure = error
        if (isConflict(error)) {
          this.stopped = true
          this.pending = undefined
          this.onState?.('conflict')
        } else {
          this.onState?.('error')
        }
      })
      .finally(() => {
        this.inFlight = undefined
        if (this.pending && !this.stopped && !this.failure) this.start()
      })
  }
}

function isConflict(error: unknown) {
  return typeof error === 'object' && error !== null && 'status' in error && error.status === 409
}
