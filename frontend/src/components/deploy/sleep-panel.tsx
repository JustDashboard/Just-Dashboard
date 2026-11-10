"use client"

import { useState } from "react"
import { get, post } from "@/lib/api"
import { percent } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { SleepStatus, WakeOutcome } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { WireMark, WireNode } from "@/components/deploy/wire"
import {
  asleepShare,
  sleepLead,
  sleepShown,
  sleepWeek,
  spanLength,
} from "@/components/deploy/sleep-model"

/** A stretch asleep on the week's strip: the calm hue, never a state's. */
const ASLEEP_HUE = "var(--tag-violet)"

/**
 * An environment asleep by design (`deploy/sleep.go`): its container was
 * stopped because nobody had visited for the plan's idle minutes, and the
 * next request starts it. It takes the shape every block on the Overview does
 * (§16): Docker's mark, since a stopped container is the evidence, with the
 * state as one sentence — asleep on purpose, and for how long — over why, what
 * the next visitor meets and what woke it last; then one picture, the last
 * seven days as a strip with each stretch asleep on it. Its one verb is Wake
 * now. It was four figures in a row on the page the operator asked to carry no
 * metric cards. Drawn only while it is asleep or waking; an awake environment
 * that sleeps says so in its Runtime settings, not here.
 */
export function SleepPanel({
  projectId,
  environmentId,
  status,
  onChange,
}: {
  projectId: number
  environmentId: number
  status: SleepStatus
  /** Called after Wake now lands, so the page's own reads catch up. */
  onChange?: () => void
}) {
  const { can } = useAuth()
  const [waking, setWaking] = useState(false)
  const wakingNow = waking || status.state === "waking"

  const wake = async () => {
    setWaking(true)
    try {
      const answer = await post<{ outcome: WakeOutcome; sleep: SleepStatus }>(
        `/deploy/${projectId}/environments/${environmentId}/sleep/wake`,
        {},
      )
      notify.success("Awake", { description: answer.outcome.reason })
      onChange?.()
    } catch (error) {
      notify.error("Could not wake it", error)
    } finally {
      setWaking(false)
    }
  }

  const lead = sleepLead(status, wakingNow)
  const week = sleepWeek(status)
  const share = `Asleep ${spanLength(status.asleepSeconds)} of the last 7 days, ${percent(asleepShare(status) * 100, 0)}`
  return (
    <Panel plain data-testid="sleep-block">
      <PanelHeader
        title={wakingNow ? "Waking up" : "Asleep"}
        actions={
          can("service.control") && (
            <Button
              variant="outline"
              size="sm"
              pending={wakingNow}
              disabled={wakingNow}
              onClick={() => void wake()}
            >
              Wake now
            </Button>
          )
        }
      />
      <PanelBody className="space-y-4 pt-4">
        <WireNode
          mark={
            <span className="relative flex">
              <WireMark tone="logo" size="md">
                <ProductGlyph id="docker" />
              </WireMark>
              <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
                <StatusDot tone="notice" live={wakingNow} />
              </span>
            </span>
          }
          title={lead.title}
          hint={
            <span key={lead.hint} className="block animate-rise">
              {lead.hint}
            </span>
          }
        />
        <div className="min-w-0">
          <div
            role="img"
            aria-label={share}
            data-testid="sleep-week"
            className="relative h-2.5 animate-rise overflow-hidden rounded-sm bg-meter-track"
          >
            {week.map((stretch) => (
              <span
                key={stretch.key}
                className="absolute inset-y-0"
                style={{
                  left: `${stretch.left * 100}%`,
                  width: `${stretch.width * 100}%`,
                  minWidth: 3,
                  background: ASLEEP_HUE,
                }}
              />
            ))}
          </div>
          <div className="mt-2 flex min-h-5 min-w-0 items-baseline justify-between gap-x-4 text-hint text-muted-foreground">
            <p className="numeric flex min-w-0 items-center gap-1.5">
              <span
                aria-hidden
                className="size-2 shrink-0 rounded-full"
                style={{ background: ASLEEP_HUE }}
              />
              <span className="truncate">{share}</span>
            </p>
            <span className="numeric shrink-0 text-micro max-sm:hidden">7 days ago to now</span>
          </div>
        </div>
      </PanelBody>
    </Panel>
  )
}

/**
 * The environment's sleep, read on a ten-second poll while the deployment
 * says it is asleep: the summary already carries that, so an awake
 * environment costs no read.
 */
export function useSleep(projectId: number, environmentId: number, enabled: boolean) {
  const base = `/deploy/${projectId}/environments/${environmentId}/sleep`
  const read = usePoll((signal) => get<SleepStatus>(base, undefined, signal), 10000, [base], {
    enabled,
  })
  const status = read.data ?? undefined
  return { status, shown: sleepShown(status), refresh: read.refresh }
}
