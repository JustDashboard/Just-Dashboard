import { plural } from "@/lib/format"
import type { CrowdSecView, Fail2banJail, SuricataView } from "@/lib/types"
import type { DotTone } from "@/components/status-dot"
import type { WireLine } from "@/components/wire-flow-model"
import { ENFORCEMENT, enforcementOf } from "@/components/security/enforcement"

/**
 * What the Intrusion page says about its three engines before it draws
 * anything: which of them is there, what each is doing in a word, which view
 * the page opens on, and the one state the identity line carries. Kept apart
 * from React so it is tested in `bun test src`.
 */

export type EngineId = "fail2ban" | "crowdsec" | "suricata"
export type IntrusionTab = EngineId | "blocked"

export const ENGINES: readonly EngineId[] = ["fail2ban", "crowdsec", "suricata"]
export const INTRUSION_TABS: readonly IntrusionTab[] = [...ENGINES, "blocked"]

export const ENGINE_NAME: Record<EngineId, string> = {
  fail2ban: "fail2ban",
  crowdsec: "CrowdSec",
  suricata: "Suricata",
}

export type Fail2banStatus = {
  available: boolean
  running: boolean
  jails: Fail2banJail[]
  error?: string
}

/** The three reads, each absent until it has answered. */
export type EngineReads = {
  fail2ban?: Fail2banStatus
  crowdsec?: CrowdSecView
  suricata?: SuricataView
  /** Suricata's read is an administrator's: without it the engine is not asked after. */
  admin: boolean
  /** The engines whose read failed: answered, and not with what the engine holds. */
  failed?: Partial<Record<EngineId, boolean>>
}

export type EngineStanding = {
  id: EngineId
  name: string
  /**
   * `absent` is not installed, `stopped` is installed and not running,
   * `failing` runs and protects nothing, `watching` is doing its job,
   * `closed` is a read this account may not make, `unread` is a read that
   * failed, and `unknown` has not answered yet.
   */
  kind: "unknown" | "unread" | "closed" | "absent" | "stopped" | "failing" | "watching"
  tone: DotTone
  /** The state as the word a `Status` carries. */
  word: string
  /** What it holds right now: "2 banned now", "8 decisions", "214 alerts". */
  count?: string
  /** The line between this server and the engine, on the page's picture. */
  line: WireLine
}

const VERDICT_TONE = {
  ok: "running",
  notice: "notice",
  warning: "warning",
  critical: "danger",
} as const

export function fail2banStanding(status: Fail2banStatus | undefined): EngineStanding {
  const base = { id: "fail2ban" as const, name: ENGINE_NAME.fail2ban }
  if (!status)
    return { ...base, kind: "unknown", tone: "unknown", word: "reading", line: { state: "up" } }
  if (!status.available)
    return {
      ...base,
      kind: "absent",
      tone: "unknown",
      word: "not installed",
      line: { state: "idle" },
    }
  if (!status.running)
    return {
      ...base,
      kind: "stopped",
      tone: "warning",
      word: "not running",
      line: { state: "down" },
    }
  const banned = status.jails.reduce((n, jail) => n + jail.currentlyBanned, 0)
  const failing = status.jails.reduce((n, jail) => n + jail.currentlyFailed, 0)
  return {
    ...base,
    kind: "watching",
    tone: "running",
    word: "running",
    count: `${banned.toLocaleString()} banned now`,
    // Something is in flight while an address is inside a jail's window.
    line: { state: "up", moving: failing > 0 },
  }
}

export function crowdsecStanding(view: CrowdSecView | undefined): EngineStanding {
  const base = { id: "crowdsec" as const, name: ENGINE_NAME.crowdsec }
  if (!view)
    return { ...base, kind: "unknown", tone: "unknown", word: "reading", line: { state: "up" } }
  if (!view.installed)
    return {
      ...base,
      kind: "absent",
      tone: "unknown",
      word: "not installed",
      line: { state: "idle" },
    }
  const state = enforcementOf(view)
  const words = ENFORCEMENT[state]
  const count = plural(view.decisions.length, "decision")
  if (state === "stopped")
    return {
      ...base,
      kind: "stopped",
      tone: "warning",
      word: words.label,
      count,
      line: { state: "down" },
    }
  if (words.verdict === "critical")
    return {
      ...base,
      kind: "failing",
      tone: "danger",
      word: words.label,
      count,
      line: { state: "danger" },
    }
  return {
    ...base,
    kind: "watching",
    tone: VERDICT_TONE[words.verdict],
    word: words.label,
    count,
    line:
      words.verdict === "ok"
        ? { state: "up", moving: view.decisions.length > 0 }
        : { state: "warning" },
  }
}

