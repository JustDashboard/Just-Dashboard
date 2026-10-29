"use client"

import { useState } from "react"
import { ApiError, get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { RollbackPolicy, RollbackWatchResponse } from "@/lib/types"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import {
  DEFAULT_ROLLBACK_POLICY,
  policyLine,
  skewLine,
} from "@/components/deploy/rollback-watch-model"
import { useSettingDraft } from "@/components/deploy/settings/use-configuration"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"

/** The form's own shape: numbers as typed, so a field can be emptied. */
type PolicyDraft = {
  keepWarm: boolean
  automatic: boolean
  windowMinutes: string
  errorRateMargin: string
  latencyFactor: string
  minRequests: string
  /** The plan's `runtime.skewProtection`, saved with the plan. */
  skewProtection: boolean
}

function draftOf(policy: RollbackPolicy, skewProtection: boolean): PolicyDraft {
  return {
    skewProtection,
    keepWarm: policy.keepWarm,
    automatic: policy.automatic,
    windowMinutes: String(Math.max(1, Math.round(policy.windowSeconds / 60))),
    errorRateMargin: String(policy.errorRateMargin),
    latencyFactor: String(policy.latencyFactor),
    minRequests: String(policy.minRequests),
  }
}

function settingDigestOf(draft: PolicyDraft) {
  return JSON.stringify(draft)
}

/** The bounds the server holds the terms to (`validateRollbackPolicy`). */
function policyOf(draft: PolicyDraft): RollbackPolicy | string {
  const minutes = Number(draft.windowMinutes)
  const margin = Number(draft.errorRateMargin)
  const factor = Number(draft.latencyFactor)
  const requests = Number(draft.minRequests)
  if (!Number.isInteger(minutes) || minutes < 1 || minutes > 60)
    return "The window is between 1 and 60 minutes."
  if (!(margin >= 0.5 && margin <= 50)) return "The 5xx margin is between 0.5 and 50 points."
  if (!(factor >= 1.2 && factor <= 10)) return "The p95 factor is between 1.2 and 10."
  if (!Number.isInteger(requests) || requests < 5 || requests > 100000)
    return "At least 5 requests decide."
  return {
    keepWarm: draft.keepWarm,
    automatic: draft.keepWarm && draft.automatic,
    windowSeconds: minutes * 60,
    errorRateMargin: margin,
    latencyFactor: factor,
    minRequests: requests,
  }
}

/**
 * What happens to the release a blue/green cutover replaces: kept warm —
 * paused, holding its memory and nothing else — for a window in which the
 * new release is read against it, and switched back to automatically when
 * the new one fails where the old one did not. The section is its own form on
 * the page's save bar, since the terms are saved apart from the plan; they
 * apply from the next cutover, so a window already open keeps the terms it
 * opened with.
 *
 * Skew protection is the plan's (`runtime.skewProtection`) but is set here,
 * beside the warm window it lives in: it keeps the warm release running for
 * the visitors it answered first, for as long as the window lasts. It goes
 * out as the plan's own save, changing that one field of the latest copy.
 */
export function RollbackWatchForm({
  projectId,
  environmentId,
  blueGreen,
  skew,
}: {
  projectId: number
  environmentId: number
  /** Whether the saved plan releases blue/green, the only kind kept warm. */
  blueGreen: boolean
  skew: {
    /** The saved plan's switch. */
    saved: boolean
    /** Why the plan cannot have it, when it cannot. */
    refusal?: string
    save: (on: boolean) => Promise<void>
  }
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  const base = `/deploy/${projectId}/environments/${environmentId}`
  const read = usePoll(
    (signal) => get<RollbackWatchResponse>(`${base}/rollback-watch`, undefined, signal),
    60000,
    [base],
  )
  const saved = read.data?.policy ?? DEFAULT_ROLLBACK_POLICY
  const draft = useSettingDraft<PolicyDraft>(
    `deploy.${projectId}.settings.rollback`,
    draftOf(saved, skew.saved),
  )
  const value = draft.value
  const patch = (next: Partial<PolicyDraft>) => draft.set({ ...value, ...next })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const parsed = policyOf(value)
  const invalid = typeof parsed === "string"

  if (read.data?.preview) return null

  const save = async () => {
    if (typeof parsed === "string") {
      setError(parsed)
      return false
    }
    setSaving(true)
    setError(undefined)
    try {
      if (value.skewProtection !== skew.saved) await skew.save(value.skewProtection)
      const policyDirty =
        settingDigestOf({ ...value, skewProtection: false }) !==
        settingDigestOf(draftOf(saved, false))
      if (policyDirty) await put(`${base}/rollback-policy`, parsed)
      read.refresh()
      return true
    } catch (failure) {
      // A plan refusal names the switch, and is the form's sentence.
      if (failure instanceof ApiError && failure.field === "runtime.skewProtection") {
        setError(failure.message)
        return false
      }
      notify.error("Could not save the rollback terms", failure)
      setError(failure instanceof Error ? failure.message : String(failure))
      return false
    } finally {
      setSaving(false)
    }
  }

  const number = (
    id: string,
    field: keyof PolicyDraft,
    unit: string,
    props: React.ComponentProps<typeof InputGroupInput>,
  ) => (
    <InputGroup>
      <InputGroupInput
        id={id}
        type="number"
        value={String(value[field])}
        readOnly={!canEdit}
        className="font-mono"
        onChange={(event) => patch({ [field]: event.target.value })}
        {...props}
      />
      <InputGroupAddon align="inline-end">
        <InputGroupText>{unit}</InputGroupText>
      </InputGroupAddon>
    </InputGroup>
  )

  return (
    <SettingForm
      name="Rollback watch"
      onSave={save}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      invalid={invalid}
      canEdit={canEdit}
      onDiscard={draft.discard}
      error={error}
    >
      <SettingSection
        id="rollback-watch"
        title="After a release goes live"
        state={
          <span
            key={typeof parsed === "string" ? "invalid" : policyLine(parsed)}
            className="block animate-rise"
          >
            {typeof parsed === "string" ? parsed : policyLine(parsed)}
            {typeof parsed !== "string" && skewLine(value.skewProtection, parsed) && (
              <> {skewLine(value.skewProtection, parsed)}</>
            )}
          </span>
        }
        status={settingStatus({ dirty: draft.dirty })}
      >
        <OptionList>
          <OptionRow
            title="Keep the previous release warm"
            hint="Paused rather than removed, holding its memory and doing no work, so traffic can switch back to it in a moment with nothing rebuilt. A new deployment, or less than a tenth of the server's memory left, removes it early."
            checked={value.keepWarm}
            onCheckedChange={(keepWarm) => patch({ keepWarm })}
            disabled={!canEdit}
          >
            <FieldRow>
              <Field label="Window" htmlFor="rollback-window">
                {number("rollback-window", "windowMinutes", "minutes", { min: 1, max: 60 })}
              </Field>
            </FieldRow>
          </OptionRow>
          <OptionRow
            title="Roll back automatically"
            hint="During the window the new release's requests are read against the previous release's own. When it fails or slows past these lines, traffic goes back to the warm release and every channel that hears about failures is told."
            checked={value.keepWarm && value.automatic}
            onCheckedChange={(automatic) => patch({ automatic })}
            disabled={!canEdit || !value.keepWarm}
          >
            <FieldRow>
              <Field
                label="5xx share above the previous release"
                htmlFor="rollback-margin"
                info="In percentage points: with 5, a release whose 5xx share is more than five points above the previous release's is rolled back. A previous release that served too little to trust is taken as clean."
              >
                {number("rollback-margin", "errorRateMargin", "points", {
                  min: 0.5,
                  max: 50,
                  step: 0.5,
                })}
              </Field>
              <Field
                label="p95 against the previous release"
                htmlFor="rollback-factor"
                info="A multiple of the previous release's p95, and always at least a quarter of a second slower, so 4 ms becoming 9 ms is not a regression. An nginx ingress records no durations, so there only the 5xx share decides."
              >
                {number("rollback-factor", "latencyFactor", "×", { min: 1.2, max: 10, step: 0.1 })}
              </Field>
            </FieldRow>
            <FieldRow>
              <Field
                label="Requests before deciding"
                htmlFor="rollback-requests"
                info="Nothing is decided over fewer requests than this, so three unlucky ones cannot roll a release back."
              >
                {number("rollback-requests", "minRequests", "requests", { min: 5, max: 100000 })}
              </Field>
            </FieldRow>
          </OptionRow>
          <OptionRow
            title="Keep each visitor on the release that answered them"
            hint="Skew protection. Each visitor's first answer sets a jd_release cookie naming the release that gave it. After a cutover the previous release stays running rather than paused and keeps answering the visitors whose cookie names it — pages, scripts and styles alike — until the window ends, so a page loaded before the cutover never asks the new release for files it does not have. Everyone else, and everyone after the window, gets the new release."
            checked={value.skewProtection}
            onCheckedChange={(skewProtection) => patch({ skewProtection })}
            disabled={!canEdit || (Boolean(skew.refusal) && !value.skewProtection)}
          />
        </OptionList>
        {skew.refusal && value.skewProtection && (
          <FormNote tone="warning" className="animate-rise">
            Skew protection will be refused on the next save: {skew.refusal}.
          </FormNote>
        )}
        {value.skewProtection && !skew.refusal && !value.keepWarm && (
          <FormNote className="animate-rise">
            With the previous release not kept warm, its visitors stay on it only while it drains,
            then move to the new release.
          </FormNote>
        )}
        {!blueGreen && value.keepWarm && (
          <FormNote className="animate-rise">
            Only a blue / green release is kept warm. This environment releases stop first, so the
            previous release is stopped before the new one starts and there is nothing to keep.
          </FormNote>
        )}
      </SettingSection>
    </SettingForm>
  )
}
