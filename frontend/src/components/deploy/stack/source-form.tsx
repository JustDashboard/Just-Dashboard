"use client"

import { useState } from "react"
import Link from "next/link"
import { Rss } from "@/components/icons"
import { ApiError, get, post } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { hostOf } from "@/components/deploy/vocabulary"
import type {
  SmartStackSourceAdded,
  SmartStackSourceDefinition,
  SmartStackSourceDetail,
  SmartStackSourceField,
} from "@/lib/types"
import {
  Disclosure,
  Field,
  FormFact,
  FormFacts,
  FormNote,
  OptionList,
  OptionRow,
} from "@/components/form"
import { INLINE_LINK } from "@/components/inline-link"
import { LoadingRows } from "@/components/loading"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { SecretField } from "@/components/secret-field"
import { ErrorState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  PRIVACY_WORD,
  addedWords,
  fieldDefaults,
  fieldsToSend,
  sourcesRefusal,
  splitFields,
  type FieldValue,
} from "@/components/deploy/stack/sources-model"

type Outcome =
  | { kind: "added"; added: SmartStackSourceAdded }
  | { kind: "failed"; message: string }
  | { kind: "error"; error: Error }

/**
 * One source's form: what it is, its fields as Prowlarr's schema has them (a
 * password field masked, the advanced ones behind More settings), then Add
 * and what came of it in words. Prowlarr tests the source before it keeps
 * it, and a test it failed is said with Prowlarr's reason, the values typed
 * here masked out by the server.
 *
 * The values live in this form only: never in the page's address or the
 * tab's storage, since a source's password is the person's own.
 */