export function suricataStanding(view: SuricataView | undefined, admin: boolean): EngineStanding {
  const base = { id: "suricata" as const, name: ENGINE_NAME.suricata }
  if (!admin)
    return {
      ...base,
      kind: "closed",
      tone: "unknown",
      word: "administrators only",
      line: { state: "idle" },
    }
  if (!view)
    return { ...base, kind: "unknown", tone: "unknown", word: "reading", line: { state: "up" } }
  if (!view.installed)
    return {
      ...base,
      kind: "absent",
      tone: "unknown",
      word: "not installed",
      line: { state: "idle" },
    }
  const count = `${plural(view.scanned, "alert")} scanned`
  if (!view.active)
    return {
      ...base,
      kind: "stopped",
      tone: "warning",
      word: "not running",
      count,
      line: { state: "down" },
    }
  const seeing = view.capture
    ? view.capture.kernelPackets > 0 || view.capture.decoderPackets > 0
    : false
  return {
    ...base,
    kind: "watching",
    tone: "running",
    word: view.mode === "ips" ? "can drop" : "only reads",
    count,
    line: { state: "up", moving: seeing },
  }
}

/** An engine whose read failed: it is not known to be absent, stopped or working. */
function unread(standing: EngineStanding): EngineStanding {
  return {
    id: standing.id,
    name: standing.name,
    kind: "unread",
    tone: "warning",
    word: "not read",
    line: { state: "down" },
  }
}

export function standings(reads: EngineReads): EngineStanding[] {
  const all = [
    fail2banStanding(reads.fail2ban),
    crowdsecStanding(reads.crowdsec),
    suricataStanding(reads.suricata, reads.admin),
  ]
  return all.map((standing) =>
    standing.kind === "unknown" && reads.failed?.[standing.id] ? unread(standing) : standing,
  )
}

/**
 * The view an address without `?tab=` opens on: the first engine that is
 * installed, in the page's order, and fail2ban's own "not installed" when
 * none is. Nothing until every engine that is asked after has answered, so
 * the page does not open on one engine and move to another.
 */
export function defaultTab(all: EngineStanding[]): IntrusionTab | undefined {
  if (all.some((engine) => engine.kind === "unknown")) return undefined
  const present = all.find(
    (engine) => engine.kind !== "absent" && engine.kind !== "closed" && engine.kind !== "unknown",
  )
  return present?.id ?? "fail2ban"
}

/** The view the page shows: what the address asks for where it is a view, else the default. */
export function shownTab(
  requested: string | null | undefined,
  all: EngineStanding[],
): IntrusionTab | undefined {
  if (requested && (INTRUSION_TABS as readonly string[]).includes(requested))
    return requested as IntrusionTab
  return defaultTab(all)
}

/**
 * The page's one state, the worst thing true of its engines: one that runs
 * and protects nothing, then one that is stopped, then none at all, and
 * otherwise how many are watching. Nothing until every engine has answered.
 */
export function posture(all: EngineStanding[]): { tone: DotTone; label: string } | undefined {
  if (all.some((engine) => engine.kind === "unknown")) return undefined
  const failing = all.filter((engine) => engine.kind === "failing")
  if (failing.length > 0) return { tone: "danger", label: `${failing[0].name} ${failing[0].word}` }
  const unreadable = all.filter((engine) => engine.kind === "unread")
  if (unreadable.length > 0)
    return {
      tone: "warning",
      label:
        unreadable.length === 1
          ? `${unreadable[0].name} not read`
          : `${unreadable.length} engines not read`,
    }
  const stopped = all.filter((engine) => engine.kind === "stopped")
  if (stopped.length === 1) return { tone: "warning", label: `${stopped[0].name} not running` }
  if (stopped.length > 1) return { tone: "warning", label: `${stopped.length} engines not running` }
  const watching = all.filter((engine) => engine.kind === "watching")
  if (watching.length === 0) return { tone: "stopped", label: "No engine installed" }
  return { tone: "running", label: `${plural(watching.length, "engine")} watching` }
}

/**
 * Why fail2ban's status could not be read, from its systemd unit where that
 * could be asked: stopped is a thing to start, running and silent is a thing
 * to look into. The client's own error is evidence, never the headline.
 */
export function fail2banFault(unit: { activeState: string; subState: string } | undefined): {
  title: string
  description: string
  startable: boolean
} {
  if (!unit)
    return {
      title: "fail2ban is not answering",
      description: "Nothing is banning repeated failures until it does.",
      startable: false,
    }
  if (unit.activeState === "active" || unit.activeState === "activating")
    return {
      title: "fail2ban is running and not answering",
      description: "Its service is up and its client got no answer. The log under this says why.",
      startable: false,
    }
  return {
    title: unit.activeState === "failed" ? "fail2ban stopped on an error" : "fail2ban is stopped",
    description: "Nothing is banning repeated failures while it is.",
    startable: true,
  }
}

/** The flag a two-letter country code spells in regional indicators, where it is one. */
export function flagOf(code?: string) {
  if (!code || !/^[A-Za-z]{2}$/.test(code)) return undefined
  return String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65))
}
