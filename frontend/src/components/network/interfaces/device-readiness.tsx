"use client"

import Link from "next/link"
import { useState } from "react"
import { get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { NetworkLink, NetworkLinkReadiness } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Field } from "@/components/form"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { parseRemotes } from "./device-reading"

/** The kinds the backend reads readiness for. */
export const READINESS_KINDS = new Set([
  "vxlan",
  "gre",
  "gretap",
  "ip6gre",
  "ip6gretap",
  "macvlan",
  "dummy",
])

const TONE: Record<NetworkLinkReadiness["checks"][number]["state"], DotTone> = {
  ok: "running",
  warning: "warning",
  failed: "danger",
  unknown: "unknown",
  info: "notice",
}

const WORD: Record<NetworkLinkReadiness["checks"][number]["state"], string> = {
  ok: "Ready",
  warning: "Check",
  failed: "Failing",
  unknown: "Unknown",
  info: "Note",
}

/**
 * What this host alone can establish about whether a tunnel or a virtual
 * device can carry traffic: its underlay, its ends, its MTU and whether
 * anything has come in through it — with what it cannot see said under the
 * checks. Nothing here sends a packet to the other end.
 */
export function DeviceReadiness({
  link,
  open,
  managed,
  remotes,
  onChanged,
}: {
  link: NetworkLink
  open: boolean
  managed: boolean
  /** A managed unicast VXLAN's further flood ends, from the spec. */
  remotes?: string[]
  onChanged: () => void
}) {
  const readiness = usePoll<NetworkLinkReadiness>(
    (signal) => get(`/network/links/${encodeURIComponent(link.name)}/readiness`, undefined, signal),
    30_000,
    [link.name],
    { enabled: open },
  )
  const r = readiness.data
  const title =
    link.kind === "macvlan"
      ? "Isolation and reach"
      : link.kind === "dummy"
        ? "What uses it"
        : "Underlay and liveness"
  return (
    <Panel plain>
      <PanelHeader title={title} />
      <PanelBody>
        {r && (
          <NetworkReadWarning
            error={readiness.error}
            refresh={readiness.refresh}
            lastSuccess={readiness.lastSuccess}
            reading="readiness checks"
          />
        )}
        {!r ? (
          readiness.error ? (
            <Notice tone="warning" title="Readiness could not be read">
              {readiness.error.message}
            </Notice>
          ) : (
            <p className="text-body text-muted-foreground">Checking…</p>
          )
        ) : (
          <div className="flex flex-col gap-4">
            <ul className="flex flex-col divide-y divide-hairline" aria-label="Readiness checks">
              {r.checks.map((c) => (
                <li key={c.id} className="flex min-w-0 flex-col gap-1 py-2 text-body">
                  <span className="flex items-center gap-3">
                    <Status tone={TONE[c.state]} label={WORD[c.state]} className="w-24 shrink-0" />
                    <span className="font-medium">{c.label}</span>
                  </span>
                  <span className="text-hint text-muted-foreground">{c.detail}</span>
                </li>
              ))}
            </ul>
            {r.limits.length > 0 && (
              <ul className="flex list-disc flex-col gap-1 pl-5 text-hint text-muted-foreground">
                {r.limits.map((limit) => (
                  <li key={limit}>{limit}</li>
                ))}
              </ul>
            )}
          </div>
        )}
        {link.kind === "dummy" && (
          <div className="mt-4 flex flex-wrap gap-2">
            <Button size="sm" variant="outline" asChild>
              <Link href={`/network/routing?route=new&device=${encodeURIComponent(link.name)}`}>
                Route a destination into {link.name}
              </Link>
            </Button>
          </div>
        )}
        {managed && link.kind === "vxlan" && link.remote && !isMulticast(link.remote) && (
          <FloodEnds
            link={link}
            remotes={remotes ?? []}
            onChanged={() => {
              readiness.refresh()
              onChanged()
            }}
          />
        )}
      </PanelBody>
    </Panel>
  )
}

function isMulticast(address: string) {
  const first = Number(address.split(".")[0])
  return address.includes(":")
    ? address.toLowerCase().startsWith("ff")
    : first >= 224 && first <= 239
}

/**
 * A unicast VXLAN's other ends beyond its own remote: every broadcast and
 * unknown frame is copied to each of them (head-end replication), so a
 * segment of three servers needs both others here. Saved in the spec and the
 * boot unit; taking one away needs the destructive capability.
 */
function FloodEnds({
  link,
  remotes,
  onChanged,
}: {
  link: NetworkLink
  remotes: string[]
  onChanged: () => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const [draft, setDraft] = useState<string>()
  const text = draft ?? remotes.join(", ")
  const parsed = parseRemotes(text, link.remote)
  const removed = remotes.filter((r) => !parsed.remotes.includes(r))
  const changed =
    !parsed.error && (removed.length > 0 || parsed.remotes.some((r) => !remotes.includes(r)))
  const save = async () => {
    await put(`/network/links/${encodeURIComponent(link.name)}/remotes`, {
      remotes: parsed.remotes,
    })
    setDraft(undefined)
    notify.success(
      `${link.name} floods to ${parsed.remotes.length + 1} end${parsed.remotes.length ? "s" : ""}`,
    )
    onChanged()
  }
  return (
    <form
      className="mt-5 flex flex-col gap-3 border-t border-hairline pt-4"
      onSubmit={(event) => {
        event.preventDefault()
        if (!changed) return
        if (removed.length === 0) {
          void save().catch((err) => notify.error(`${link.name}: not changed`, err))
          return
        }
        confirm({
          title: `Stop flooding to ${removed.join(", ")}`,
          confirmLabel: "Remove",
          description: (
            <p>
              Broadcast and unknown frames on <span className="font-mono">{link.name}</span> stop
              reaching {removed.join(", ")}; machines behind it lose the segment until it is added
              back.
            </p>
          ),
          action: save,
        })
      }}
    >
      <Field
        label="Further ends"
        htmlFor={`${link.name}-remotes`}
        error={parsed.error}
        hint={`Besides ${link.remote}: every other server on this segment, separated by commas`}
      >
        <Input
          id={`${link.name}-remotes`}
          value={text}
          placeholder="198.51.100.8, 198.51.100.9"
          onChange={(event) => setDraft(event.target.value)}
          aria-invalid={Boolean(parsed.error)}
          className="font-mono"
          disabled={!admin}
        />
      </Field>
      <Button
        type="submit"
        size="sm"
        variant="outline"
        className="self-start"
        disabled={!admin || !changed}
      >
        Save ends
      </Button>
      {dialog}
    </form>
  )
}
