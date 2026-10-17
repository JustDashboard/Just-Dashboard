"use client"

import { useId, useState } from "react"
import Link from "next/link"
import { post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { DbAlertKind, NotificationChannel } from "@/lib/types"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { ChannelGlyph } from "@/components/deploy/vocabulary"
import { Segments } from "@/components/deploy/settings/segments"
import { SidePanelFooter } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import {
  ALERT_KINDS,
  ALERT_KIND_ORDER,
  cooldownWords,
  draftBody,
  draftErrors,
  draftFor,
  draftRule,
  durationWords,
  sentence,
  withKind,
  type AlertDraft,
} from "@/components/database/ops/alerts-model"
import { KIND_GLYPH } from "@/components/database/ops/alerts-rows"
import type { DbAlertRule } from "@/components/database/ops/alerts-types"
import { useDatabase } from "@/components/database/shell/database-context"

const CHANNEL_KIND: Record<NotificationChannel["kind"], string> = {
  discord: "Discord",
  slack: "Slack",
  telegram: "Telegram",
  email: "E-mail",
  webhook: "Webhook",
}

/**
 * One rule as a form: which database, what to watch for, its line and how
 * long it must hold, how often it may repeat itself, and who to tell.
 *
 * The kind is picked by its card and decides the unit, which sits inside the
 * line's own edge. A rule for this database is offered what its engine can
 * be watched for; one for every database is offered every kind, and watches
 * the databases that have it. The rule is spoken back at the foot, beside
 * the command that saves it, before it is saved.
 */
export function AlertForm({
  rule,
  kind,
  channels,
  onDone,
  onCancel,
}: {
  rule?: DbAlertRule
  /** The kind a new rule starts as, when the reader chose one to start from. */
  kind?: DbAlertKind
  channels: NotificationChannel[]
  onDone: () => void
  onCancel: () => void
}) {
  const { id, conn, engine } = useDatabase()
  const own = engine.capabilities.alertKinds
  const [draft, setDraft] = useState<AlertDraft>(() =>
    draftFor(rule, kind ?? own[0] ?? "backup_failed", "this"),
  )
  const [tried, setTried] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const formId = useId()
  const lineId = useId()
  const minutesId = useId()
  const cooldownId = useId()
  const spec = ALERT_KINDS[draft.kind]
  const errors = tried ? draftErrors(draft) : {}
  const offered = draft.scope === "every" ? ALERT_KIND_ORDER : own

  const change = (next: Partial<AlertDraft>) => setDraft((held) => ({ ...held, ...next }))
  const choose = (next: DbAlertKind) => setDraft((held) => withKind(held, next))
  const scope = (next: AlertDraft["scope"]) =>
    setDraft((held) =>
      next === "this" && !own.includes(held.kind)
        ? withKind({ ...held, scope: next }, own[0])
        : { ...held, scope: next },
    )

  const save = async () => {
    setTried(true)
    setError(undefined)
    if (Object.keys(draftErrors(draft)).length > 0) return
    setSaving(true)
    const body = draftBody(draft, id, rule?.enabled ?? true)
    try {
      if (rule) await put(`/databases/alerts/rules/${rule.id}`, body)
      else await post("/databases/alerts/rules", body)
      notify.success(rule ? "Alert updated" : "Alert added", {
        description: `${sentence(draftRule(draft))}.`,
      })
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <form
      id={formId}
      className="flex flex-col gap-6"
      noValidate
      onSubmit={(event) => {
        event.preventDefault()
        void save()
      }}
    >
      <Field label="Watch">
        <Segments
          label="Which databases the alert watches"
          fill
          value={draft.scope}
          onChange={scope}
          options={[
            { value: "this", label: conn.name },
            { value: "every", label: "Every database" },
          ]}
        />
      </Field>

      <Field label="Watch for">
        <div role="group" aria-label="Alert kind">
          <ChoiceGrid columns={2} className="grid-cols-1 sm:grid-cols-2 lg:grid-cols-2">
            {offered.map((option) => {
              const Glyph = KIND_GLYPH[option]
              return (
                <ChoiceCard
                  key={option}
                  selected={draft.kind === option}
                  onClick={() => choose(option)}
                  className="min-h-0 gap-1 p-2.5"
                >
                  <span className="flex min-w-0 items-center gap-2">
                    <Glyph
                      aria-hidden
                      className={cn(
                        "size-4 shrink-0",
                        draft.kind === option ? "text-brand" : "text-muted-foreground",
                      )}
                    />
                    <ChoiceCardTitle className="truncate">
                      {ALERT_KINDS[option].label}
                    </ChoiceCardTitle>
                  </span>
                  <ChoiceCardHint className="line-clamp-2">
                    {ALERT_KINDS[option].hint}
                  </ChoiceCardHint>
                </ChoiceCard>
              )
            })}
          </ChoiceGrid>
        </div>
        {draft.scope === "every" && !own.includes(draft.kind) && (
          <FormNote className="mt-2 animate-rise">
            {engine.label} has no {ALERT_KINDS[draft.kind].label.toLowerCase()} to watch; the rule
            watches the databases that have one.
          </FormNote>
        )}
      </Field>

      {(spec.line || spec.minutes.max > 0) && (
        <FieldRow>
          {spec.line && (
            <Field
              label={draft.kind === "replication" ? "Behind by more than" : "Above"}
              htmlFor={lineId}
              hint={`${spec.line.min} to ${spec.line.max}`}
              error={errors.threshold}
              className="animate-rise"
            >
              <InputGroup>
                <InputGroupInput
                  id={lineId}
                  inputMode="decimal"
                  value={draft.threshold}
                  onChange={(event) => change({ threshold: event.target.value })}
                  aria-label="Alert line"
                  className="numeric"
                />
                <InputGroupAddon align="inline-end">
                  <InputGroupText>{spec.unit === "%" ? "%" : "s"}</InputGroupText>
                </InputGroupAddon>
              </InputGroup>
            </Field>
          )}
          {spec.minutes.max > 0 && (
            <Field
              label={spec.minutes.label}
              htmlFor={minutesId}
              hint={
                draft.kind === "backup_missed"
                  ? "After a run was due."
                  : Number(draft.minutes) > 0
                    ? `= ${durationWords(Number(draft.minutes) || 0)}`
                    : "Two readings in a row."
              }
              info={
                draft.kind === "backup_missed"
                  ? "How long after its schedule's time a job may still succeed before it counts as missed."
                  : "How long it must stay over the line before anybody is told. It is read every 30 seconds."
              }
              error={errors.minutes}
            >
              <InputGroup>
                <InputGroupInput
                  id={minutesId}
                  inputMode="numeric"
                  value={draft.minutes}
                  onChange={(event) => change({ minutes: event.target.value })}
                  aria-label={
                    draft.kind === "backup_missed" ? "Grace in minutes" : "Minutes it must hold"
                  }
                  className="numeric"
                />
                <InputGroupAddon align="inline-end">
                  <InputGroupText>min</InputGroupText>
                </InputGroupAddon>
              </InputGroup>
            </Field>
          )}
        </FieldRow>
      )}

      <Field
        label="Cooldown"
        htmlFor={cooldownId}
        hint={errors.cooldown ? undefined : `Told ${cooldownWords(Number(draft.cooldown) || 0)}.`}
        info="After a message, the same database is not told again until this has passed, however often it crosses and comes back."
        error={errors.cooldown}
      >
        <InputGroup className="sm:max-w-56">
          <InputGroupInput
            id={cooldownId}
            inputMode="numeric"
            value={draft.cooldown}
            onChange={(event) => change({ cooldown: event.target.value })}
            aria-label="Cooldown in minutes"
            className="numeric"
          />
          <InputGroupAddon align="inline-end">
            <InputGroupText>min</InputGroupText>
          </InputGroupAddon>
        </InputGroup>
      </Field>

      <OptionList>
        <OptionRow
          title="Tell when it is over, too"
          checked={draft.notifyRecovery}
          onCheckedChange={(on) => change({ notifyRecovery: on })}
        />
      </OptionList>

      <Field label="Tell">
        {channels.length === 0 ? (
          <FormNote>
            No notification channels yet — the rule reaches every channel you add later.{" "}
            <Link
              href="/deploy/notifications"
              className="text-foreground underline-offset-2 hover:underline"
            >
              Add a channel
            </Link>
          </FormNote>
        ) : (
          <div className="space-y-1.5">
            <OptionList>
              {channels.map((channel) => (
                <OptionRow
                  key={channel.id}
                  title={
                    <span className="inline-flex items-center gap-2">
                      <ChannelGlyph channel={channel} />
                      {channel.name}
                    </span>
                  }
                  hint={`${CHANNEL_KIND[channel.kind]} · ${channel.target || channel.url}${channel.enabled ? "" : " · paused"}`}
                  checked={draft.channels.includes(channel.id)}
                  onCheckedChange={(on) =>
                    change({
                      channels: on
                        ? [...draft.channels, channel.id]
                        : draft.channels.filter((held) => held !== channel.id),
                    })
                  }
                />
              ))}
            </OptionList>
            {draft.channels.length === 0 && (
              <FormNote className="animate-rise">
                None chosen: every enabled channel is told.
              </FormNote>
            )}
          </div>
        )}
      </Field>

      {error && (
        <FormNote tone="danger" role="alert">
          {error}
        </FormNote>
      )}

      <SidePanelFooter>
        <p className="mr-auto min-w-0 basis-full text-hint text-muted-foreground sm:basis-auto">
          {sentence(draftRule(draft))}
          {draft.scope === "every" ? ", on every database." : "."}
        </p>
        <Button type="button" variant="outline" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
        <Button type="submit" form={formId} pending={saving}>
          {rule ? "Save alert" : "Add alert"}
        </Button>
      </SidePanelFooter>
    </form>
  )
}
