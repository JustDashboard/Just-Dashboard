"use client"

import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { del, post, ApiError } from "@/lib/api"
import type { DNSApplied, DNSClearableList, DNSVerification, DNSView } from "@/lib/types"
import { ChoiceCard, ChoiceGrid, ProductCard } from "@/components/choice-card"
import { Field, FieldRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { Checkbox } from "@/components/ui/checkbox"
import { useConfirm } from "@/components/confirm-dialog"
import { Globe } from "@/components/icons"
import {
  clearedLists,
  missingTLSNames,
  parseList,
  presetChosen,
  refusedVerification,
} from "@/components/network/dns/resolvers"
import { VerificationChecks } from "@/components/network/dns/verification"
import { dnsServersProblem } from "@/components/network/draft-input"

type Draft = {
  servers: string
  fallback: string
  domains: string
  dnssec: string
  dot: string
  cache: string
  /** The lists written as empty on purpose rather than left to the host. */
  clear: Partial<Record<DNSClearableList, boolean>>
}

/** What clearing each list does, said where the box is ticked. */
const CLEAR: Record<DNSClearableList, string> = {
  servers: "Clear the global servers; each link's own servers still answer",
  fallback: "No fallback servers: turns off resolved's built-in fallback",
  domains: "Clear the global search and routing domains",
}

/** DNS over TLS, as the three answers it has. `no` is resolved's word for off. */
const DOT = [
  {
    value: "no",
    title: "Off",
    description: "Plain DNS on port 53, readable and changeable by anyone on the path",
  },
  {
    value: "opportunistic",
    title: "Opportunistic",
    description: "Encrypted when the server offers it, plain when it does not",
  },
  {
    value: "yes",
    title: "Required",
    description:
      "Plain DNS is refused; every server needs its name, like 1.1.1.1#cloudflare-dns.com",
  },
]

/** Radix's select has no empty value, so "leave it at resolved's default" is a word of its own. */
const DNSSEC = [
  { value: "default", label: "Resolved's default" },
  { value: "no", label: "Off" },
  { value: "allow-downgrade", label: "Validate where the server supports it" },
  { value: "yes", label: "Validate, refusing answers that fail" },
]

const CACHE = [
  { value: "default", label: "Resolved's default" },
  { value: "yes", label: "All answers" },
  { value: "no-negative", label: "Positive answers only" },
  { value: "no", label: "Disabled" },
]

/**
 * What the form starts from: the dashboard's own drop-in when it has written
 * one, otherwise what resolved is running with now. Fallback and cache defaults
 * start unset unless the operator chose them in our drop-in; writing compiled-in
 * defaults back would pin them instead of preserving host behaviour.
 */
function baseline(view: DNSView): Draft {
  const { managed, resolved } = view
  const source = managed.exists
    ? {
        servers: managed.servers,
        domains: managed.domains,
        dnssec: managed.dnssec,
        dot: managed.dnsOverTLS,
      }
    : {
        servers: resolved.global.servers,
        domains: resolved.global.domains,
        dnssec: resolved.global.dnssec,
        dot: resolved.global.dnsOverTLS,
      }
  const cleared = managed.exists ? (managed.cleared ?? []) : []
  return {
    servers: source.servers.join("\n"),
    domains: source.domains.join(" "),
    dnssec: source.dnssec,
    dot: source.dot || "no",
    fallback: managed.exists ? managed.fallback.join("\n") : "",
    cache: managed.exists ? managed.cache : "",
    clear: Object.fromEntries(cleared.map((list) => [list, true])),
  }
}

/**
 * Who this server asks. Public resolvers are cards, because they are things
 * you pick: choosing one fills the servers with the `address#name` pairs DNS
 * over TLS validates against, and the text area under them is the same list
 * for anything else. The three answers DNS over TLS has are cards too, since
 * they differ in what they promise; DNSSEC and the domains are plain fields.
 *
 * A wrong upstream stops every lookup on this host, the dashboard's own among
 * them, so Apply asks first, and the server checks that a name resolves
 * afterwards and puts the previous settings back if it does not. What it
 * found is drawn under the buttons: the name that resolved and how long it
 * took, or the warning the change could not fix.
 */
export function UpstreamEditor({
  view,
  readOnly,
  onChanged,
}: {
  view: DNSView
  /** Why nothing here can change — systemd-resolved is not running — or undefined. */
  readOnly?: string
  onChanged: () => void
}) {
  const { can } = useAuth()
  const base = baseline(view)
  const [edits, setEdits] = useState<Partial<Draft>>({})
  const [applied, setApplied] = useState<DNSApplied>()
  const [rolledBack, setRolledBack] = useState<{ reason: string; verification: DNSVerification }>()
  const [refused, setRefused] = useState<string>()
  const [planError, setPlanError] = useState<string>()
  const [planning, setPlanning] = useState(false)
  const [verificationNames, setVerificationNames] = useState("")
  const { confirm, dialog } = useConfirm()
  const draft: Draft = { ...base, ...edits }
  const edit = (patch: Partial<Draft>) => {
    setEdits((previous) => ({ ...previous, ...patch }))
    setApplied(undefined)
    setRolledBack(undefined)
    setPlanError(undefined)
  }

  const servers = parseList(draft.servers)
  const fallback = parseList(draft.fallback)
  const domains = parseList(draft.domains)
  const clear = clearedLists({ servers, fallback, domains }, draft.clear)
  const baseClear = clearedLists(
    {
      servers: parseList(base.servers),
      fallback: parseList(base.fallback),
      domains: parseList(base.domains),
    },
    base.clear,
  )
  const names = parseList(verificationNames)
  const unnamed = draft.dot === "yes" ? missingTLSNames(servers) : []
  const unnamedFallback = draft.dot === "yes" ? missingTLSNames(fallback) : []
  const serversError =
    dnsServersProblem(servers) ??
    (draft.dot === "yes" && servers.length === 0
      ? "DNS over TLS is required, so it needs servers to talk to."
      : unnamed.length > 0
        ? `Required DNS over TLS needs a name on every server: ${unnamed[0]} has none.`
        : undefined)
  const fallbackError =
    dnsServersProblem(fallback) ??
    (unnamedFallback.length > 0
      ? `Required DNS over TLS needs a name on every fallback server: ${unnamedFallback[0]} has none.`
      : undefined)
  const dirty =
    servers.join(" ") !== parseList(base.servers).join(" ") ||
    fallback.join(" ") !== parseList(base.fallback).join(" ") ||
    domains.join(" ") !== parseList(base.domains).join(" ") ||
    draft.dnssec !== base.dnssec ||
    draft.dot !== base.dot ||
    draft.cache !== base.cache ||
    clear.join(" ") !== baseClear.join(" ")
  const blocked = !can("system.admin")
    ? "Changing the host resolver requires an administrator."
    : (readOnly ?? refused)
  const invalid = Boolean(serversError || fallbackError)

  const finish = (result: DNSApplied) => {
    setApplied(result)
    setRolledBack(undefined)
    setEdits({})
    onChanged()
  }
  const refuse = (err: unknown) => {
    if (err instanceof ApiError && err.code === "network_read_only") setRefused(err.message)
  }

  const body = {
    servers,
    fallback,
    domains,
    dnssec: draft.dnssec,
    dnsOverTLS: draft.dot,
    cache: draft.cache,
    ...(clear.length > 0 ? { clear } : {}),
    ...(names.length > 0 ? { verificationNames: names } : {}),
  }

  // The plan comes from the server before the dialog opens, so what the
  // dialog promises to check is exactly what the change will be held to.
  const apply = async () => {
    setPlanning(true)
    setPlanError(undefined)
    let plan: DNSVerification
    try {
      plan = await post<DNSVerification>("/network/dns/verification-plan", body)
    } catch (err) {
      refuse(err)
      setPlanError(err instanceof Error ? err.message : String(err))
      return
    } finally {
      setPlanning(false)
    }
    confirm({
      title: "Change the upstream resolvers",
      confirmLabel: "Apply",
      description: (
        <>
          <p>
            Every program on this server resolves names through these servers: package updates,
            certificate renewals and this dashboard included. A wrong one breaks all of them at
            once.
          </p>
          <p className="font-mono text-xs break-all text-muted-foreground">
            {servers.length > 0
              ? servers.join("  ")
              : clear.includes("servers")
                ? "no global servers: each link's own"
                : "the servers the network hands out"}
          </p>
          <p className="text-body">
            Fallback:{" "}
            {fallback.length > 0
              ? fallback.join("  ")
              : clear.includes("fallback")
                ? "none"
                : "the host's defaults"}
            . Cache: {CACHE.find((option) => option.value === (draft.cache || "default"))?.label}.
          </p>
          <p>
            After the restart each check below runs; a required one that fails puts the previous
            settings back.
          </p>
          <VerificationChecks verification={plan} label="Verification plan" />
        </>
      ),
      action: async () => {
        try {
          finish(await post<DNSApplied>("/network/dns/", body))
        } catch (err) {
          refuse(err)
          const verification =
            err instanceof ApiError && err.code === "dns_upstream_unreachable"
              ? refusedVerification(err.body)
              : undefined
          if (!verification || !(err instanceof ApiError)) throw err
          setApplied(undefined)
          setRolledBack({ reason: err.message, verification })
        }
        return "reported"
      },
    })
  }

  const reset = () =>
    confirm({
      title: "Go back to the system's resolvers",
      confirmLabel: "Reset",
      description: (
        <p>
          The settings this dashboard wrote are removed, and this server asks the servers its
          network configuration hands out again.
        </p>
      ),
      action: async () => {
        try {
          finish(await del<DNSApplied>("/network/dns/"))
        } catch (err) {
          refuse(err)
          throw err
        }
        return "reported"
      },
    })

  const choose = (id: string) => {
    const preset = view.presets.find((p) => p.id === id)
    if (preset) edit({ servers: preset.tlsServers.join("\n") })
  }

  return (
    <form
      className="flex min-w-0 flex-col gap-6"
      onSubmit={(event) => {
        event.preventDefault()
        if (dirty && !invalid && !blocked && !planning) void apply()
      }}
    >
      {blocked && (
        <Notice title="These settings are read here, not written" tone="warning">
          {blocked}
        </Notice>
      )}

      <div>
        <p className="mb-2 text-body font-medium">Ask a public resolver</p>
        <ChoiceGrid columns="compact">
          {view.presets.map((preset) => (
            <ProductCard
              key={preset.id}
              product={preset.product}
              fallback={Globe}
              label={preset.name.split(",")[0]}
              detail={
                preset.blocksAds
                  ? "Blocks ads and trackers"
                  : preset.blocksMalware
                    ? "Blocks malware"
                    : "No filtering"
              }
              selected={presetChosen(preset, servers)}
              disabled={Boolean(blocked)}
              onClick={() => choose(preset.id)}
            />
          ))}
        </ChoiceGrid>
      </div>

      <div className="grid min-w-0 gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <div className="flex min-w-0 flex-col gap-5">
          <Field
            label="Verification names"
            htmlFor="dns-verification-names"
            hint="Optional, up to eight: a name in each scope this change touches, such as nas.home.arpa or git.corp.example. Each must answer after applying. Leave empty to check public names."
          >
            <Textarea
              id="dns-verification-names"
              value={verificationNames}
              rows={2}
              spellCheck={false}
              autoComplete="off"
              onChange={(event) => {
                setVerificationNames(event.target.value)
                setPlanError(undefined)
              }}
              disabled={Boolean(blocked)}
              placeholder={"nas.home.arpa\ngit.corp.example"}
              className="font-mono"
            />
          </Field>
          <Field
            label="Servers"
            htmlFor="dns-servers"
            hint="One per line, as 9.9.9.9 or 9.9.9.9#dns.quad9.net. Up to eight; empty uses the servers the network hands out."
            error={serversError}
          >
            <Textarea
              id="dns-servers"
              value={draft.servers}
              rows={Math.min(Math.max(servers.length + 1, 3), 9)}
              spellCheck={false}
              autoComplete="off"
              disabled={Boolean(blocked)}
              aria-invalid={Boolean(serversError)}
              onChange={(event) => edit({ servers: event.target.value })}
              className="font-mono"
              placeholder={"1.1.1.1#cloudflare-dns.com\n1.0.0.1#cloudflare-dns.com"}
            />
          </Field>
          <ClearList
            list="servers"
            empty={servers.length === 0}
            checked={Boolean(draft.clear.servers)}
            disabled={Boolean(blocked)}
            onChange={(checked) => edit({ clear: { ...draft.clear, servers: checked } })}
          />
          <Field
            label="Fallback servers"
            htmlFor="dns-fallback"
            hint="Optional, up to eight. Used when no other server is known. Leave empty for the host's fallback defaults. Ports, %interface and #TLS names use the same format as Servers."
            error={fallbackError}
          >
            <Textarea
              id="dns-fallback"
              value={draft.fallback}
              rows={Math.min(Math.max(fallback.length + 1, 2), 9)}
              spellCheck={false}
              autoComplete="off"
              disabled={Boolean(blocked)}
              aria-invalid={Boolean(fallbackError)}
              onChange={(event) => edit({ fallback: event.target.value })}
              className="font-mono"
              placeholder="192.168.1.53#resolver.home.arpa"
            />
          </Field>
          <ClearList
            list="fallback"
            empty={fallback.length === 0}
            checked={Boolean(draft.clear.fallback)}
            disabled={Boolean(blocked)}
            onChange={(checked) => edit({ clear: { ...draft.clear, fallback: checked } })}
          />
          <Field
            label="Cache mode"
            htmlFor="dns-cache"
            hint="Positive answers only avoids caching a missing name; the default leaves the host's policy in charge."
          >
            <Select
              value={draft.cache || "default"}
              onValueChange={(value) => edit({ cache: value === "default" ? "" : value })}
              disabled={Boolean(blocked)}
            >
              <SelectTrigger id="dns-cache" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CACHE.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <FieldRow>
            <Field label="DNSSEC" htmlFor="dns-dnssec">
              <Select
                value={draft.dnssec || "default"}
                onValueChange={(value) => edit({ dnssec: value === "default" ? "" : value })}
                disabled={Boolean(blocked)}
              >
                <SelectTrigger id="dns-dnssec" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {DNSSEC.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field
              label="Domains"
              htmlFor="dns-domains"
              hint="Search domains, or ~domain to send a domain's names only to these servers; ~. is every name."
            >
              <Input
                id="dns-domains"
                value={draft.domains}
                onChange={(event) => edit({ domains: event.target.value })}
                disabled={Boolean(blocked)}
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
                placeholder="~. lan.example.net"
              />
            </Field>
          </FieldRow>
          <ClearList
            list="domains"
            empty={domains.length === 0}
            checked={Boolean(draft.clear.domains)}
            disabled={Boolean(blocked)}
            onChange={(checked) => edit({ clear: { ...draft.clear, domains: checked } })}
          />
        </div>

        <fieldset className="min-w-0">
          <legend className="mb-2 text-body font-medium">DNS over TLS</legend>
          <ChoiceGrid columns={2} className="sm:grid-cols-3 xl:grid-cols-1">
            {DOT.map((option, index) => (
              <ChoiceCard
                key={option.value}
                verb={`DNS over TLS ${option.title.toLowerCase()}`}
                title={option.title}
                description={option.description}
                selected={draft.dot === option.value}
                disabled={Boolean(blocked)}
                onClick={() => edit({ dot: option.value })}
                index={index}
              />
            ))}
          </ChoiceGrid>
        </fieldset>
      </div>

      <div className="flex min-w-0 flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" disabled={!dirty || invalid || Boolean(blocked)} pending={planning}>
            Apply
          </Button>
          {view.managed.exists && (
            <Button
              type="button"
              variant="outline"
              disabled={Boolean(blocked)}
              onClick={() => reset()}
            >
              Reset to the system&rsquo;s
            </Button>
          )}
        </div>
        {planError && (
          <p role="alert" className="text-body text-destructive">
            {planError}
          </p>
        )}
        {applied && <AppliedResult applied={applied} />}
        {rolledBack && (
          <div className="flex min-w-0 animate-rise flex-col gap-3" role="alert">
            <Notice title="Put back: a required check failed" tone="danger">
              {rolledBack.reason}
            </Notice>
            <VerificationChecks
              verification={rolledBack.verification}
              label="Verification results"
            />
          </div>
        )}
      </div>
      {dialog}
    </form>
  )
}

/**
 * Emptying a list on purpose, offered only while the list is empty: an empty
 * field otherwise leaves the host's own list in force.
 */
function ClearList({
  list,
  empty,
  checked,
  disabled,
  onChange,
}: {
  list: DNSClearableList
  empty: boolean
  checked: boolean
  disabled: boolean
  onChange: (checked: boolean) => void
}) {
  if (!empty) return null
  return (
    <label className="flex min-h-11 items-center gap-3 text-body">
      <Checkbox
        aria-label={CLEAR[list]}
        checked={checked}
        disabled={disabled}
        onCheckedChange={(value) => onChange(value === true)}
      />
      {CLEAR[list]}
    </label>
  )
}

/** What the server found after the change: every check's result, and any warning. */
function AppliedResult({ applied }: { applied: DNSApplied }) {
  return (
    <div className="flex min-w-0 animate-rise flex-col gap-3" role="status">
      {applied.verified ? (
        <Status
          tone="running"
          className="text-body"
          label={
            <>
              Verified: <span className="font-mono">{applied.via}</span> resolved in{" "}
              <span className="numeric">{Math.round(applied.millis ?? 0)}</span> ms
            </>
          }
        />
      ) : (
        <Status
          tone="notice"
          className="text-body"
          label="Applied; no name was resolved to check it"
        />
      )}
      {applied.verification && (
        <VerificationChecks verification={applied.verification} label="Verification results" />
      )}
      {applied.warning && (
        <Notice title="Applied, with a warning" tone="warning">
          {applied.warning}
        </Notice>
      )}
    </div>
  )
}
