"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { CheckCircle, ListOrdered, Play, RefreshClockwise, Stop } from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { duration } from "@/lib/format"
import type { ProxyValidation, SystemdUnit } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { engineLabel, engineUnit, type ProxyStatus } from "@/components/proxy/proxy-context"

/**
 * The engine itself: whether it is running, and the three things done to it.
 *
 * Every other page in this section writes files the engine reads; none of
 * them said whether it was up, and a reload that failed because nginx was
 * not running at all reported "invalid PID" and left the operator to guess.
 * The service unit is read through the same route the Services page uses,
 * so the two never disagree about what "running" means.
 */
export function useEngineUnit(status: ProxyStatus | undefined) {
  const name = engineUnit(status)
  const poll = usePoll(
    async (signal) => {
      const r = await get<{ unit: SystemdUnit }>(`/systemd/${name}`, undefined, signal)
      // Read once per poll rather than during render, which has to stay
      // pure: the uptime shown is as of the last fetch, which is the truth.
      return { unit: r.unit, fetchedAt: Date.now() }
    },
    30_000,
    [name],
    { enabled: Boolean(name) },
  )
  const unit = poll.data?.unit
  // systemd answers `not-found` for a unit it has never seen rather than
  // failing, so a host that runs nginx from a container or a nohup has no
  // unit and gets no controls, instead of controls that cannot work.
  const known = Boolean(unit && unit.loadState === "loaded")
  return {
    name,
    unit: known ? unit : undefined,
    fetchedAt: poll.data?.fetchedAt,
    refresh: poll.refresh,
  }
}

/** The one-line reading beside the engine's name. */
export function EngineStatus({
  unit,
  fetchedAt,
}: {
  unit: SystemdUnit | undefined
  /** When the unit was last read, so the uptime is computed without a clock read during render. */
  fetchedAt?: number
}) {
  if (!unit) return null
  const running = unit.activeState === "active"
  const since = unit.activeSince && fetchedAt ? Math.max(0, fetchedAt / 1000 - unit.activeSince) : 0
  return (
    <Status
      state={unit.activeState}
      label={
        running
          ? `running${since > 60 ? ` for ${duration(since)}` : ""}`
          : `${unit.activeState}${unit.subState && unit.subState !== unit.activeState ? ` (${unit.subState})` : ""}`
      }
    />
  )
}

/**
 * What this host's proxy is, as data: the engine and its version, the
 * directory it reads, whether certbot is here, who renews. The page header's
 * description slot used to carry a sentence about the section; these are the
 * facts a person opens the page to check.
 */
export function EngineFacts({
  status,
  unit,
  fetchedAt,
  certbotVersion,
  renewSource,
}: {
  status: ProxyStatus
  unit: SystemdUnit | undefined
  fetchedAt?: number
  certbotVersion?: string
  renewSource?: string | null
}) {
  const engine = status.nginx || status.caddy
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-body text-muted-foreground">
      <span className={engine ? "text-foreground" : undefined}>{engineLabel(status)}</span>
      {unit && (
        <>
          <Dot />
          <EngineStatus unit={unit} fetchedAt={fetchedAt} />
        </>
      )}
      {engine && (
        <>
          <Dot />
          <Tag mono>{status.nginx ? status.nginxDir : status.caddyFile}</Tag>
        </>
      )}
      {status.ingressContainer && (
        <>
          <Dot />
          <span>
            ingress <Tag mono>{status.ingressContainer}</Tag>
          </span>
        </>
      )}
      <Dot />
      <span>{status.certbot ? (certbotVersion ?? "certbot") : "no certbot"}</span>
      {renewSource !== undefined && status.certbot && (
        <>
          <Dot />
          <span>{renewSource ? `renews via ${renewSource}` : "renewal not scheduled"}</span>
        </>
      )}
    </div>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}

/**
 * Test, reload, and — behind the menu — restart, start and stop. Test and
 * reload are the daily two and stand inline; the rest are words with a
 * sentence, and the two that take every site offline for a moment confirm
 * first, through the same dialog the Services page uses for the same unit.
 */
export function EngineActions({
  status,
  unitName,
  unit,
  onChanged,
}: {
  status: ProxyStatus
  unitName: string | undefined
  unit: SystemdUnit | undefined
  onChanged: () => void
}) {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState<"test" | "reload" | "">("")
  const kind = status.nginx ? "nginx" : "caddy"
  const engine = status.nginx ? "nginx" : "Caddy"

  const test = async () => {
    setBusy("test")
    try {
      const res = await post<ProxyValidation>("/proxy/test", { kind })
      if (res.valid) {
        notify.success(`${engine}'s configuration is valid`, {
          description: "A reload would succeed.",
        })
      } else {
        notify.error(`${engine} refuses its configuration`, undefined, {
          description: res.output.slice(0, 400),
        })
      }
    } catch (err) {
      notify.error("Could not test the configuration", err)
    } finally {
      setBusy("")
    }
  }

  const reload = async () => {
    setBusy("reload")
    try {
      await post("/proxy/reload", { kind })
      notify.success(`${engine} reloaded`)
      onChanged()
    } catch (err) {
      notify.error("Reload refused", err)
    } finally {
      setBusy("")
    }
  }

  const control = (action: "restart" | "start" | "stop") =>
    post(`/systemd/${unitName}/${action}`).then(() => onChanged())

  const running = unit?.activeState === "active"
  const verbs: Verb[] = []
  if (unitName && unit) {
    if (!running) {
      verbs.push({
        key: "start",
        label: `Start ${engine}`,
        detail: "It is not running, which is why nothing is being served.",
        icon: Play,
        run: () =>
          control("start")
            .then(() => notify.success(`${engine} started`))
            .catch((err) => notify.error("Could not start", err)),
      })
    }
    verbs.push({
      key: "restart",
      label: `Restart ${engine}`,
      detail:
        "A full stop and start. Every site is briefly unreachable; a reload is usually enough.",
      icon: RefreshClockwise,
      danger: true,
      run: () =>
        confirm({
          title: `Restart ${engine}`,
          confirmLabel: "Restart",
          description: (
            <p>
              Every connection is dropped and every site is unreachable until the process is back. A
              reload applies configuration changes without either.
            </p>
          ),
          action: () => control("restart"),
        }),
    })
    if (running) {
      verbs.push({
        key: "stop",
        label: `Stop ${engine}`,
        detail: "Takes every site on this host offline until it is started again.",
        icon: Stop,
        danger: true,
        run: () =>
          confirm({
            title: `Stop ${engine}`,
            confirmLabel: "Stop",
            description: (
              <p>Every site this proxy serves goes offline until it is started again.</p>
            ),
            action: () => control("stop"),
          }),
      })
    }
    verbs.push({
      key: "unit",
      label: "Service details",
      detail: "The unit's journal, restarts and startup setting, under Processes.",
      icon: ListOrdered,
      run: () => router.push(`/processes/services?unit=${encodeURIComponent(unitName)}`),
    })
  }

  return (
    <>
      <Button
        size="sm"
        variant="outline"
        onClick={test}
        pending={busy === "test"}
        disabled={busy !== ""}
      >
        <CheckCircle className="size-3.5" />
        Test config
      </Button>
      <Button size="sm" onClick={reload} pending={busy === "reload"} disabled={busy !== ""}>
        <RefreshClockwise className="size-3.5" />
        Reload
      </Button>
      {verbs.length > 0 && <VerbMenu verbs={verbs} label={`More ${engine} actions`} />}
      {dialog}
    </>
  )
}
