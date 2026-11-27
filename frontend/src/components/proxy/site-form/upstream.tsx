"use client"

import { Plus, Trash } from "@/components/icons"
import type { PoolMethod, PoolServer, RetryCondition, SitePool, SiteSpec } from "@/lib/types"
import { Disclosure, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Group } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Field } from "@/components/proxy/site-form/fields"

/** The Host header and how nginx talks to an HTTPS upstream. */
export function UpstreamAdvanced({
  spec,
  set,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
}) {
  const https =
    spec.pool?.scheme === "https" ||
    [spec.pool ? "" : spec.upstream, ...spec.locations.map((loc) => loc.upstream)].some((u) =>
      u?.startsWith("https://"),
    )
  const host = spec.hostHeader ?? ""
  const facts = [
    host === "upstream" && "Host: the upstream's",
    host === "custom" && `Host: ${spec.hostHeaderValue ?? ""}`,
    spec.upstreamSni && "sends SNI",
    spec.upstreamVerify && "verifies the certificate",
  ].filter(Boolean)
  return (
    <Disclosure
      quiet
      summary="Advanced upstream"
      facts={facts.length > 0 ? facts.join(" · ") : undefined}
    >
      <div className="space-y-3 pt-2">
        <Field
          label="Host header"
          htmlFor="site-host-header"
          tip="What the application is told it was asked for. Most applications want the visitor's."
        >
          <ToggleGroup
            id="site-host-header"
            type="single"
            value={host || "visitor"}
            onValueChange={(v) => {
              if (!v) return
              set("hostHeader", v === "visitor" ? undefined : (v as SiteSpec["hostHeader"]))
            }}
            variant="outline"
            size="sm"
            className="w-full"
          >
            <ToggleGroupItem value="visitor" className="flex-1 text-hint">
              The visitor&apos;s
            </ToggleGroupItem>
            <ToggleGroupItem
              value="upstream"
              disabled={Boolean(spec.pool)}
              className="flex-1 text-hint"
            >
              The upstream&apos;s
            </ToggleGroupItem>
            <ToggleGroupItem value="custom" className="flex-1 text-hint">
              Custom
            </ToggleGroupItem>
          </ToggleGroup>
        </Field>
        {host === "custom" && (
          <Field label="Host" htmlFor="site-host-value">
            <Input
              id="site-host-value"
              value={spec.hostHeaderValue ?? ""}
              onChange={(e) => set("hostHeaderValue", e.target.value)}
              placeholder="app.internal"
              className="font-mono text-xs"
            />
          </Field>
        )}
        {!https && (
          <FormNote>
            The settings below apply to an https:// upstream, and this site forwards to none.
          </FormNote>
        )}
        <OptionList>
          <OptionRow
            title="Send the name in the TLS handshake (SNI)"
            hint="nginx does not by default, and a host serving several names over HTTPS answers without one with the wrong certificate or not at all."
            checked={spec.upstreamSni ?? false}
            onCheckedChange={(v) => set("upstreamSni", v)}
          />
          <OptionRow
            title="Verify the upstream's certificate"
            hint="Without this nginx accepts any certificate the upstream presents."
            checked={spec.upstreamVerify ?? false}
            onCheckedChange={(v) => set("upstreamVerify", v)}
          >
            <Field
              label="CA file"
              htmlFor="site-upstream-ca"
              tip="Empty checks against the system's CAs."
            >
              <Input
                id="site-upstream-ca"
                value={spec.upstreamCa ?? ""}
                onChange={(e) => set("upstreamCa", e.target.value)}
                placeholder="/etc/ssl/certs/ca-certificates.crt"
                className="font-mono text-xs"
              />
            </Field>
          </OptionRow>
        </OptionList>
        {(spec.upstreamSni || spec.upstreamVerify) && (
          <Field
            label="TLS name"
            htmlFor="site-upstream-name"
            hint={
              spec.pool?.scheme === "https"
                ? "Sent and checked for every server of the pool — required, since the pool's own name is internal."
                : "Sent and checked in place of the upstream's host — for an upstream addressed by IP. Empty uses the host."
            }
          >
            <Input
              id="site-upstream-name"
              value={spec.upstreamTlsName ?? ""}
              onChange={(e) => set("upstreamTlsName", e.target.value)}
              placeholder="api.example.com"
              className="font-mono text-xs"
            />
          </Field>
        )}
      </div>
    </Disclosure>
  )
}

