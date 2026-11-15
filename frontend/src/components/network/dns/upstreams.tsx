"use client"

import { useEffect, useRef, useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { del, post, ApiError } from "@/lib/api"
import { plural } from "@/lib/format"
import type { DNSApplied, DNSClearableList, DNSVerification, DNSView } from "@/lib/types"
import { ChoiceGrid, ProductCard } from "@/components/choice-card"
import { ConfirmDialog, useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { Field, OptionRow } from "@/components/form"
import { Globe } from "@/components/icons"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Segments } from "@/components/deploy/settings/segments"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import {
  clearedLists,
  missingTLSNames,
  parseList,
  presetChosen,
  refusedVerification,
  splitServer,
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

/** What clearing each list does, said where the switch is. */
const CLEAR: Record<DNSClearableList, string> = {
  servers: "Clear the global servers; each link's own servers still answer",
  fallback: "No fallback servers: turns off resolved's built-in fallback",
  domains: "Clear the global search and routing domains",
}

/**
 * DNS over TLS, as the three answers it has. `no` is resolved's word for off.
 * What the chosen one promises is the line under the control, so the three
 * are one row rather than three cards of prose.
 */
const DOT = [
  {
    value: "no",
    label: "Off",
    says: "Plain DNS on port 53, readable and changeable by anyone on the path.",
  },
  {
    value: "opportunistic",
    label: "Opportunistic",
    says: "Encrypted when the server offers it, plain when it does not.",
  },
  {
    value: "yes",
    label: "Required",
    says: "Plain DNS is refused; every server needs its name, like 1.1.1.1#cloudflare-dns.com.",
  },
]

/** A segmented control has no empty value, so "leave it at resolved's default" is a word of its own. */
const DNSSEC = [
  { value: "default", label: "Default", says: "Left to resolved's own default." },
  { value: "no", label: "Off", says: "Answers are not validated." },
  {
    value: "allow-downgrade",
    label: "When supported",
    says: "Validated where the server supports it.",
  },
  { value: "yes", label: "Always", says: "Validated; an answer that fails is refused." },
]

const CACHE = [
  { value: "default", label: "Default", says: "Left to the host's own policy." },
  { value: "yes", label: "Everything", says: "Every answer is cached." },
  { value: "no-negative", label: "Positive only", says: "A missing name is not cached." },
  { value: "no", label: "Off", says: "Nothing is cached." },
]

const said = (options: typeof CACHE, value: string) =>
  options.find((option) => option.value === (value || "default"))

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

/** What a resolver change came to: applied with its checks, or put back with the check that failed. */
export type ResolverOutcome =
  | { kind: "applied"; applied: DNSApplied }
  | { kind: "put-back"; reason: string; verification: DNSVerification }

/**
 * Who this server asks, as one form in the settings column. Public resolvers
 * are cards, because they are things you pick: choosing one fills the servers
 * with the `address#name` pairs DNS over TLS validates against, and the text
 * area under them is the same list for anything else. DNS over TLS, DNSSEC
 * and the cache are each a closed choice of a few words, so each is one
 * segmented row with what the chosen answer promises under it.
 *
 * The form draws no Save: it stands on the page's save bar beside the host
 * records. A wrong upstream stops every lookup on this host, the dashboard's
 * own among them, so Save asks the server for the checks the change will be
 * held to and shows them in a confirmation first; the server puts the previous
 * settings back if a required one fails, and what it found is handed to the
 * page (`onOutcome`), which draws it at the head of the column.
 */
export function ResolverSettings({
  view,
  readOnly,
  onChanged,
  onOutcome,
}: {
  view: DNSView
  /** Why nothing here can change — systemd-resolved is not running — or undefined. */
  readOnly?: string
  onChanged: () => void
  onOutcome: (outcome: ResolverOutcome | undefined) => void
}) {
  const { can } = useAuth()
  const base = baseline(view)
  const [edits, setEdits] = useState<Partial<Draft>>({})
  const [refused, setRefused] = useState<string>()
  const [planError, setPlanError] = useState<string>()
  const [planning, setPlanning] = useState(false)
  const [verificationNames, setVerificationNames] = useState("")
  // The save bar waits on the confirmation: resolved by Apply going through,
  // by a put-back, or by the dialog being closed.
  const [request, setRequest] = useState<ConfirmRequest | null>(null)
  const settle = useRef<((saved: boolean) => void) | undefined>(undefined)
  const { confirm, dialog } = useConfirm()
  const draft: Draft = { ...base, ...edits }
  const edit = (patch: Partial<Draft>) => {
    setEdits((previous) => ({ ...previous, ...patch }))
    onOutcome(undefined)
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
  // One count per setting that differs, for the bar's "3 unsaved changes".
  const changed = {
    upstreams:
      servers.join(" ") !== parseList(base.servers).join(" ") ||
      fallback.join(" ") !== parseList(base.fallback).join(" ") ||
      domains.join(" ") !== parseList(base.domains).join(" ") ||
      clear.join(" ") !== baseClear.join(" "),
    dot: draft.dot !== base.dot,
    dnssec: draft.dnssec !== base.dnssec,
    cache: draft.cache !== base.cache,
  }
  const changes = Object.values(changed).filter(Boolean).length
  const dirty = changes > 0
  // Applying and resetting are destructive routes: an administrator's session
  // narrowed to less was offered both and refused by the server.
  const blocked = !can("system.admin")
    ? "Changing the host resolver requires an administrator."
    : !can("destructive")
      ? "An administrator with destructive permission can change the host resolver."
      : (readOnly ?? refused)
  const canEdit = !blocked
  const invalid = Boolean(serversError || fallbackError)

  const finish = (applied: DNSApplied) => {
    onOutcome({ kind: "applied", applied })
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
  const save = async (): Promise<boolean> => {
    if (!dirty || invalid || !canEdit || planning) return false
    setPlanning(true)
    setPlanError(undefined)
    let plan: DNSVerification
    try {
      plan = await post<DNSVerification>("/network/dns/verification-plan", body)
    } catch (err) {
      refuse(err)
      setPlanError(err instanceof Error ? err.message : String(err))
      return false
    } finally {
      setPlanning(false)
    }
    return new Promise<boolean>((resolve) => {
      const done = (saved: boolean) => {
        settle.current = undefined
        resolve(saved)
      }
      settle.current = done
      setRequest({
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
              . Cache: {said(CACHE, draft.cache)?.label.toLowerCase()}.
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
            done(true)
          } catch (err) {
            refuse(err)
            const verification =
              err instanceof ApiError && err.code === "dns_upstream_unreachable"
                ? refusedVerification(err.body)
                : undefined
            if (!verification || !(err instanceof ApiError)) throw err
            onOutcome({ kind: "put-back", reason: err.message, verification })
            done(false)
          }
          return "reported"
        },
      })
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

  const running = view.resolved.global
  const named = running.servers.filter((s) => splitServer(s).tlsName).length
  const stats = view.resolved.statistics

  return (
    <>
      <SettingForm
        name="Resolver"
        onSave={save}
        dirty={dirty}
        changes={changes}
        saving={planning}
        invalid={invalid}
        canEdit={canEdit}
        onDiscard={() => {
          setEdits({})
          setPlanError(undefined)
        }}
        applies="immediately"
        error={planError}
        // The page says once, at the head of the column, that a reader's
        // account changes nothing here; this is the narrower refusal.
        note={blocked && !readOnly && can("system.admin") ? blocked : undefined}
      >
        <SettingSection
          id="dns-upstreams"
          title="Upstreams"
          state={
            view.managed.exists ? (
              <>
                set here in <span className="font-mono">{view.managed.path}</span>
              </>
            ) : (
              "the host's own settings"
            )
          }
          status={settingStatus({ dirty: changed.upstreams, refused: Boolean(planError) })}
          actions={
            view.managed.exists &&
            canEdit && (
              <Button type="button" size="xs" variant="outline" onClick={() => reset()}>
                Reset to the system&rsquo;s
              </Button>
            )
          }
        >
          {canEdit && (
            <div>
              <p className="mb-2 text-body font-medium">Ask a public resolver</p>
              <ChoiceGrid columns="compact" className="xl:grid-cols-3">
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
                    onClick={() => choose(preset.id)}
                  />
                ))}
              </ChoiceGrid>
            </div>
          )}
          <Field
            label="Servers"
            htmlFor="dns-servers"
            info="One per line, as 9.9.9.9 or 9.9.9.9#dns.quad9.net. Up to eight; empty uses the servers the network hands out."
            error={serversError}
          >
            <Textarea
              id="dns-servers"
              value={draft.servers}
              rows={Math.min(Math.max(servers.length + 1, 3), 9)}
              spellCheck={false}
              autoComplete="off"
              disabled={!canEdit}
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
            disabled={!canEdit}
            onChange={(checked) => edit({ clear: { ...draft.clear, servers: checked } })}
          />
          <Field
            label="Fallback servers"
            htmlFor="dns-fallback"
            info="Optional, up to eight. Used when no other server is known. Leave empty for the host's fallback defaults. Ports, %interface and #TLS names use the same format as Servers."
            error={fallbackError}
          >
            <Textarea
              id="dns-fallback"
              value={draft.fallback}
              rows={Math.min(Math.max(fallback.length + 1, 2), 9)}
              spellCheck={false}
              autoComplete="off"
              disabled={!canEdit}
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
            disabled={!canEdit}
            onChange={(checked) => edit({ clear: { ...draft.clear, fallback: checked } })}
          />
          <Field
            label="Domains"
            htmlFor="dns-domains"
            info="Search domains, or ~domain to send a domain's names only to these servers; ~. is every name."
          >
            <Input
              id="dns-domains"
              value={draft.domains}
              onChange={(event) => edit({ domains: event.target.value })}
              disabled={!canEdit}
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
              placeholder="~. lan.example.net"
            />
          </Field>
          <ClearList
            list="domains"
            empty={domains.length === 0}
            checked={Boolean(draft.clear.domains)}
            disabled={!canEdit}
            onChange={(checked) => edit({ clear: { ...draft.clear, domains: checked } })}
          />
        </SettingSection>

        <SettingSection
          title="DNS over TLS"
          state={
            running.servers.length > 0
              ? `${named} of ${plural(running.servers.length, "global server")} named`
              : "no global servers"
          }
          status={settingStatus({ dirty: changed.dot })}
        >
          <Choice
            label="DNS over TLS"
            options={DOT}
            value={draft.dot}
            disabled={!canEdit}
            onChange={(dot) => edit({ dot })}
          />
        </SettingSection>

        <SettingSection
          title="DNSSEC"
          state={
            view.resolved.active
              ? running.dnssecSupported
                ? "the server in use supports it"
                : "the server in use does not support it"
              : undefined
          }
          status={settingStatus({ dirty: changed.dnssec })}
        >
          <Choice
            label="DNSSEC"
            options={DNSSEC}
            value={draft.dnssec || "default"}
            disabled={!canEdit}
            onChange={(value) => edit({ dnssec: value === "default" ? "" : value })}
          />
        </SettingSection>

        <SettingSection
          title="Cache"
          state={stats ? `${plural(stats.cacheSize, "answer")} held` : undefined}
          status={settingStatus({ dirty: changed.cache })}
        >
          <Choice
            label="Cache mode"
            options={CACHE}
            value={draft.cache || "default"}
            disabled={!canEdit}
            onChange={(value) => edit({ cache: value === "default" ? "" : value })}
          />
        </SettingSection>

        {canEdit && (
          <SettingSection title="Verification">
            <Field
              label="Names to check"
              htmlFor="dns-verification-names"
              info="Optional, up to eight: a name in each scope this change touches, such as nas.home.arpa or git.corp.example. Each must answer after saving, or the previous settings are put back. Leave empty to check public names."
            >
              <Textarea
                id="dns-verification-names"
                aria-label="Verification names"
                value={verificationNames}
                rows={2}
                spellCheck={false}
                autoComplete="off"
                onChange={(event) => {
                  setVerificationNames(event.target.value)
                  setPlanError(undefined)
                }}
                placeholder={"nas.home.arpa\ngit.corp.example"}
                className="font-mono"
              />
            </Field>
          </SettingSection>
        )}
      </SettingForm>
      <ConfirmDialog
        request={request}
        onOpenChange={(open) => {
          if (open) return
          setRequest(null)
          // Closed without applying: the bar's Save is over, and nothing was saved.
          settle.current?.(false)
        }}
      />
      {dialog}
    </>
  )
}

/** A closed choice as one segmented row, with what the chosen answer does under it. */
function Choice({
  label,
  options,
  value,
  disabled,
  onChange,
}: {
  label: string
  options: { value: string; label: string; says: string }[]
  value: string
  disabled: boolean
  onChange: (value: string) => void
}) {
  return (
    <div className="min-w-0 space-y-2">
      <Segments
        label={label}
        value={value}
        options={options}
        onChange={onChange}
        disabled={disabled}
        fill
        className="sm:max-w-md"
      />
      {/* The one line changes its words, never its height. */}
      <p aria-live="polite" className="text-hint text-muted-foreground">
        {options.find((option) => option.value === value)?.says}
      </p>
    </div>
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
  // A switch nobody here may turn is drawn only when it is on: then it is a
  // fact about the drop-in, and off it is one more dead control.
  if (!empty || (disabled && !checked)) return null
  return (
    <OptionRow
      title={CLEAR[list]}
      checked={checked}
      disabled={disabled}
      onCheckedChange={onChange}
    />
  )
}

/**
 * What the last change came to, at the head of the settings column: every
 * check's result and any warning, or the check that failed and the settings
 * put back.
 */
export function ResolverOutcomeNote({ outcome }: { outcome: ResolverOutcome }) {
  // Save is pressed at the foot of the window and this lands at the head of
  // the column, so it is brought into view when it arrives.
  const note = useRef<HTMLDivElement>(null)
  useEffect(() => {
    note.current?.scrollIntoView({ block: "nearest", behavior: "smooth" })
  }, [outcome])
  if (outcome.kind === "put-back") {
    return (
      <div ref={note} className="flex min-w-0 animate-rise scroll-mt-6 flex-col gap-3" role="alert">
        <Notice title="Put back: a required check failed" tone="danger">
          {outcome.reason}
        </Notice>
        <VerificationChecks verification={outcome.verification} label="Verification results" />
      </div>
    )
  }
  const { applied } = outcome
  return (
    <div ref={note} className="flex min-w-0 animate-rise scroll-mt-6 flex-col gap-3" role="status">
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
