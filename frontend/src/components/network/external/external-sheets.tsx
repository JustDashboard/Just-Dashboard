"use client"

import { useState } from "react"
import { SidePanel } from "@/components/side-panel"
import { Field, FieldRow, FormNote, FormSection, OptionRow } from "@/components/form"
import { Segments } from "@/components/deploy/settings/segments"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Copy } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { calendarTime } from "@/components/network/external/time"
import type {
  EnrollmentDraft,
  ExternalCheck,
  ExternalEnrollment,
  ExternalScope,
  ExternalVantage,
} from "@/lib/network-external"
import { checkTime, familyWord } from "@/components/network/external/reading"

/** What a check asks: the source, its scope, the family, the port and TLS. */
export type CheckDraft = {
  vantageId: string
  scopeId: string
  family: "inet" | "inet6"
  port: string
  tls: boolean
}

/** A draft that starts on a source's first scope, as the old form did when one was chosen. */
export function draftFrom(vantage?: ExternalVantage, tls = true): CheckDraft {
  const scope = vantage?.scopes[0]
  return {
    vantageId: vantage?.id ?? "",
    scopeId: scope?.id ?? "",
    family: scope?.families[0] ?? "inet",
    port: scope ? String(scope.ports[0]) : "",
    tls,
  }
}

/** The draft names an enrolled source's own scope, family and port. */
export function draftScope(draft: CheckDraft, enrolled: ExternalVantage[]) {
  const scope = enrolled
    .find((v) => v.id === draft.vantageId)
    ?.scopes.find((s) => s.id === draft.scopeId)
  if (!scope || !scope.families.includes(draft.family)) return undefined
  if (!scope.ports.includes(Number(draft.port))) return undefined
  return scope
}

export function RunCheckSheet({
  open,
  onOpenChange,
  enrolled,
  draft,
  onDraft,
  waiting,
  busy,
  error,
  onQueue,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  enrolled: ExternalVantage[]
  draft: CheckDraft
  onDraft: (draft: CheckDraft) => void
  /** The chosen source already has a check queued or running. */
  waiting: boolean
  busy: boolean
  error?: string
  onQueue: () => void
}) {
  const vantage = enrolled.find((v) => v.id === draft.vantageId)
  const scope: ExternalScope | undefined = vantage?.scopes.find((s) => s.id === draft.scopeId)
  const ready = Boolean(draftScope(draft, enrolled))
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title="Run a check"
      description="Ask a controlled source to measure one approved service."
      width="md"
      footer={
        <>
          {waiting && (
            <FormNote className="mr-auto">One check at a time from each source.</FormNote>
          )}
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button pending={busy} disabled={!ready || waiting || busy} onClick={onQueue}>
            Queue check
          </Button>
        </>
      }
    >
      <div className="space-y-6">
        <Field label="Controlled source" htmlFor="external-source">
          <Picker
            id="external-source"
            value={draft.vantageId}
            onChange={(id) =>
              onDraft(
                draftFrom(
                  enrolled.find((v) => v.id === id),
                  draft.tls,
                ),
              )
            }
            disabled={busy}
            options={enrolled.map((v) => ({
              value: v.id,
              label: `${v.name} · ${v.location || "Location undeclared"}`,
            }))}
          />
        </Field>
        <FieldRow>
          <Field label="Approved service" htmlFor="external-scope">
            <Picker
              id="external-scope"
              value={draft.scopeId}
              onChange={(id) => {
                const next = vantage?.scopes.find((s) => s.id === id)
                onDraft({
                  ...draft,
                  scopeId: id,
                  family: next?.families[0] ?? "inet",
                  port: next ? String(next.ports[0]) : "",
                })
              }}
              disabled={!vantage || busy}
              options={(vantage?.scopes ?? []).map((s) => ({ value: s.id, label: s.target }))}
            />
          </Field>
          <Field label="Approved TCP port" htmlFor="external-port">
            <Picker
              id="external-port"
              value={draft.port}
              onChange={(port) => onDraft({ ...draft, port })}
              disabled={!scope || busy}
              options={(scope?.ports ?? []).map((p) => ({ value: String(p), label: String(p) }))}
            />
          </Field>
        </FieldRow>
        <Field label="Address family" htmlFor="external-family">
          <Segments
            id="external-family"
            label="Address family"
            value={draft.family}
            disabled={!scope || busy}
            onChange={(family) => onDraft({ ...draft, family })}
            options={(["inet", "inet6"] as const).map((value) => ({
              value,
              label: familyWord(value),
              disabled: !scope?.families.includes(value),
            }))}
          />
        </Field>
        {scope && (
          <FormNote className="font-mono break-all">
            Approved destinations: {scope.addresses.join(", ")}. A DNS answer outside this set stops
            before TCP.
          </FormNote>
        )}
        <OptionRow
          title="Verify TLS"
          hint="The certificate and hostname on this connection"
          checked={draft.tls}
          onCheckedChange={(tls) => onDraft({ ...draft, tls })}
          disabled={busy}
        />
        <FormNote>
          The agent polls outbound through Caddy. A missing agent expires without a measurement.
        </FormNote>
        {error && (
          <FormNote tone="danger" role="alert">
            {error}
          </FormNote>
        )}
      </div>
    </SidePanel>
  )
}