const POOL_METHODS: { value: PoolMethod; label: string }[] = [
  { value: "", label: "Round robin" },
  { value: "least_conn", label: "Least connections" },
  { value: "ip_hash", label: "Client IP" },
  { value: "hash", label: "URI hash" },
  { value: "random", label: "Random" },
]

const RETRY_CONDITIONS: { value: RetryCondition; label: string }[] = [
  { value: "error", label: "Connection error" },
  { value: "timeout", label: "Timeout" },
  { value: "invalid_header", label: "Invalid response" },
  { value: "http_502", label: "502" },
  { value: "http_503", label: "503" },
  { value: "http_504", label: "504" },
  { value: "http_500", label: "500" },
  { value: "http_429", label: "429" },
  { value: "non_idempotent", label: "Also POST, PUT and DELETE" },
]

/** nginx's own when proxy_next_upstream is not written. */
const DEFAULT_RETRY: RetryCondition[] = ["error", "timeout"]

/** A new pool starts from the single upstream it replaces, when that is a bare address. */
export function poolFrom(upstream: string | undefined): SitePool {
  const match = /^(https?):\/\/([^/]+)$/.exec(upstream?.trim() ?? "")
  if (!match) return { servers: [{ address: "" }] }
  return { scheme: match[1] === "https" ? "https" : undefined, servers: [{ address: match[2] }] }
}

/** Whole numbers only; empty is nginx's default. */
function count(value: string): number | undefined {
  return Number(value) || undefined
}

