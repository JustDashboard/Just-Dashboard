import { expect, test } from "bun:test"
import {
  crowdsecStanding,
  defaultTab,
  fail2banFault,
  fail2banStanding,
  flagOf,
  posture,
  shownTab,
  standings,
  suricataStanding,
} from "./intrusion-model"

const jail = (over = {}) => ({
  name: "sshd",
  currentlyFailed: 0,
  totalFailed: 0,
  currentlyBanned: 0,
  totalBanned: 0,
  bannedIps: [],
  fileList: [],
  ...over,
})
const running = { available: true, running: true, jails: [jail({ currentlyBanned: 2 })] }
const crowdsec = (over = {}) => ({
  installed: true,
  active: true,
  decisions: [{}, {}],
  alerts: [],
  bouncers: [],
  enforcement: { state: "enforcing" },
  ...over,
})
const suricata = (over = {}) => ({
  installed: true,
  active: true,
  mode: "ids",
  scanned: 214,
  ...over,
})
const absent = { installed: false, active: false, decisions: [], alerts: [], bouncers: [] }

test("fail2ban says what it holds, and its line moves only while something is failing", () => {
  expect(fail2banStanding(running)).toMatchObject({
    kind: "watching",
    tone: "running",
    count: "2 banned now",
    line: { state: "up", moving: false },
  })
  const busy = { ...running, jails: [jail({ currentlyFailed: 3 })] }
  expect(fail2banStanding(busy).line).toEqual({ state: "up", moving: true })
  expect(fail2banStanding({ available: true, running: false, jails: [] })).toMatchObject({
    kind: "stopped",
    word: "not running",
    line: { state: "down" },
  })
  expect(fail2banStanding({ available: false, running: false, jails: [] }).kind).toBe("absent")
  expect(fail2banStanding(undefined).kind).toBe("unknown")
})

test("CrowdSec is watching only where its verdict says decisions are dropped", () => {
  expect(crowdsecStanding(crowdsec())).toMatchObject({
    kind: "watching",
    tone: "running",
    word: "enforcing",
    count: "2 decisions",
    line: { state: "up", moving: true },
  })
  expect(crowdsecStanding(crowdsec({ enforcement: { state: "degraded" } }))).toMatchObject({
    kind: "failing",
    tone: "danger",
    word: "not dropping",
    line: { state: "danger" },
  })
  expect(crowdsecStanding(crowdsec({ enforcement: { state: "partial" } }))).toMatchObject({
    kind: "watching",
    tone: "notice",
    word: "HTTP only",
    line: { state: "warning" },
  })
  // A server that sends no verdict is not evidence of protection.
  expect(crowdsecStanding(crowdsec({ enforcement: undefined })).word).toBe("unverified")
  expect(crowdsecStanding(crowdsec({ active: false }))).toMatchObject({
    kind: "stopped",
    line: { state: "down" },
  })
  expect(crowdsecStanding(absent).kind).toBe("absent")
})

test("Suricata says whether it can drop, and is closed to a reader", () => {
  expect(suricataStanding(suricata(), true)).toMatchObject({
    kind: "watching",
    word: "only reads",
    count: "214 alerts scanned",
  })
  expect(suricataStanding(suricata({ mode: "ips" }), true).word).toBe("can drop")
  expect(suricataStanding(suricata({ active: false }), true).kind).toBe("stopped")
  expect(suricataStanding(undefined, true).kind).toBe("unknown")
  expect(suricataStanding(undefined, false)).toMatchObject({ kind: "closed" })
  const seeing = suricata({ capture: { kernelPackets: 10, decoderPackets: 0 } })
  expect(suricataStanding(seeing, true).line).toEqual({ state: "up", moving: true })
})

test("the page opens on the first engine that is installed", () => {
  const all = (reads) => standings({ admin: true, ...reads })
  expect(defaultTab(all({ fail2ban: running, crowdsec: crowdsec(), suricata: suricata() }))).toBe(
    "fail2ban",
  )
  const noFail2ban = { available: false, running: false, jails: [] }
  expect(
    defaultTab(all({ fail2ban: noFail2ban, crowdsec: crowdsec(), suricata: suricata() })),
  ).toBe("crowdsec")
  expect(defaultTab(all({ fail2ban: noFail2ban, crowdsec: absent, suricata: suricata() }))).toBe(
    "suricata",
  )
  // None installed: fail2ban's own "not installed", where the first install is.
  expect(
    defaultTab(
      all({ fail2ban: noFail2ban, crowdsec: absent, suricata: suricata({ installed: false }) }),
    ),
  ).toBe("fail2ban")
  // A stopped engine is still the engine to open on.
  expect(
    defaultTab(
      all({
        fail2ban: { available: true, running: false, jails: [] },
        crowdsec: crowdsec(),
        suricata: suricata(),
      }),
    ),
  ).toBe("fail2ban")
})

