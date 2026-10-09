"use client"

import { useEffect, useId, useRef, useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { errorMessage, post, put } from "@/lib/api"
import {
  DNS_ENGINES,
  DNS_SERVICE_BASE,
  dnsEngineName,
  readDNSView,
  type DNSConnection,
  type DNSEngine,
  type DNSServiceView,
} from "@/lib/network-dns-services"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Field, FieldRow, FormSection, OptionRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { dnsConnectionDraft, prepareDNSConnection, type DNSFormErrors } from "./service-form"

export function DNSServiceConnectionForm(props: {
  connection?: DNSConnection
  onConnected: (view: DNSServiceView) => void
  onCancel?: () => void
}) {
  return <ConnectionEditor key={props.connection?.id ?? "new"} {...props} />
}

function ConnectionEditor({
  connection,
  onConnected,
  onCancel,
}: {
  connection?: DNSConnection
  onConnected: (view: DNSServiceView) => void
  onCancel?: () => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const id = useId()
  const [draft, setDraft] = useState(() => dnsConnectionDraft(connection))
  const [baseline] = useState(connection?.generation)
  const [busy, setBusy] = useState(false)
  const [errors, setErrors] = useState<DNSFormErrors>({})
  const [error, setError] = useState<string>()
  const summary = useRef<HTMLDivElement>(null)
  const inFlight = useRef(false)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const change = (key: keyof typeof draft, value: string | boolean) => {
    setDraft((current) => ({ ...current, [key]: value }))
    setErrors((current) => ({
      ...current,
      [key]: undefined,
      ...(key === "customCA" ? { ca: undefined } : {}),
    }))
  }
  const selectEngine = (engine: DNSEngine) => {
    setDraft((current) => ({ ...current, engine, username: "", password: "", token: "" }))
    setErrors({})
  }
  const submit = async () => {
    if (!admin || inFlight.current) return
    if (baseline !== connection?.generation) {
      setError("The connection changed. Reopen its replacement form; your draft is retained here.")
      requestAnimationFrame(() => summary.current?.focus())
      return
    }
    const prepared = prepareDNSConnection(draft, connection)
    setErrors(prepared.errors)
    setError(undefined)
    if (!prepared.request) {
      requestAnimationFrame(() => summary.current?.focus())
      return
    }
    inFlight.current = true
    setBusy(true)
    try {
      const value = connection
        ? await put<unknown>(
            `${DNS_SERVICE_BASE}/${encodeURIComponent(connection.id)}`,
            prepared.request,
          )
        : await post<unknown>(DNS_SERVICE_BASE, prepared.request)
      const view = readDNSView(value, connection?.id)
      if (
        view.connection.engine !== prepared.request.engine ||
        view.connection.endpoint !== prepared.request.endpoint
      )
        throw new Error(
          "The returned native engine or management origin changed. Read the connection again.",
        )
      if (!mounted.current) return
      setDraft((current) => ({ ...current, password: "", token: "", ca: "" }))
      onConnected(view)
    } catch (err) {
      if (!mounted.current) return
      // Full credentials are request-only. Retain the nonsecret draft after
      // failure, but require explicit secret re-entry for another attempt.
      setDraft((current) => ({ ...current, password: "", token: "", ca: "" }))
      setError(errorMessage(err))
      requestAnimationFrame(() => summary.current?.focus())
    } finally {
      inFlight.current = false
      if (mounted.current) setBusy(false)
    }
  }
  const blocked = !admin || busy
  const invalid = Object.entries(errors).filter(([, message]) => message)
  const control = (
    key: "name" | "endpoint" | "serverName" | "username" | "password" | "token",
    label: string,
    hint?: string,
  ) => (
    <Field label={label} htmlFor={`${id}-${key}`} error={errors[key]} hint={hint}>
      <Input
        id={`${id}-${key}`}
        value={draft[key]}
        onChange={(event) => change(key, event.target.value)}
        disabled={blocked || (key === "endpoint" && Boolean(connection))}
        type={key === "password" || key === "token" ? "password" : "text"}
        autoComplete={key === "password" ? "new-password" : "off"}
        spellCheck={false}
        aria-invalid={Boolean(errors[key])}
        maxLength={key === "name" ? 80 : key === "serverName" ? 253 : 4096}
        className={key === "endpoint" || key === "serverName" ? "font-mono" : undefined}
      />
    </Field>
  )
  return (
    <form
      className="flex min-w-0 flex-col gap-6"
      aria-label={connection ? "Replace native DNS connection" : "Connect native DNS service"}
      aria-busy={busy}
      onSubmit={(event) => {
        event.preventDefault()
        void submit()
      }}
    >
      <FormSection title={connection ? "Replace connection settings" : "Native DNS engine"}>
        {connection ? (
          <p className="text-body">
            {dnsEngineName(connection.engine)} ·{" "}
            <span className="font-mono break-all">{connection.endpoint}</span>
          </p>
        ) : (
          <ChoiceGrid>
            {DNS_ENGINES.map((engine) => (
              <ChoiceCard
                key={engine.value}
                selected={draft.engine === engine.value}
                disabled={blocked}
                title={engine.name}
                verb={`Select ${engine.name}`}
                onClick={() => selectEngine(engine.value)}
                description={
                  engine.value === "technitium"
                    ? "Native scoped API token"
                    : engine.value === "pihole"
                      ? "Native API password"
                      : "Native username and password"
                }
              />
            ))}
          </ChoiceGrid>
        )}
        <FieldRow>
          {control("name", "Connection name")}
          {control(
            "endpoint",
            "Management origin",
            "Literal IP with an explicit port. HTTP requires loopback; other addresses require verified HTTPS.",
          )}
        </FieldRow>
      </FormSection>
      <FormSection title="Native credentials">
        <p className="text-body text-muted-foreground">
          {connection
            ? "Re-enter the complete native credential for this replacement."
            : "Credentials are sealed by the server and are never returned or exported."}
        </p>
        <FieldRow>
          {draft.engine === "adguard" && control("username", "Native username")}
          {draft.engine !== "technitium" &&
            control(
              "password",
              draft.engine === "pihole" ? "Native API password" : "Native password",
            )}
          {draft.engine === "technitium" &&
            control(
              "token",
              "Native API token",
              "A scoped Technitium token, supplied only in the request body.",
            )}
        </FieldRow>
      </FormSection>
      <FormSection title="Verified HTTPS trust">
        {control(
          "serverName",
          "TLS server name (optional)",
          "Declare the DNS identity on the certificate while dialing the literal management IP.",
        )}
        <OptionRow
          title="Use a custom CA"
          hint={
            connection?.customCA
              ? "Existing custom trust is sealed. Re-enter it or explicitly select system trust."
              : "System certificate trust is used unless you supply a custom CA."
          }
          checked={draft.customCA}
          disabled={blocked}
          onCheckedChange={(value) => change("customCA", value)}
        >
          <Field label="Custom CA certificate" htmlFor={`${id}-ca`} error={errors.ca}>
            <Textarea
              id={`${id}-ca`}
              value={draft.ca}
              onChange={(event) => change("ca", event.target.value)}
              disabled={blocked}
              maxLength={32768}
              spellCheck={false}
              autoComplete="off"
              aria-invalid={Boolean(errors.ca)}
              className="font-mono"
            />
          </Field>
        </OptionRow>
      </FormSection>
      <OptionRow
        title="Allow reviewed native changes"
        checked={draft.management}
        disabled={blocked}
        onCheckedChange={(value) => change("management", value)}
        hint="Connections default to read-only. Native changes need a separate retained review and destructive-action permission to apply."
      />
      {(error || invalid.length > 0) && (
        <div ref={summary} role="alert" tabIndex={-1} className="rounded-sm focus-ring">
          <Notice tone="warning" title="Connection needs review">
            {error && (
              <p>
                {error} Your nonsensitive draft remains; re-enter the credential and any custom CA
                before retrying.
              </p>
            )}
            {invalid.length > 0 && (
              <ul className="list-inside list-disc">
                {invalid.map(([key, message]) => (
                  <li key={key}>
                    <a className="underline" href={`#${id}-${key}`}>
                      {message}
                    </a>
                  </li>
                ))}
              </ul>
            )}
          </Notice>
        </div>
      )}
      {!admin && (
        <p className="text-body text-muted-foreground">
          Connecting native DNS services requires system administration access.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={blocked} pending={busy}>
          {connection ? "Replace and read native service" : "Connect and read native service"}
        </Button>
        {onCancel && (
          <Button type="button" variant="ghost" disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
        )}
      </div>
    </form>
  )
}
