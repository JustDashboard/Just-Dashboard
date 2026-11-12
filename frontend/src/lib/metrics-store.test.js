import { afterEach, beforeEach, expect, test } from "bun:test"

/*
 * The store is a module with one state, as it is in a tab. Each test loads its
 * own copy over a browser of its own making: a session store, and a document
 * whose visibility the test turns.
 */
const KEY = "just-dashboard.metrics.v3"
let copy = 0
let storage
let shown
let onVisibility

function fakeStorage() {
  const map = new Map()
  return {
    getItem: (key) => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => map.set(key, String(value)),
    map,
  }
}

const load = () => import(`./metrics-store?copy=${copy++}`)

beforeEach(() => {
  storage = fakeStorage()
  shown = true
  onVisibility = undefined
  globalThis.window = { sessionStorage: storage }
  globalThis.document = {
    get visibilityState() {
      return shown ? "visible" : "hidden"
    },
    addEventListener: (name, listener) => {
      if (name === "visibilitychange") onVisibility = listener
    },
  }
})

afterEach(() => {
  delete globalThis.window
  delete globalThis.document
})

const T0 = Date.parse("2026-11-10T12:00:00Z")

function snapshot(seconds, patch = {}) {
  return {
    ts: new Date(T0 + seconds * 1000).toISOString(),
    cpu: {
      totalPercent: 12,
      modes: { user: 7, system: 2, irq: 1, softirq: 0.5, iowait: 1, steal: 0 },
    },
    memory: { usedPercent: 40 },
    swap: { usedPercent: 3 },
    net: [
      { recvRate: 100, sendRate: 10 },
      { recvRate: 50, sendRate: 5 },
    ],
    mounts: [
      {
        usedPercent: 91,
        readRate: 1,
        writeRate: 2,
        readOps: 3,
        writeOps: 4,
        busyPercent: 5,
        readLatencyMs: 0.5,
        writeLatencyMs: 2,
      },
      {
        usedPercent: 12,
        readRate: 10,
        writeRate: 20,
        readOps: 30,
        writeOps: 40,
        busyPercent: 70,
        readLatencyMs: 9,
        writeLatencyMs: 1,
      },
    ],
    pressure: { supported: true, cpuSome: 0.4, memSome: 0, ioSome: 1.5 },
    sockets: { tcpInUse: 300 },
    procs: { blocked: 2 },
    ...patch,
  }
}

const point = (ts, cpu = 1) => ({ t: "", ts, cpu })

// ---------------------------------------------------------------- a frame

test("a frame becomes one point: rates summed, the fullest disk, the busiest and the slowest", async () => {
  const store = await load()
  store.pushSnapshot(snapshot(0))
  const [p] = store.getState().history
  expect(p.ts).toBe(T0)
  expect([p.cpu, p.mem, p.swap]).toEqual([12, 40, 3])
  expect([p.rx, p.tx]).toEqual([150, 15])
  expect([p.dread, p.dwrite, p.dreads, p.dwrites]).toEqual([11, 22, 33, 44])
  // Not the mean of the two: one full disk is the question, one saturated disk the problem.
  expect(p.disk).toBe(91)
  expect(p.busy).toBe(70)
  expect(p.await).toBe(9)
  // Interrupts are the kernel's time.
  expect([p.cpuUser, p.cpuSystem, p.cpuIowait, p.cpuSteal]).toEqual([7, 3.5, 1, 0])
  expect([p.psiCpu, p.psiMem, p.psiIo]).toEqual([0.4, 0, 1.5])
  expect([p.tcp, p.procsBlocked]).toEqual([300, 2])
})

test("a kernel without pressure is marked, not measured as zero", async () => {
  const store = await load()
  store.pushSnapshot(snapshot(0, { pressure: { supported: false } }))
  store.pushSnapshot(snapshot(2, { pressure: undefined }))
  for (const p of store.getState().history)
    expect([p.psiCpu, p.psiMem, p.psiIo]).toEqual([-1, -1, -1])
})

test("a frame from a host that reports less still makes a whole point", async () => {
  const store = await load()
  store.pushSnapshot(
    snapshot(0, {
      cpu: { totalPercent: 5 },
      mounts: [{ usedPercent: 50, readRate: 0, writeRate: 0 }],
      sockets: undefined,
      procs: undefined,
    }),
  )
  const [p] = store.getState().history
  expect([p.cpuUser, p.cpuSystem, p.cpuIowait, p.cpuSteal]).toEqual([0, 0, 0, 0])
  expect([p.dreads, p.dwrites, p.await, p.busy]).toEqual([0, 0, 0, 0])
  expect([p.tcp, p.procsBlocked]).toEqual([0, 0])
})

test("the buffer keeps the newest five minutes and lets the oldest frame go", async () => {
  const store = await load()
  for (let i = 0; i < store.HISTORY + 3; i++) store.pushSnapshot(snapshot(i * 2))
  const history = store.getState().history
  expect(history).toHaveLength(store.HISTORY)
  expect(history[0].ts).toBe(T0 + 3 * 2000)
  expect(history.at(-1).ts).toBe(T0 + (store.HISTORY + 2) * 2000)
})

