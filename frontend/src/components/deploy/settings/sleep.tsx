"use client"

import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import {
  SLEEP_DEFAULTS,
  SLEEP_LIMITS,
  holdSeconds,
  sleepPlanOf,
  sleepPolicyLine,
  type SleepDraft,
} from "@/components/deploy/sleep-model"
import { SettingSection } from "@/components/deploy/settings/setting-card"

/** The Runtime form's field ids for the sleep terms, as the server's refusal names them. */
export const SLEEP_FIELD_IDS: Record<string, string> = {
  "runtime.sleep.enabled": "runtime-sleep-enabled",
  "runtime.sleep.idleMinutes": "runtime-sleep-idle",
  "runtime.sleep.wakeTimeoutSeconds": "runtime-sleep-wake",
}

/** The same ids by the draft's own fields, for a refusal the form makes before saving. */
export const SLEEP_DRAFT_FIELD_IDS: Record<keyof SleepDraft, string> = {
  enabled: "runtime-sleep-enabled",
  idleMinutes: "runtime-sleep-idle",
  wakeTimeoutSeconds: "runtime-sleep-wake",
}

/**
 * Why the plan as it stands could not sleep, in the server's terms
 * (`sleepRefusal` in `deploy/sleep.go`), said beside the switch before a
 * save is refused for it.
 */
export function sleepBlocker({
  servesDomain,
  compose,
  hostNetwork,
  replicas = 1,
  canary = false,
  skewProtection = false,
  blockingCheck,
}: {
  servesDomain: boolean
  compose: boolean
  hostNetwork: boolean
  /** The release's replicas: a wake starts and holds the request for one. */
  replicas?: number
  /** The plan releases as a canary, which puts a second release on the route. */
  canary?: boolean
  /** The saved plan keeps the previous release on the route for its visitors. */
  skewProtection?: boolean
  blockingCheck?: { name: string; kind: string }
}) {
  if (compose)
    return "A Compose stack is several containers that start in an order of their own; sleeping stops one container, so it is offered for a single-container release."
  if (skewProtection)
    return "Skew protection keeps the previous release on the route for its visitors, and a wake starts one container and holds the request for it. Turn it off under After a release goes live to let the application sleep."
  if (canary)
    return "A canary puts a second release on the route beside the live one, and a wake starts one container and holds the request for it; sleeping is offered for a blue / green or stop-first release."
  if (replicas > 1)
    return `A wake starts one container and holds the request for it, and this release runs ${replicas} replicas; sleeping is offered for a single-container release.`
  if (hostNetwork)
    return "A host-network application is reached at the host's own port, not through the proxy's route, so no request could wake it."
  if (!servesDomain)
    return "Only a request through the proxy's route wakes a sleeping application, and no domain serves this one. Add one under Domains."
  if (blockingCheck?.kind === "docker_health")
    return `The readiness check “${blockingCheck.name}” reads Docker's health verdict, which a started container is given only after its image's HEALTHCHECK interval — 30 seconds unless the image says otherwise — so every wake would outlast what a visitor waits for. Use an HTTP or TCP readiness check.`
  if (blockingCheck?.kind === "public_route")
    return `The readiness check “${blockingCheck.name}” asks the public route, which while the application sleeps answers by waking it, so the wake would wait on itself. Use an HTTP or TCP readiness check.`
  return undefined
}

/**
 * Scale to zero: the live release's container is stopped once its route has
 * served no request for the idle minutes — stopped, not removed — and the
 * next request is held while it starts and passes its readiness checks, then
 * answered by it. It is a section of the Runtime form because the terms are
 * the plan's (`runtime.sleep`) and go out in the same save, live with the
 * next release.
 */
export function SleepSection({
  draft,
  onChange,
  canEdit,
  status,
  errorFor,
  blocker,
}: {
  draft: SleepDraft
  onChange: (fields: Partial<SleepDraft>) => void
  canEdit: boolean
  status: React.ReactNode
  errorFor: (id: string) => string | undefined
  /** Why this plan could not sleep, or nothing. */
  blocker?: string
}) {
  const plan = sleepPlanOf(draft)
  const line = sleepPolicyLine(plan)
  const hold = holdSeconds(plan)
  const number = (
    id: string,
    term: "idleMinutes" | "wakeTimeoutSeconds",
    unit: string,
    props: { min: number; max: number },
  ) => (
    <InputGroup>
      <InputGroupInput
        id={id}
        type="number"
        min={props.min}
        max={props.max}
        step={1}
        value={draft[term]}
        readOnly={!canEdit}
        aria-invalid={Boolean(errorFor(id))}
        className="font-mono"
        placeholder={String(SLEEP_DEFAULTS[term])}
        onChange={(event) => onChange({ [term]: event.target.value })}
      />
      <InputGroupAddon align="inline-end">
        <InputGroupText>{unit}</InputGroupText>
      </InputGroupAddon>
    </InputGroup>
  )

  return (
    <SettingSection
      id="sleep"
      title="Sleep when idle"
      state={
        <span key={line} className="block animate-rise">
          {line}
        </span>
      }
      status={status}
    >
      <OptionList>
        <OptionRow
          title="Stop the container while nobody visits"
          hint="Once the route has served no request for the idle time, the container is stopped — not removed: its image, volumes and network stay. The next request is held while it starts and passes its readiness checks, then reaches it as it was sent, so a visitor gets the page after a pause rather than an error."
          checked={draft.enabled}
          onCheckedChange={(on) => onChange({ enabled: on })}
          disabled={!canEdit}
        >
          <div className="space-y-4">
            <FieldRow>
              <Field
                label="Idle time"
                htmlFor="runtime-sleep-idle"
                info="Counted from the last request the proxy recorded for the environment's domains, from the release going live, or from its last wake, whichever is latest. Every request counts, a crawler's included."
                error={errorFor("runtime-sleep-idle")}
              >
                {number("runtime-sleep-idle", "idleMinutes", "minutes", {
                  min: SLEEP_LIMITS.minIdleMinutes,
                  max: SLEEP_LIMITS.maxIdleMinutes,
                })}
              </Field>
              <Field
                label="Wake limit"
                htmlFor="runtime-sleep-wake"
                info={`How long a wake waits for the release's readiness checks. A visitor is held for up to ${SLEEP_LIMITS.holdSeconds} seconds of it; one who would wait longer gets a waking page that tries again by itself.`}
                error={errorFor("runtime-sleep-wake")}
              >
                {number("runtime-sleep-wake", "wakeTimeoutSeconds", "seconds", {
                  min: SLEEP_LIMITS.minWakeTimeoutSeconds,
                  max: SLEEP_LIMITS.maxWakeTimeoutSeconds,
                })}
              </Field>
            </FieldRow>
          </div>
        </OptionRow>
      </OptionList>
      {errorFor("runtime-sleep-enabled") && (
        <FormNote tone="danger" className="animate-rise">
          {errorFor("runtime-sleep-enabled")}
        </FormNote>
      )}
      {draft.enabled && blocker && !errorFor("runtime-sleep-enabled") && (
        <FormNote tone="warning" className="animate-rise">
          {blocker}
        </FormNote>
      )}
      {draft.enabled && !blocker && (
        <FormNote className="animate-rise">
          A request while it sleeps waits up to {hold} s. Schedules that run a command inside the
          container cannot be on while it sleeps, and the overview offers Wake now.
        </FormNote>
      )}
    </SettingSection>
  )
}
