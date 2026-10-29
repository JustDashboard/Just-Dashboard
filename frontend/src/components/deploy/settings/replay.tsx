"use client"

import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { Textarea } from "@/components/ui/textarea"
import {
  REPLAY_DEFAULTS,
  REPLAY_LIMITS,
  replayPlanOf,
  replayPolicyLine,
  type ReplayDraft,
} from "@/components/deploy/replay-model"
import { SettingSection } from "@/components/deploy/settings/setting-card"

/** The Runtime form's field ids for the replay's terms, as the server's refusal names them. */
export const REPLAY_FIELD_IDS: Record<string, string> = {
  "runtime.replay.requests": "runtime-replay-requests",
  "runtime.replay.paths": "runtime-replay-paths",
  "runtime.replay.maxStatusDrift": "runtime-replay-drift",
  "runtime.replay.latencyFactor": "runtime-replay-factor",
  "runtime.replay.timeoutSeconds": "runtime-replay-timeout",
}

/** The same ids by the draft's own fields, for a refusal the form makes before saving. */
export const REPLAY_DRAFT_FIELD_IDS: Record<keyof ReplayDraft, string> = {
  enabled: "runtime-replay-requests",
  requests: "runtime-replay-requests",
  paths: "runtime-replay-paths",
  maxStatusDrift: "runtime-replay-drift",
  latencyFactor: "runtime-replay-factor",
  timeoutSeconds: "runtime-replay-timeout",
}

type NumberTerm = "requests" | "maxStatusDrift" | "latencyFactor" | "timeoutSeconds"

/**
 * Traffic replay before the cutover: the last distinct GET and HEAD requests
 * the access log recorded for the environment's domains are sent to the
 * candidate, and a candidate that answers them in other status classes, or
 * slower, than the live release did is stopped before any traffic moves. It
 * is a section of the Runtime form because the terms are the plan's
 * (`runtime.replay`) and go out in the same save.
 */
export function ReplaySection({
  draft,
  onChange,
  canEdit,
  status,
  errorFor,
  servesDomain,
  stopFirst,
}: {
  draft: ReplayDraft
  onChange: (fields: Partial<ReplayDraft>) => void
  canEdit: boolean
  status: React.ReactNode
  errorFor: (id: string) => string | undefined
  /** Whether the plan has a domain that serves the application, whose requests are recorded. */
  servesDomain: boolean
  stopFirst: boolean
}) {
  const enabled = draft.enabled
  // What a deployment would run with, read off the draft as it would save.
  const line = replayPolicyLine(replayPlanOf(draft))
  const number = (
    id: string,
    term: NumberTerm,
    unit: string,
    props: { min: number; max: number; step?: number },
  ) => (
    <InputGroup>
      <InputGroupInput
        id={id}
        type="number"
        min={props.min}
        max={props.max}
        step={props.step}
        value={draft[term]}
        readOnly={!canEdit}
        aria-invalid={Boolean(errorFor(id))}
        className="font-mono"
        placeholder={String(REPLAY_DEFAULTS[term])}
        onChange={(event) => onChange({ [term]: event.target.value })}
      />
      <InputGroupAddon align="inline-end">
        <InputGroupText>{unit}</InputGroupText>
      </InputGroupAddon>
    </InputGroup>
  )

  return (
    <SettingSection
      id="replay"
      title="Traffic replay"
      state={
        <span key={line} className="block animate-rise">
          {line}
        </span>
      }
      status={status}
    >
      <OptionList>
        <OptionRow
          title="Replay recorded requests before the cutover"
          hint="The last distinct GET and HEAD requests visitors sent, as the access log recorded them, are sent to the new release at its own address — with the recorded Host, and without cookies, credentials or bodies. A release that answers them in other status classes, or slower, than the live release did is stopped before any traffic moves to it."
          checked={enabled}
          onCheckedChange={(on) => onChange({ enabled: on })}
          disabled={!canEdit}
        >
          <div className="space-y-4">
            <FieldRow>
              <Field
                label="Requests replayed"
                htmlFor="runtime-replay-requests"
                info="Distinct requests, newest first, from the last day of the record. Writes, failed answers (5xx) and answers decided by who asked (401, 403) are never replayed, nor is a request whose query carries a token."
                error={errorFor("runtime-replay-requests")}
              >
                {number("runtime-replay-requests", "requests", "requests", {
                  min: 1,
                  max: REPLAY_LIMITS.requests,
                })}
              </Field>
              <Field
                label="Status drift allowed"
                htmlFor="runtime-replay-drift"
                info="The share of replayed requests the new release may answer in another status class — a 404 where the live release answered 200, a redirect where it answered a page. A request it does not answer in time counts as drifted."
                error={errorFor("runtime-replay-drift")}
              >
                {number("runtime-replay-drift", "maxStatusDrift", "%", {
                  min: 0,
                  max: REPLAY_LIMITS.maxStatusDrift,
                  step: 0.1,
                })}
              </Field>
            </FieldRow>
            <FieldRow>
              <Field
                label="p95 against the live release"
                htmlFor="runtime-replay-factor"
                info="A multiple of the recorded p95 over the same requests, and always at least a quarter of a second slower, so 4 ms becoming 9 ms is not a regression. An nginx ingress records no durations, so there only the status classes decide."
                error={errorFor("runtime-replay-factor")}
              >
                {number("runtime-replay-factor", "latencyFactor", "×", {
                  min: 1,
                  max: REPLAY_LIMITS.latencyFactor,
                  step: 0.1,
                })}
              </Field>
              <Field
                label="Time limit"
                htmlFor="runtime-replay-timeout"
                info="How long the whole replay may take, four requests at a time. A request not answered by then counts as drifted."
                error={errorFor("runtime-replay-timeout")}
              >
                {number("runtime-replay-timeout", "timeoutSeconds", "seconds", {
                  min: 1,
                  max: REPLAY_LIMITS.timeoutSeconds,
                })}
              </Field>
            </FieldRow>
            <Field
              label="Only paths under"
              htmlFor="runtime-replay-paths"
              info="Path prefixes, one per line. Empty replays every path. Keep to the public part of a site whose other pages answer by who is signed in: replayed as nobody, those answer a sign-in redirect and would read as drift."
              error={errorFor("runtime-replay-paths")}
            >
              <Textarea
                id="runtime-replay-paths"
                value={draft.paths}
                onChange={(event) => onChange({ paths: event.target.value })}
                readOnly={!canEdit}
                aria-invalid={Boolean(errorFor("runtime-replay-paths"))}
                placeholder={"/\n/docs"}
                className="min-h-16 font-mono sm:text-xs"
              />
            </Field>
          </div>
        </OptionRow>
      </OptionList>
      {enabled && !servesDomain && (
        <FormNote tone="warning" className="animate-rise">
          No domain serves this application, so the proxy records no requests for it and every
          deployment skips the replay.
        </FormNote>
      )}
      {enabled && servesDomain && stopFirst && (
        <FormNote className="animate-rise">
          This environment releases stop first, so the replay runs while the site is down, before
          the new release is routed — a stopped release starts the previous one again.
        </FormNote>
      )}
    </SettingSection>
  )
}
