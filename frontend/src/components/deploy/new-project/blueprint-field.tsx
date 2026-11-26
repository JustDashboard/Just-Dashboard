"use client"

import { useEffect } from "react"
import Link from "next/link"
import { ArrowUpRight } from "@/components/icons"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { BlueprintDetail, BlueprintInput, DeploymentHostnameSuggestion } from "@/lib/types"
import { Field, OptionRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/**
 * A reviewed template's inputs, as the Template tab and the Stacks tab both
 * ask them: one field per declared input, and the public name a `domain`
 * input is filled in with before anybody has typed one.
 */

/**
 * The public name a template's own application will be told it answers to,
 * asked for under the name the project is about to be created with, which is
 * the same question the public-address field asks later.
 *
 * Eight of the reviewed definitions declare a required `domain`: n8n writes
 * it into every webhook URL, Vaultwarden into its WebAuthn origin, Nextcloud
 * refuses any other host. That is a real requirement of those applications
 * and not something the dashboard invented — but nobody's first server owns
 * a domain, and the answer already exists: `/deploy/hostname` returns a
 * `<slug>.<address>.sslip.io` name that resolves to this host with no record
 * to create. A `domain` input *is* the plan's domain — `Render` puts it in
 * `plan.Domains` — so filling it in here is the same act as setting the
 * route, and the application's idea of its URL cannot drift from the route
 * that serves it.
 *
 * Returns the suggestion, so the field can say where its value came from.
 */
export function useSuggestedDomain(
  definition: Pick<BlueprintDetail, "inputs"> | undefined,
  projectName: string,
  setInputs: (update: (current: Record<string, string>) => Record<string, string>) => void,
) {
  const wantsDomain = (definition?.inputs ?? []).some((input) => input.kind === "domain")
  const suggestion = usePoll(
    (signal) =>
      get<DeploymentHostnameSuggestion>(
        `/deploy/hostname?name=${encodeURIComponent(projectName)}`,
        undefined,
        signal,
      ),
    0,
    [projectName],
    { enabled: wantsDomain && Boolean(projectName) },
  )
  const suggested =
    suggestion.data && suggestion.data.method !== "none" ? suggestion.data.hostname : undefined
  useEffect(() => {
    if (!definition || !suggested) return
    setInputs((current) => {
      // Keyed on presence, not on emptiness: a domain the operator cleared on
      // purpose stays cleared rather than being filled in again under them.
      const seeded = { ...current }
      let changed = false
      for (const input of definition.inputs ?? []) {
        if (input.kind !== "domain" || input.default || input.name in seeded) continue
        seeded[input.name] = suggested
        changed = true
      }
      return changed ? seeded : current
    })
  }, [definition, suggested, setInputs])
  return suggestion.data
}

export function BlueprintField({
  input,
  value,
  error,
  suggested,
  onChange,
}: {
  input: BlueprintInput
  value: string
  /** The definition requires this and it is empty, and Use has been pressed. */
  error?: string
  /** Where a pre-filled hostname came from, so the field can say so. */
  suggested?: DeploymentHostnameSuggestion
  onChange: (value: string) => void
}) {
  const id = `blueprint-${input.name}`
  if (input.kind === "secret") {
    return (
      <Notice title={input.label}>
        {input.description} Connect a database or enter {input.variable ?? input.name} in the
        Variables step. Credentials stay out of the template settings.
      </Notice>
    )
  }
  if (input.kind === "accept") {
    return (
      <Label
        htmlFor={id}
        className="flex min-h-11 items-start gap-2 text-body leading-snug text-foreground"
      >
        <Checkbox
          id={id}
          checked={value === "true"}
          onCheckedChange={(checked) => onChange(checked === true ? "true" : "false")}
        />
        <span className="space-y-1">
          <span className="block font-medium">{input.label}</span>
          {input.description && (
            <span className="block text-hint font-normal text-muted-foreground">
              {input.description}
            </span>
          )}
          {input.acceptUrl && (
            <Link
              href={input.acceptUrl}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1 text-hint font-normal underline underline-offset-4"
            >
              Read the agreement <ArrowUpRight className="size-3" />
            </Link>
          )}
          {error && (
            <span role="alert" className="block text-hint font-normal text-destructive">
              {error}
            </span>
          )}
        </span>
      </Label>
    )
  }
  // A yes-or-no answer is an option, not a field: its label is the sentence
  // and the switch answers it (§7). It was a switch under a label with "On"
  // or "Off" written beside it — the state said twice, once in a word 12px
  // high.
  if (input.kind === "boolean") {
    // The error stays with it: a required yes-or-no with no default is
    // "Required." until answered, and without the line Use this template was
    // refused with nothing on screen saying why.
    return (
      <div className="space-y-1.5">
        <OptionRow
          title={input.label}
          hint={input.description}
          checked={value === "true"}
          onCheckedChange={(checked) => onChange(checked ? "true" : "false")}
        />
        {error && (
          <p role="alert" className="text-hint leading-relaxed text-destructive">
            {error}
          </p>
        )}
      </div>
    )
  }
  return (
    <Field
      label={input.label}
      htmlFor={id}
      hint={
        suggested && value === suggested.hostname && suggested.method === "sslip"
          ? `${suggested.detail} Replace it with your own domain if you have one.`
          : input.description
      }
      error={error}
      trailing={input.required && <span className="text-hint text-muted-foreground">Required</span>}
    >
      {input.kind === "choice" ? (
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger id={id} className="w-full">
            <SelectValue placeholder="Choose" />
          </SelectTrigger>
          <SelectContent>
            {(input.choices ?? []).map((choice) => (
              <SelectItem key={choice.value} value={choice.value}>
                {choice.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Input
          id={id}
          value={value}
          type={input.kind === "number" || input.kind === "memory" ? "number" : "text"}
          inputMode={input.kind === "number" || input.kind === "memory" ? "numeric" : undefined}
          min={input.minimum}
          max={input.maximum}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
    </Field>
  )
}
