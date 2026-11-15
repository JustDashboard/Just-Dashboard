"use client"

import { useState } from "react"
import { post } from "@/lib/api"
import { calendarDate, relativeTime, shortSha } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  BlocklistPreview,
  ProtectionBlocklist,
  ProtectionView,
  SessionPreview,
  SessionResult,
} from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { Field } from "@/components/form"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { impactTone, totalWord } from "@/components/network/gateway/reading"
import {
  coverageWord,
  diffWord,
  listFacts,
  listState,
} from "@/components/network/protection/reading"
import { useAuth } from "@/hooks/use-auth"

const KIND_WORD: Record<ProtectionBlocklist["kind"], string> = {
  country: "Countries list",
  feed: "Feed list",
  manual: "Typed addresses list",
}

/**
 * How a list stands, at the head of its editor: its state, the last fetch's
 * error in the server's own words, everything its one-line row has no room
 * for (what it holds, its coverage, when it was fetched and when it next is,
 * what the last change did, its signature), what it has dropped, and the
 * enforcement evidence.
 *
 * All of it hung under the list's row before, which is why no two rows were
 * one height.
 */
export function ListStanding({
  list,
  presets,
}: {
  list: ProtectionBlocklist
  presets: ProtectionView["presets"]
}) {
  const state = listState(list)
  const total = totalWord(list.total, calendarDate)
  return (
    <section aria-label={`About ${list.name}`} className="min-w-0 space-y-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <Status tone={state.tone} label={state.label} />
        <span className="text-hint text-muted-foreground">
          {KIND_WORD[list.kind]} · the kind cannot change
        </span>
      </div>
      {list.error && (
        <p role="alert" className="text-hint text-destructive">
          {list.error}
        </p>
      )}
      <p className="text-hint leading-relaxed text-muted-foreground">
        {listFacts(list, presets, relativeTime).join(" · ")}
      </p>
      <p className="numeric text-hint text-muted-foreground">
        {list.packets.toLocaleString()} packets dropped since the table was loaded
        {total ? ` · ${total}` : ""}
      </p>
      {list.cache && <Enforcement list={list} />}
    </section>
  )
}

/**
 * What is cached, what was rendered and what the kernel holds, said apart: a
 * failed refresh does not itself prove that the protection loaded before it
 * went away.
 */
function Enforcement({ list }: { list: ProtectionBlocklist }) {
  if (!list.cache) return null
  return (
    <div
      className="space-y-1 text-hint text-muted-foreground"
      aria-label={`${list.name} enforcement evidence`}
    >
      <p>
        Cache {list.cache.status}: {list.cache.count.toLocaleString()} networks
        {list.savedCount !== undefined && list.savedCount !== list.cache.count
          ? ` · last fetched count ${list.savedCount.toLocaleString()}`
          : ""}
        {list.runtime
          ? ` · kernel ${list.runtime.status}: ${list.runtime.count === null ? "unknown" : list.runtime.count.toLocaleString()} networks`
          : " · kernel unknown"}
      </p>
      {list.cache.error && <p role="alert">{list.cache.error}</p>}
      {list.runtime?.error && <p role="alert">{list.runtime.error}</p>}
      <details>
        <summary className="cursor-pointer focus-ring">Policy generations</summary>
        <p className="break-all">Cache: {list.cache.generation || "unavailable"}</p>
        <p className="break-all">Render: {list.renderedGeneration || "unavailable"}</p>
        <p className="break-all">Kernel: {list.runtime?.generation || "unavailable"}</p>
      </details>
    </div>
  )
}

/**
 * What saving a list would block, before it is saved: how much address space
 * it takes, how it differs from what the list holds now, this server's own
 * networks it would cover, the trusted networks and exceptions that keep
 * passing, and the sessions open now from inside it — which carry on, because
 * a list refuses new connections only. A country list adds what its geography
 * actually is: registry allocations, not where a machine stands.
 */
export function BlocklistPreviewView({ preview }: { preview: BlocklistPreview }) {
  if (preview.error) {
    return (
      <p role="alert" className="text-hint text-destructive">
        {preview.error}
      </p>
    )
  }
  return (
    <section aria-label="What saving would block" className="min-w-0 space-y-3">
      {preview.refused && (
        <p role="alert" className="text-hint text-destructive">
          {preview.refused}
        </p>
      )}
      <p className="text-hint text-muted-foreground">
        <span className="numeric text-foreground">{preview.networks.toLocaleString()}</span>{" "}
        networks · {coverageWord(preview.coverage) ?? "no addresses"}
        {preview.diff ? ` · ${diffWord(preview.diff)}` : ""}
      </p>
      {preview.diff &&
        preview.diff.baseline &&
        (preview.diff.added > 0 || preview.diff.removed > 0) && (
          <p className="text-hint text-muted-foreground">
            {preview.diff.addedSample?.length
              ? `Added: ${preview.diff.addedSample.join(", ")}`
              : ""}
            {preview.diff.addedSample?.length && preview.diff.removedSample?.length ? " · " : ""}
            {preview.diff.removedSample?.length
              ? `Removed: ${preview.diff.removedSample.join(", ")}`
              : ""}
          </p>
        )}
      {preview.impacts.length > 0 && (
        <ul className="space-y-1.5">
          {preview.impacts.map((impact, index) => (
            <li key={index} className="flex min-w-0 items-baseline gap-2 text-hint">
              <Tag tone={impactTone(impact)} className="shrink-0">
                {impact.severity === "warning" ? "check" : "note"}
              </Tag>
              <span className="min-w-0 text-muted-foreground">{impact.message}</span>
            </li>
          ))}
        </ul>
      )}
      {preview.trustedOverlap.length > 0 && (
        <p className="text-hint text-muted-foreground">
          Keeps passing: {preview.trustedOverlap.join("; ")}.
        </p>
      )}
      {preview.geography && <Geography geography={preview.geography} />}
      {preview.sources.length > 0 && <Provenance sources={preview.sources} />}
    </section>
  )
}