test("the page waits for every engine before it picks a view, unless the address names one", () => {
  const waiting = standings({ admin: true, fail2ban: running })
  expect(defaultTab(waiting)).toBeUndefined()
  expect(shownTab(null, waiting)).toBeUndefined()
  expect(shownTab("suricata", waiting)).toBe("suricata")
  expect(shownTab("blocked", waiting)).toBe("blocked")
  // An address that names no view falls back to the default.
  const ready = standings({ admin: false, fail2ban: running, crowdsec: crowdsec() })
  expect(shownTab("jails", ready)).toBe("fail2ban")
  // A reader is never waiting on Suricata, which is not asked after for them.
  expect(defaultTab(ready)).toBe("fail2ban")
})

test("the page's state is the worst thing true of its engines", () => {
  const all = (reads) => posture(standings({ admin: true, ...reads }))
  expect(all({ fail2ban: running, crowdsec: crowdsec(), suricata: suricata() })).toEqual({
    tone: "running",
    label: "3 engines watching",
  })
  expect(
    all({
      fail2ban: { available: true, running: false, jails: [] },
      crowdsec: crowdsec({ enforcement: { state: "unenforced" } }),
      suricata: suricata(),
    }),
  ).toEqual({ tone: "danger", label: "CrowdSec not enforcing" })
  expect(
    all({
      fail2ban: { available: true, running: false, jails: [] },
      crowdsec: crowdsec(),
      suricata: suricata(),
    }),
  ).toEqual({ tone: "warning", label: "fail2ban not running" })
  expect(
    all({
      fail2ban: { available: true, running: false, jails: [] },
      crowdsec: crowdsec(),
      suricata: suricata({ active: false }),
    }),
  ).toEqual({ tone: "warning", label: "2 engines not running" })
  expect(
    all({
      fail2ban: { available: false, running: false, jails: [] },
      crowdsec: absent,
      suricata: suricata({ installed: false }),
    }),
  ).toEqual({ tone: "stopped", label: "No engine installed" })
  expect(all({ fail2ban: running })).toBeUndefined()
  expect(
    all({
      fail2ban: running,
      crowdsec: absent,
      suricata: suricata({ installed: false }),
    }),
  ).toEqual({ tone: "running", label: "1 engine watching" })
})

test("a stopped fail2ban is a thing to start; a running, silent one is not", () => {
  expect(fail2banFault({ activeState: "inactive", subState: "dead" })).toMatchObject({
    title: "fail2ban is stopped",
    startable: true,
  })
  expect(fail2banFault({ activeState: "failed", subState: "failed" })).toMatchObject({
    title: "fail2ban stopped on an error",
    startable: true,
  })
  expect(fail2banFault({ activeState: "active", subState: "running" })).toMatchObject({
    title: "fail2ban is running and not answering",
    startable: false,
  })
  expect(fail2banFault(undefined)).toMatchObject({
    title: "fail2ban is not answering",
    startable: false,
  })
})

test("a country code is its flag, and anything else is nothing", () => {
  expect(flagOf("nl")).toBe("🇳🇱")
  expect(flagOf("CN")).toBe("🇨🇳")
  expect(flagOf("")).toBeUndefined()
  expect(flagOf("NLD")).toBeUndefined()
  expect(flagOf(undefined)).toBeUndefined()
})

test("an engine whose read failed is not read, and the page stops waiting on it", () => {
  const all = standings({
    admin: true,
    fail2ban: running,
    crowdsec: undefined,
    suricata: suricata(),
    failed: { crowdsec: true },
  })
  expect(all[1]).toMatchObject({ kind: "unread", word: "not read", line: { state: "down" } })
  expect(posture(all)).toEqual({ tone: "warning", label: "CrowdSec not read" })
  expect(defaultTab(all)).toBe("fail2ban")
  // With nothing else installed, the unread engine is the one to open: its view says why.
  const only = standings({
    admin: true,
    fail2ban: { available: false, running: false, jails: [] },
    crowdsec: undefined,
    suricata: suricata({ installed: false }),
    failed: { crowdsec: true },
  })
  expect(defaultTab(only)).toBe("crowdsec")
  // A read that has answered since is believed over the failure before it.
  expect(standings({ admin: true, fail2ban: running, failed: { fail2ban: true } })[0].kind).toBe(
    "watching",
  )
})
