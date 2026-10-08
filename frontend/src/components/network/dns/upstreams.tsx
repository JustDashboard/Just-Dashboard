"use client"

import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { del, post, ApiError } from "@/lib/api"
import type { DNSApplied, DNSView } from "@/lib/types"
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
import { useConfirm } from "@/components/confirm-dialog"
import { Globe } from "@/components/icons"
import { missingTLSNames, parseList, presetChosen } from "@/components/network/dns/resolvers"
import { dnsServersProblem } from "@/components/network/draft-input"

type Draft = {
  servers: string
  fallback: string
  domains: string
  dnssec: string
  dot: string
  cache: string
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
  return {
    servers: source.servers.join("\n"),
    domains: source.domains.join(" "),
    dnssec: source.dnssec,
    dot: source.dot || "no",
    fallback: managed.exists ? managed.fallback.join("\n") : "",
    cache: managed.exists ? managed.cache : "",
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
  const [refused, setRefused] = useState<string>()
  const [verificationName, setVerificationName] = useState("")
  const { confirm, dialog } = useConfirm()
  const draft: Draft = { ...base, ...edits }
  const edit = (patch: Partial<Draft>) => {
    setEdits((previous) => ({ ...previous, ...patch }))
    setApplied(undefined)
  }

  const servers = parseList(draft.servers)
  const fallback = parseList(draft.fallback)
  const domains = parseList(draft.domains)
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
    draft.cache !== base.cache
  const blocked = !can("system.admin")
    ? "Changing the host resolver requires an administrator."
    : (readOnly ?? refused)
  const invalid = Boolean(serversError || fallbackError)

  const finish = (result: DNSApplied) => {
    setApplied(result)
    setEdits({})
    onChanged()
  }
  const refuse = (err: unknown) => {
    if (err instanceof ApiError && err.code === "network_read_only") setRefused(err.message)
  }

  const apply = () =>
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
          <p>
            The dashboard checks that a name still resolves afterwards, and puts the previous
            settings back if it does not.
          </p>
          <p className="font-mono text-xs break-all text-muted-foreground">
            {servers.length > 0 ? servers.join("  ") : "the servers the network hands out"}
          </p>
          <p className="text-body">
            Fallback: {fallback.length > 0 ? fallback.join("  ") : "the host's defaults"}. Cache:{" "}
            {CACHE.find((option) => option.value === (draft.cache || "default"))?.label}.
          </p>
        </>
      ),
      action: async () => {
        try {
          finish(
            await post<DNSApplied>("/network/dns/", {
              servers,
              fallback,
              domains,
              dnssec: draft.dnssec,
              dnsOverTLS: draft.dot,
              cache: draft.cache,
              ...(verificationName.trim() ? { verificationName: verificationName.trim() } : {}),
            }),
          )
        } catch (err) {
          refuse(err)
          throw err
        }
        return "reported"
      },
    })

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
        if (dirty && !invalid && !blocked) apply()
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
            label="Verification name"
            htmlFor="dns-verification-name"
            hint="Optional: a name your private network resolves, such as nas.home.arpa. Leave empty to check public names after applying."
          >
            <Input
              id="dns-verification-name"
              value={verificationName}
              onChange={(event) => setVerificationName(event.target.value)}
              disabled={Boolean(blocked)}
              placeholder="nas.home.arpa"
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
          <Button type="submit" disabled={!dirty || invalid || Boolean(blocked)}>
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
        {applied && <AppliedResult applied={applied} />}
      </div>
      {dialog}
    </form>
  )
}

/** What the server found after the change: the name that resolved and how long it took, and any warning. */
function AppliedResult({ applied }: { applied: DNSApplied }) {
  return (
    <div className="flex min-w-0 animate-rise flex-col gap-2" role="status">
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
      {applied.warning && (
        <Notice title="Applied, with a warning" tone="warning">
          {applied.warning}
        </Notice>
      )}
    </div>
  )
}