test("a frame clears the error the first fetch left, and each state is a new object for its readers", async () => {
  const store = await load()
  store.setError("could not reach the server")
  const before = store.getState()
  expect(before.error).toBe("could not reach the server")
  store.pushSnapshot(snapshot(0))
  const after = store.getState()
  expect(after.error).toBeUndefined()
  expect(after).not.toBe(before)
  expect(after.history).not.toBe(before.history)
})

test("the one-shot fetch seeds the page and never replaces what the stream has said", async () => {
  const store = await load()
  const streamed = snapshot(0)
  store.setHost({ hostname: "stream" })
  store.pushSnapshot(streamed)
  store.seed({ hostname: "fetch" }, snapshot(99))
  expect(store.getState().host.hostname).toBe("stream")
  expect(store.getState().snapshot).toBe(streamed)

  const empty = await load()
  const fetched = snapshot(5)
  empty.seed({ hostname: "fetch" }, fetched)
  expect(empty.getState().host.hostname).toBe("fetch")
  expect(empty.getState().snapshot).toBe(fetched)
})

test("a connection that did not change announces nothing", async () => {
  const store = await load()
  let told = 0
  const stop = store.subscribe(() => told++)
  store.setConnection("open")
  store.setConnection("open")
  expect(told).toBe(1)
  stop()
  store.setConnection("closed")
  expect(told).toBe(1)
})

// ----------------------------------------------------------- across a reload

test("the buffer is written every fifth frame, and at once when the page is leaving", async () => {
  const store = await load()
  for (let i = 0; i < 4; i++) store.pushSnapshot(snapshot(i * 2))
  expect(storage.map.has(KEY)).toBe(false)
  store.pushSnapshot(snapshot(8))
  expect(JSON.parse(storage.map.get(KEY))).toHaveLength(5)

  store.pushSnapshot(snapshot(10))
  expect(JSON.parse(storage.map.get(KEY))).toHaveLength(5)
  store.flush()
  expect(JSON.parse(storage.map.get(KEY))).toHaveLength(6)

  // Nothing new since: leaving again writes nothing.
  storage.map.delete(KEY)
  store.flush()
  expect(storage.map.has(KEY)).toBe(false)
})

test("a reload gets its chart back, without the frames that are too old to join on to", async () => {
  const now = Date.now()
  storage.setItem(
    KEY,
    JSON.stringify([
      point(now - 11 * 60_000, 1),
      point(now - 10 * 60_000 - 5_000, 2),
      point(now - 9 * 60_000, 3),
      point(now - 2_000, 4),
    ]),
  )
  const store = await load()
  expect(store.getState().history.map((p) => p.cpu)).toEqual([3, 4])
})

test("what is restored is the newest buffer's worth, and only what is a point", async () => {
  const now = Date.now()
  const many = Array.from({ length: 200 }, (_, i) => point(now - (200 - i) * 1000, i))
  storage.setItem(KEY, JSON.stringify([null, "text", { cpu: 1 }, { ts: "soon" }, ...many]))
  const store = await load()
  const history = store.getState().history
  expect(history).toHaveLength(store.HISTORY)
  expect(history[0].cpu).toBe(50)
  expect(history.at(-1).cpu).toBe(199)
})

test("a store that cannot be read, or holds something else, starts an empty chart", async () => {
  for (const held of ["{not json", JSON.stringify({ history: [] }), ""]) {
    storage.setItem(KEY, held)
    const store = await load()
    expect(store.getState().history).toEqual([])
  }
  globalThis.window = {
    get sessionStorage() {
      throw new Error("storage is disabled")
    },
  }
  const store = await load()
  expect(store.getState().history).toEqual([])
  // And a frame is still a frame where nothing can be kept.
  for (let i = 0; i < 5; i++) store.pushSnapshot(snapshot(i * 2))
  expect(store.getState().history).toHaveLength(5)
})

test("the restored chart is read once: a frame lands on it and the store is not read again", async () => {
  const now = Date.now()
  storage.setItem(KEY, JSON.stringify([point(now - 4_000, 7)]))
  const store = await load()
  store.pushSnapshot(snapshot(0))
  expect(store.getState().history.map((p) => p.cpu)).toEqual([7, 12])
  storage.setItem(KEY, JSON.stringify([point(now - 2_000, 99)]))
  expect(store.getState().history.map((p) => p.cpu)).toEqual([7, 12])
})

test("the server draws no history at all", async () => {
  const store = await load()
  store.pushSnapshot(snapshot(0))
  expect(store.getServerState()).toEqual({ history: [], connection: "closed" })
  expect(store.getServerState()).toBe(store.getServerState())
})

// ------------------------------------------------------------ a hidden tab

test("a hidden tab keeps every frame and tells nobody until it is shown", async () => {
  const store = await load()
  let told = 0
  store.subscribe(() => told++)
  store.pushSnapshot(snapshot(0))
  expect(told).toBe(1)

  shown = false
  store.pushSnapshot(snapshot(2))
  store.pushSnapshot(snapshot(4))
  expect(told).toBe(1)
  expect(store.getState().history).toHaveLength(3)

  // Shown again: one render catches up on everything that arrived.
  shown = true
  onVisibility()
  expect(told).toBe(2)
  // And showing a tab that missed nothing draws nothing.
  onVisibility()
  expect(told).toBe(2)
})