export function SourceForm({
  base,
  definition,
  listed,
  canAdd,
  backHref,
  stackHref,
}: {
  base: string
  definition: string
  /** The catalogue's row, so the name shows before the fields have landed. */
  listed?: SmartStackSourceDefinition
  canAdd: boolean
  backHref: string
  stackHref: string
}) {
  const detail = usePoll(
    (signal) =>
      get<SmartStackSourceDetail>(
        `${base}/stack/sources/catalogue/${encodeURIComponent(definition)}`,
        undefined,
        signal,
      ),
    0,
    [base, definition],
  )
  const source = detail.data?.definition === definition ? detail.data : undefined
  const [values, setValues] = useState<Record<string, FieldValue>>()
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [busy, setBusy] = useState(false)
  const [outcome, setOutcome] = useState<Outcome>()
  const name = source?.name ?? listed?.name ?? definition
  const privacy = source?.privacy ?? listed?.privacy
  const held = values ?? (source ? fieldDefaults(source.fields) : {})
  const { basic, advanced } = splitFields(source?.fields ?? [])

  const set = (field: string, value: FieldValue) => {
    setValues({ ...held, [field]: value })
    if (outcome?.kind !== "added") setOutcome(undefined)
  }

  const add = async () => {
    if (!source || busy) return
    setBusy(true)
    setOutcome(undefined)
    try {
      const added = await post<SmartStackSourceAdded>(`${base}/stack/sources`, {
        definition,
        fields: fieldsToSend(source.fields, held),
      })
      setOutcome({ kind: "added", added })
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === "source_test_failed")
        setOutcome({ kind: "failed", message: caught.message })
      else
        setOutcome({
          kind: "error",
          error: caught instanceof Error ? caught : new Error(String(caught)),
        })
    } finally {
      setBusy(false)
    }
  }

  const refusal = detail.error instanceof ApiError ? sourcesRefusal(detail.error.code) : undefined
  const added = outcome?.kind === "added" ? addedWords(outcome.added) : undefined

  return (
    <Panel plain data-slot="source-form" aria-busy={detail.loading || busy}>
      <PanelHeader
        title={
          <span className="flex min-w-0 items-center gap-2.5">
            <ProductLogo size="sm" fallback={Rss} />
            <span className="truncate">{name}</span>
          </span>
        }
        count={privacy ? PRIVACY_WORD[privacy] : undefined}
      />
      <PanelBody className="space-y-4 pt-3">
        {detail.loading && !source && (
          <div role="status" aria-label={`Loading ${name}`}>
            <LoadingRows rows={4} />
          </div>
        )}
        {detail.error &&
          !source &&
          (refusal ? (
            <Notice tone="warning" title="This source cannot be read now">
              {refusal}
            </Notice>
          ) : (
            <ErrorState error={detail.error} onRetry={detail.refresh} what={name} />
          ))}
        {source && (
          <>
            {(source.description || source.urls.length > 0) && (
              <div className="max-w-3xl space-y-1.5">
                {source.description && (
                  <p className="text-body leading-relaxed text-muted-foreground">
                    {source.description}
                  </p>
                )}
                {source.urls.length > 0 && (
                  <FormFacts>
                    <FormFact label="At" mono>
                      {source.urls.map(hostOf).join(", ")}
                    </FormFact>
                  </FormFacts>
                )}
              </div>
            )}
            <form
              aria-label={`Add ${name}`}
              className="max-w-xl min-w-0 space-y-4"
              onSubmit={(event) => {
                event.preventDefault()
                void add()
              }}
            >
              <fieldset disabled={busy || !canAdd} className="min-w-0 space-y-4">
                {basic.length === 0 && (
                  <p className="text-body text-muted-foreground">
                    Nothing to fill in: Prowlarr knows how to reach it.
                  </p>
                )}
                <SourceFields fields={basic} values={held} onChange={set} />
                {advanced.length > 0 && (
                  <Disclosure
                    quiet
                    summary="More settings"
                    open={showAdvanced}
                    onOpenChange={setShowAdvanced}
                  >
                    {showAdvanced && (
                      <div className="space-y-4">
                        <SourceFields fields={advanced} values={held} onChange={set} />
                      </div>
                    )}
                  </Disclosure>
                )}
              </fieldset>
              {added && (
                <Notice data-slot="source-outcome" tone="success" title={added.title} live="status">
                  <p>{added.body}</p>
                  <p>
                    <Link href={backHref} className={INLINE_LINK}>
                      See the stack&apos;s sources
                    </Link>{" "}
                    ·{" "}
                    <Link href={stackHref} className={INLINE_LINK}>
                      Back to the stack
                    </Link>
                  </p>
                </Notice>
              )}
              {outcome?.kind === "failed" && (
                <Notice
                  data-slot="source-outcome"
                  tone="danger"
                  title={`Prowlarr's test of ${name} failed, so it was not added`}
                  live="alert"
                >
                  <p className="break-words">{outcome.message}</p>
                  <p className="text-hint text-muted-foreground">
                    Check what you typed, then try Add again.
                  </p>
                </Notice>
              )}
              {outcome?.kind === "error" && (
                <ErrorState error={outcome.error} onRetry={() => void add()} what={name} />
              )}
              <div className="flex flex-wrap items-center gap-3">
                <Button type="submit" pending={busy} disabled={!canAdd || added !== undefined}>
                  Add
                </Button>
                {!canAdd && (
                  <FormNote>Adding a source needs permission to control services.</FormNote>
                )}
              </div>
            </form>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

/** Each field as the kind of control its schema names, with its help as the hint. */
function SourceFields({
  fields,
  values,
  onChange,
}: {
  fields: SmartStackSourceField[]
  values: Record<string, FieldValue>
  onChange: (field: string, value: FieldValue) => void
}) {
  const toggles = fields.filter((field) => field.type === "checkbox")
  const others = fields.filter((field) => field.type !== "checkbox")
  return (
    <>
      {others.map((field) => {
        const id = `source-field-${field.name}`
        const value = values[field.name]
        if (field.type === "password")
          return (
            <SecretField
              key={field.name}
              id={id}
              label={field.label}
              hint={field.help}
              value={String(value ?? "")}
              onChange={(next) => onChange(field.name, next)}
              shown={false}
              generate={false}
              copy={false}
            />
          )
        if (field.type === "select")
          return (
            <Field key={field.name} label={field.label} htmlFor={id} hint={field.help}>
              <Select
                value={value === undefined || value === "" ? undefined : String(value)}
                onValueChange={(next) => onChange(field.name, next)}
              >
                <SelectTrigger id={id} className="w-full">
                  <SelectValue placeholder="Choose" />
                </SelectTrigger>
                <SelectContent>
                  {(field.options ?? []).map((option) => (
                    <SelectItem key={String(option.value)} value={String(option.value)}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          )
        return (
          <Field key={field.name} label={field.label} htmlFor={id} hint={field.help}>
            <Input
              id={id}
              value={String(value ?? "")}
              type={field.type === "number" ? "number" : "text"}
              inputMode={field.type === "number" ? "numeric" : undefined}
              autoComplete="off"
              spellCheck={false}
              onChange={(event) => onChange(field.name, event.target.value)}
            />
          </Field>
        )
      })}
      {toggles.length > 0 && (
        <OptionList>
          {toggles.map((field) => (
            <OptionRow
              key={field.name}
              title={field.label}
              hint={field.help}
              checked={values[field.name] === true}
              onCheckedChange={(checked) => onChange(field.name, checked)}
            />
          ))}
        </OptionList>
      )}
    </>
  )
}