/** What a country list's geography is, and what it is not. */
export function Geography({
  geography,
}: {
  geography: NonNullable<BlocklistPreview["geography"]>
}) {
  return (
    <div className="space-y-1 text-hint text-muted-foreground">
      <p>
        <span className="font-medium text-foreground">Approximate geography.</span>{" "}
        {geography.basis} Source: {geography.source}
      </p>
      <ul className="list-disc space-y-0.5 pl-5">
        {geography.limits.map((limit) => (
          <li key={limit}>{limit}</li>
        ))}
      </ul>
    </div>
  )
}

/** Where each URL of a fetched list came from, and what it held. */
export function Provenance({ sources }: { sources: NonNullable<ProtectionBlocklist["sources"]> }) {
  return (
    <details className="text-hint text-muted-foreground">
      <summary className="cursor-pointer focus-ring">Where it came from</summary>
      <ul className="mt-1.5 space-y-1">
        {sources.map((s) => (
          <li key={s.url} className="min-w-0 break-all">
            <span className="font-mono text-foreground">{s.url}</span>
            {" — "}
            {s.status === "absent"
              ? "no zone published for this family"
              : s.status === "unchanged"
                ? `unchanged since its validators, ${relativeTime(s.fetchedAt)}`
                : `${s.networks.toLocaleString()} networks${s.skipped ? `, ${s.skipped.toLocaleString()} lines skipped` : ""}, ${s.bytes.toLocaleString()} bytes, sha256 ${shortSha(s.sha256, 12)}, ${relativeTime(s.fetchedAt)}`}
            {s.signed && " · signature verified"}
          </li>
        ))}
      </ul>
    </details>
  )
}

/**
 * Ending the sessions a list already refuses new connections from.
 *
 * A list never cuts what is open; this is the separate, explicit step that
 * does. It counts first, then ends exactly those tracked connections: their
 * next packets meet the list as new connections and are dropped. It cannot
 * be undone, so it asks for the destructive capability and a confirmation.
 */
export function SessionRevoke({ list }: { list: ProtectionBlocklist }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [network, setNetwork] = useState(list.kind === "manual" ? (list.entries[0] ?? "") : "")
  const [preview, setPreview] = useState<SessionPreview>()
  const [busy, setBusy] = useState(false)
  const count = async () => {
    setBusy(true)
    try {
      setPreview(
        await post<SessionPreview>("/network/protection/sessions/preview", {
          network: network.trim(),
          blocklistId: list.id,
        }),
      )
    } catch (err) {
      setPreview(undefined)
      notify.error("The sessions were not counted", err)
    } finally {
      setBusy(false)
    }
  }
  const end = () =>
    confirm({
      title: `End the open sessions from ${network.trim()}`,
      confirmLabel: "End sessions",
      description: (
        <p>
          {preview?.connections.tracked.toLocaleString() ?? "The"} tracked connections from{" "}
          {network.trim()} stop at their next packet, which {list.name} then drops. This cannot be
          undone.
        </p>
      ),
      action: async () => {
        const res = await post<SessionResult>("/network/protection/sessions/revoke", {
          network: network.trim(),
          blocklistId: list.id,
        })
        notify.success(`${res.ended.toLocaleString()} sessions ended`, {
          description: res.failed ? `${res.failed} could not be ended: ${res.error}` : undefined,
        })
        setPreview(undefined)
      },
    })
  return (
    <section aria-label="Open sessions" className="min-w-0 space-y-2 border-t border-hairline pt-4">
      <p className="text-body font-medium">Sessions already open</p>
      <p className="text-hint text-muted-foreground">
        The list refuses new connections only; what is open carries on until it closes. End the
        sessions from a network inside the list here.
      </p>
      <div className="flex flex-wrap items-end gap-2">
        <Field label="Network" htmlFor={`sessions-${list.id}`} className="min-w-48 flex-1">
          <Input
            id={`sessions-${list.id}`}
            value={network}
            placeholder="198.51.100.0/24"
            onChange={(event) => {
              setNetwork(event.target.value)
              setPreview(undefined)
            }}
            className="font-mono"
            autoComplete="off"
          />
        </Field>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => void count()}
          disabled={!can("system.admin") || busy || !network.trim()}
          pending={busy}
        >
          Count sessions
        </Button>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={end}
          disabled={
            !can("destructive") ||
            !preview ||
            Boolean(preview.refused) ||
            !preview.connections.tracked
          }
        >
          End them
        </Button>
      </div>
      {preview && (
        <p className="text-hint text-muted-foreground" aria-live="polite">
          {preview.refused ??
            `${preview.connections.tracked.toLocaleString()} open from ${preview.connections.sources.toLocaleString()} addresses${preview.connections.sample.length ? ` (${preview.connections.sample.join(", ")})` : ""}.`}{" "}
          {preview.basis}
        </p>
      )}
      {dialog}
    </section>
  )
}
