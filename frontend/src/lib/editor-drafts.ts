// These buffers can contain credentials. Keep them in this tab's memory and
// drop them at the same account boundary as the other workspace state.
const stores = new Set<{ size: () => number; clear: () => void }>()
let listening = false

function installGuard() {
  if (listening || typeof window === "undefined") return
  listening = true
  window.addEventListener("beforeunload", (event) => {
    if (![...stores].some((store) => store.size() > 0)) return
    event.preventDefault()
    event.returnValue = ""
  })
  window.addEventListener("jd-working-state-cleared", () => {
    for (const store of stores) store.clear()
  })
}

export function createEditorDraftStore<T>() {
  const drafts = new Map<string, T>()
  const pending = new Map<string, Promise<unknown>>()
  let generation = 0
  const store = {
    generation: () => generation,
    get: (key: string) => drafts.get(key),
    set(key: string, draft: T, owner: number) {
      if (owner !== generation) return
      installGuard()
      drafts.set(key, draft)
    },
    forget(key: string, owner: number) {
      if (owner === generation) drafts.delete(key)
    },
    track(key: string, task: Promise<unknown>, owner: number) {
      if (owner !== generation) return
      pending.set(key, task)
      const done = () => {
        if (pending.get(key) === task) pending.delete(key)
      }
      void task.then(done, done)
    },
    async settled(key: string) {
      // A returning editor must not take the old revision while its previous
      // instance still has a save in flight and can acknowledge a newer one.
      while (pending.has(key)) await pending.get(key)?.catch(() => undefined)
    },
    size: () => drafts.size,
    clear() {
      generation++
      drafts.clear()
      pending.clear()
    },
  }
  stores.add(store)
  installGuard()
  return store
}