/** Several servers sharing the site's requests, as one nginx upstream block. */
export function PoolField({
  pool,
  onChange,
}: {
  pool: SitePool
  onChange: (pool: SitePool) => void
}) {
  const set = <K extends keyof SitePool>(key: K, value: SitePool[K]) =>
    onChange({ ...pool, [key]: value })
  const update = (i: number, patch: Partial<PoolServer>) =>
    set(
      "servers",
      pool.servers.map((server, j) => (j === i ? { ...server, ...patch } : server)),
    )
  const method = pool.method ?? ""
  // nginx refuses a backup server beside a method that picks from the request.
  const noBackup = method === "ip_hash" || method === "hash" || method === "random"
  const retry = pool.retryOn?.length ? pool.retryOn : DEFAULT_RETRY
  const toggleRetry = (cond: RetryCondition, on: boolean) => {
    const next = RETRY_CONDITIONS.map((c) => c.value).filter((c) =>
      c === cond ? on : retry.includes(c),
    )
    set("retryOn", next.length > 0 ? next : ["off"])
  }
  return (
    <div className="space-y-3">
      <FieldRow>
        <Field label="Balancing" htmlFor="site-pool-method">
          <Select
            value={method || "round_robin"}
            onValueChange={(v) => {
              const next = v === "round_robin" ? "" : (v as PoolMethod)
              const keepsBackup = next === "" || next === "least_conn"
              onChange({
                ...pool,
                method: next || undefined,
                servers: keepsBackup
                  ? pool.servers
                  : pool.servers.map((server) => ({ ...server, backup: undefined })),
              })
            }}
          >
            <SelectTrigger id="site-pool-method" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {POOL_METHODS.map((m) => (
                <SelectItem key={m.value || "round_robin"} value={m.value || "round_robin"}>
                  {m.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label="Spoken to over" htmlFor="site-pool-scheme">
          <ToggleGroup
            id="site-pool-scheme"
            type="single"
            value={pool.scheme || "http"}
            onValueChange={(v) => v && set("scheme", v === "https" ? "https" : undefined)}
            variant="outline"
            size="sm"
            className="w-full"
          >
            <ToggleGroupItem value="http" className="flex-1 text-hint">
              http
            </ToggleGroupItem>
            <ToggleGroupItem value="https" className="flex-1 text-hint">
              https
            </ToggleGroupItem>
          </ToggleGroup>
        </Field>
      </FieldRow>
      {pool.servers.map((server, i) => (
        <Group key={i} className="space-y-2">
          <div className="flex items-center gap-2">
            <Input
              value={server.address}
              onChange={(e) => update(i, { address: e.target.value })}
              placeholder="127.0.0.1:3000"
              aria-label={`Server ${i + 1} address`}
              className="font-mono text-xs"
            />
            <IconAction
              label={`Remove ${server.address || `server ${i + 1}`}`}
              className="text-destructive"
              disabled={pool.servers.length === 1}
              onClick={() =>
                set(
                  "servers",
                  pool.servers.filter((_, j) => j !== i),
                )
              }
            >
              <Trash />
            </IconAction>
          </div>
          <FieldRow columns={3}>
            <Field label="Weight" htmlFor={`site-pool-${i}-weight`}>
              <Input
                id={`site-pool-${i}-weight`}
                value={server.weight ? String(server.weight) : ""}
                inputMode="numeric"
                placeholder="1"
                onChange={(e) => update(i, { weight: count(e.target.value) })}
                className="font-mono text-xs"
              />
            </Field>
            <Field label="Max fails" htmlFor={`site-pool-${i}-fails`}>
              <Input
                id={`site-pool-${i}-fails`}
                value={server.maxFails ? String(server.maxFails) : ""}
                inputMode="numeric"
                placeholder="1"
                onChange={(e) => update(i, { maxFails: count(e.target.value) })}
                className="font-mono text-xs"
              />
            </Field>
            <Field label="Fail timeout" htmlFor={`site-pool-${i}-timeout`}>
              <Input
                id={`site-pool-${i}-timeout`}
                value={server.failTimeout ? String(server.failTimeout) : ""}
                inputMode="numeric"
                placeholder="10"
                onChange={(e) => update(i, { failTimeout: count(e.target.value) })}
                className="font-mono text-xs"
              />
            </Field>
          </FieldRow>
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            <label className="flex items-center gap-2 text-hint text-muted-foreground">
              <Checkbox
                checked={server.backup ?? false}
                disabled={noBackup}
                onCheckedChange={(v) => update(i, { backup: Boolean(v) || undefined })}
              />
              Backup: used only while the others are unavailable
            </label>
            <label className="flex items-center gap-2 text-hint text-muted-foreground">
              <Checkbox
                checked={server.down ?? false}
                onCheckedChange={(v) => update(i, { down: Boolean(v) || undefined })}
              />
              Down: sent nothing
            </label>
          </div>
        </Group>
      ))}
      <Button
        size="sm"
        variant="outline"
        onClick={() => set("servers", [...pool.servers, { address: "" }])}
      >
        <Plus className="size-3.5" />
        Add a server
      </Button>
      <FormNote>
        Health checking is passive: after max fails of a server&apos;s real requests fail within its
        fail timeout, nginx sends it nothing for the fail timeout, then tries it again. Nothing
        probes the servers in between.
        {noBackup && " A backup server needs round robin or least connections."}
      </FormNote>
      <Field
        label="Retry on the next server after"
        tip="Nothing ticked never retries. nginx does not retry a POST, PUT or DELETE unless told to, since the first server may have acted on it."
      >
        <div className="flex flex-wrap gap-x-4 gap-y-1">
          {RETRY_CONDITIONS.map((c) => (
            <label
              key={c.value}
              className="flex items-center gap-2 text-hint text-muted-foreground"
            >
              <Checkbox
                checked={retry.includes(c.value)}
                onCheckedChange={(v) => toggleRetry(c.value, Boolean(v))}
              />
              {c.label}
            </label>
          ))}
        </div>
      </Field>
      <FieldRow>
        <Field
          label="Tries"
          htmlFor="site-pool-tries"
          tip="Servers one request may try, the first included. Empty is no cap."
        >
          <Input
            id="site-pool-tries"
            value={pool.tries ? String(pool.tries) : ""}
            inputMode="numeric"
            placeholder="no cap"
            onChange={(e) => set("tries", count(e.target.value))}
            className="font-mono text-xs"
          />
        </Field>
        <Field
          label="Kept-open connections"
          htmlFor="site-pool-keepalive"
          tip="Idle connections each nginx worker keeps to the servers. Empty opens one per request."
        >
          <Input
            id="site-pool-keepalive"
            value={pool.keepalive ? String(pool.keepalive) : ""}
            inputMode="numeric"
            placeholder="32"
            onChange={(e) => set("keepalive", count(e.target.value))}
            className="font-mono text-xs"
          />
        </Field>
      </FieldRow>
    </div>
  )
}