export function EnrollSheet({
  open,
  onOpenChange,
  draft,
  onDraft,
  ready,
  busy,
  error,
  enrollment,
  onCreate,
  onDismiss,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  draft: EnrollmentDraft
  onDraft: (draft: EnrollmentDraft) => void
  ready: boolean
  busy: boolean
  error?: string
  /** The one-use credential, while it is kept: shown in place of the form. */
  enrollment?: ExternalEnrollment
  onCreate: () => void
  onDismiss: () => void
}) {
  const change = <K extends keyof EnrollmentDraft>(key: K, value: EnrollmentDraft[K]) =>
    onDraft({ ...draft, [key]: value })
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title={enrollment ? `${enrollment.vantage.name} can enroll` : "Enroll a source"}
      description="Create a ten-minute, one-use enrollment for a machine you control."
      width="md"
      footer={
        enrollment ? (
          <>
            <Button
              variant="outline"
              onClick={() => void copyText(enrollment.token, "Enrollment token copied")}
            >
              <Copy className="size-3.5" />
              Copy enrollment token
            </Button>
            <Button onClick={onDismiss}>Dismiss credential</Button>
          </>
        ) : (
          <>
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
              Cancel
            </Button>
            <Button type="submit" form="external-enroll" pending={busy} disabled={!ready || busy}>
              Create one-use enrollment
            </Button>
          </>
        )
      }
    >
      {enrollment ? (
        <div className="space-y-5" data-testid="external-enrollment">
          <FormNote>
            Shown once, and kept only on this page until dismissed or{" "}
            <time dateTime={enrollment.expiresAt}>{calendarTime(enrollment.expiresAt)}</time>. Send
            it to the agent through standard input, with the dashboard&apos;s HTTPS origin, its
            trusted TLS public-key pin and this server&apos;s signing key.
          </FormNote>
          <Field label="One-use enrollment token" htmlFor="enrollment-token">
            <Input
              id="enrollment-token"
              value={enrollment.token}
              readOnly
              autoComplete="off"
              className="font-mono"
            />
          </Field>
          <Field label="Server signing key" htmlFor="enrollment-key">
            <Input
              id="enrollment-key"
              value={enrollment.serverKey}
              readOnly
              className="font-mono"
            />
          </Field>
          <Field label="Vantage ID" htmlFor="enrollment-vantage">
            <Input
              id="enrollment-vantage"
              value={enrollment.vantage.id}
              readOnly
              className="font-mono"
            />
          </Field>
        </div>
      ) : (
        <form
          id="external-enroll"
          className="space-y-6"
          onSubmit={(event) => {
            event.preventDefault()
            if (ready && !busy) onCreate()
          }}
        >
          <FormSection title="Source">
            <FieldRow>
              <Field label="Source name" htmlFor="enroll-name">
                <Input
                  id="enroll-name"
                  value={draft.name}
                  onChange={(event) => change("name", event.target.value)}
                  maxLength={80}
                  disabled={busy}
                />
              </Field>
              <Field
                label="Declared location"
                htmlFor="enroll-location"
                hint="Your label; not verified."
              >
                <Input
                  id="enroll-location"
                  value={draft.location}
                  onChange={(event) => change("location", event.target.value)}
                  maxLength={120}
                  disabled={busy}
                />
              </Field>
            </FieldRow>
            <Field label="Placement" htmlFor="enroll-placement">
              <Segments
                id="enroll-placement"
                label="Placement"
                value={draft.placement}
                disabled={busy}
                onChange={(placement) => change("placement", placement)}
                options={[
                  { value: "external_host", label: "External controlled host" },
                  { value: "controlled_fixture", label: "Controlled fixture" },
                ]}
              />
            </Field>
          </FormSection>
          <FormSection title="Approved service">
            <Field label="Service name or address" htmlFor="enroll-target">
              <Input
                id="enroll-target"
                value={draft.target}
                onChange={(event) => change("target", event.target.value)}
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
                disabled={busy}
              />
            </Field>
            <Field
              label="Approved literal addresses"
              htmlFor="enroll-addresses"
              hint="Exact IPv4 or IPv6 addresses, comma-separated. No ranges."
            >
              <Input
                id="enroll-addresses"
                value={draft.addresses}
                onChange={(event) => change("addresses", event.target.value)}
                className="font-mono"
                spellCheck={false}
                disabled={busy}
              />
            </Field>
            <FieldRow>
              <Field label="Approved TCP ports" htmlFor="enroll-ports">
                <Input
                  id="enroll-ports"
                  value={draft.ports}
                  onChange={(event) => change("ports", event.target.value)}
                  className="font-mono"
                  disabled={busy}
                />
              </Field>
              <Field label="Approved families" htmlFor="enroll-families">
                <Segments
                  id="enroll-families"
                  label="Approved families"
                  value={draft.family}
                  disabled={busy}
                  onChange={(family) => change("family", family)}
                  options={[
                    { value: "inet", label: "IPv4" },
                    { value: "inet6", label: "IPv6" },
                    { value: "both", label: "Both" },
                  ]}
                />
              </Field>
            </FieldRow>
            <FormNote>
              The agent cannot widen these addresses and ports. Keep its outbound address in the
              dashboard&apos;s allowlist.
            </FormNote>
          </FormSection>
          {error && (
            <FormNote tone="danger" role="alert">
              {error}
            </FormNote>
          )}
        </form>
      )}
    </SidePanel>
  )
}

export function CompareSheet({
  open,
  onOpenChange,
  retained,
  name,
  before,
  after,
  onBefore,
  onAfter,
  busy,
  error,
  comparison,
  onCompare,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  retained: ExternalCheck[]
  /** A check's source, as the picker words it. */
  name: (check: ExternalCheck) => string
  before: string
  after: string
  onBefore: (id: string) => void
  onAfter: (id: string) => void
  busy: boolean
  error?: string
  comparison?: string
  onCompare: () => void
}) {
  const options = retained.map((row) => ({
    value: row.id,
    label: `${name(row)} · ${familyWord(row.request.family)} · ${calendarTime(checkTime(row))}`,
  }))
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title="Compare measurements"
      description="Read two retained measurements side by side."
      width="md"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Close
          </Button>
          <Button
            pending={busy}
            disabled={!before || !after || before === after || busy}
            onClick={onCompare}
          >
            Compare
          </Button>
        </>
      }
    >
      <div className="space-y-6">
        <Field label="First measurement" htmlFor="external-before">
          <Picker id="external-before" value={before} onChange={onBefore} options={options} />
        </Field>
        <Field label="Second measurement" htmlFor="external-after">
          <Picker id="external-after" value={after} onChange={onAfter} options={options} />
        </Field>
        {comparison && (
          <p role="status" className="text-body">
            {comparison}
          </p>
        )}
        {error && (
          <FormNote tone="danger" role="alert">
            {error}
          </FormNote>
        )}
      </div>
    </SidePanel>
  )
}

function Picker({
  id,
  value,
  onChange,
  options,
  disabled,
}: {
  id: string
  value: string
  onChange: (value: string) => void
  options: { value: string; label: string }[]
  disabled?: boolean
}) {
  return (
    <Select
      value={value}
      onValueChange={(next) => {
        // Native form events can report an empty value while dependent options mount.
        if (options.some((option) => option.value === next)) onChange(next)
      }}
      disabled={disabled}
    >
      <SelectTrigger id={id} className="w-full">
        <SelectValue placeholder="Select…" />
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem key={option.value} value={option.value}>
            {option.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
