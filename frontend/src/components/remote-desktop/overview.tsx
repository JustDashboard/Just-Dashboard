"use client"

import Link from "next/link"
import { useMemo, useState } from "react"
import { Modal } from "@/components/modal"
import { useConfirm } from "@/components/confirm-dialog"
import { IconAction } from "@/components/icon-action"
import { Plus, RefreshClockwise, TerminalWindow } from "@/components/icons"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, Section } from "@/components/page"
import { ProductGlyph, ProductLogos } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useMetrics } from "@/hooks/use-metrics"
import { del, put } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import {
  candidates,
  isoOf,
  osLabel,
  osProduct,
  overviewVerdict,
  peerName,
  peerOS,
  REMOTE_DESKTOP_HREF,
  type RdComputer,
  type RdComputerView,
  type RdOverview,
} from "@/lib/remote-desktop"
import { notify } from "@/lib/toast"
import { ComputerCard } from "@/components/remote-desktop/computer-card"
import {
  ComputerFields,
  draftOf,
  inputOf,
  type ComputerDraft,
} from "@/components/remote-desktop/computer-form"
import { EngineNotice, EngineSetup } from "@/components/remote-desktop/engine"
import { ComputerMark } from "@/components/remote-desktop/marks"
import { ComputerPicture } from "@/components/remote-desktop/picture"
import { useRemoteDesktop } from "@/components/remote-desktop/use-remote-desktop"

const NEW_HREF = `${REMOTE_DESKTOP_HREF}/new`

/**
 * Remote Desktop: the operator's own computers, opened from the browser.
 *
 * A reading page (§15). It opens on the computers as one line — their
 * systems as their own marks, how many there are and how many can be opened
 * now, the open sessions, and the path they take — with the verdict and Add
 * a computer at its end. Under it the picture of how a session travels, from
 * this browser through guacd on this server to each computer, wired by
 * whether it answers. Then the computers, each a card that opens its screen,
 * and the tailnet's desktops that are not added yet.
 *
 * A server that has never had the engine opens on the one thing to do: the
 * install, with its output under it.
 */
export function RemoteDesktopOverview() {
  const { data, error, refresh, hueOf } = useRemoteDesktop()
  const { host } = useMetrics()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<RdComputer | null>(null)
  const offered = useMemo(() => (data ? candidates(data.tailnet, data.computers) : []), [data])

  const add = (
    <Button size="sm" asChild>
      <Link href={NEW_HREF}>
        <Plus className="size-4" />
        Add a computer
      </Link>
    </Button>
  )

  if (!data) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Workspace" title="Remote Desktop" />
        {error ? (
          <ErrorState error={error} onRetry={refresh} />
        ) : (
          <div role="status" aria-label="Loading Remote Desktop" className="flex flex-col gap-8">
            <Skeleton className="h-16 rounded-xl" />
            <Skeleton className="h-64 rounded-xl" />
          </div>
        )}
      </Page>
    )
  }

  if (data.engine.state === "missing" && data.computers.length === 0) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Workspace" title="Remote Desktop" />
        <EngineSetup onReady={refresh} />
      </Page>
    )
  }

  const remove = (computer: RdComputerView) =>
    confirm({
      title: `Remove ${computer.name}`,
      description:
        "The dashboard forgets this computer and the password it saved for it. Nothing changes on the computer itself.",
      subject: {
        mark: <ComputerMark os={computer.os} size="sm" />,
        name: computer.name,
        facts: `${osLabel(computer.os)} · ${computer.host}:${computer.port}`,
      },
      confirmLabel: "Remove",
      action: async () => {
        await del(`/remote-desktop/computers/${computer.id}`)
        refresh()
      },
    })

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Workspace" title="Remote Desktop" />

      <HostIdentity
        className="animate-rise"
        logo={<Marks overview={data} />}
        fallback={TerminalWindow}
        title={
          <>
            Remote Desktop{" "}
            <span className="font-mono text-body font-normal text-muted-foreground">
              guacd {data.engine.version}
            </span>
          </>
        }
        facts={<Facts overview={data} />}
        aside={
          <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
            <Verdict overview={data} />
            <span className="flex items-center gap-2">
              <IconAction label="Check every computer again" onClick={refresh}>
                <RefreshClockwise />
              </IconAction>
              {add}
            </span>
          </div>
        }
      />

      {data.engine.state !== "running" && <EngineNotice engine={data.engine} onReady={refresh} />}

      {data.computers.length > 0 ? (
        <>
          <ComputerPicture overview={data} hueOf={hueOf} hostname={host?.hostname} />
          <Section title="Computers">
            <ul className="grid grid-cols-[repeat(auto-fill,minmax(19rem,1fr))] gap-3">
              {data.computers.map((computer, index) => (
                <ComputerCard
                  key={computer.id}
                  computer={computer}
                  index={index}
                  onEdit={() => setEditing(computer)}
                  onRemove={() => remove(computer)}
                />
              ))}
            </ul>
          </Section>
        </>
      ) : (
        <EmptyState
          mark={<ProductLogos ids={["windows", "apple", "linux"]} size="md" />}
          title="No computers yet"
          description="Add a PC or a Mac and the dashboard walks you through turning on its remote desktop, then opens its screen here."
          action={add}
        />
      )}

      <OnTheTailnet overview={data} offered={offered} />

      <EditComputer
        computer={editing}
        layouts={data.layouts}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null)
          refresh()
        }}
      />
      {dialog}
    </Page>
  )
}

/** The computers' systems, commonest first, as the identity line's mark. */
function Marks({ overview }: { overview: RdOverview }) {
  const counts = new Map<string, number>()
  for (const computer of overview.computers) {
    const id = osProduct(computer.os)
    counts.set(id, (counts.get(id) ?? 0) + 1)
  }
  const ids = [...counts.entries()].sort((a, b) => b[1] - a[1]).map(([id]) => id)
  if (ids.length === 0) ids.push("guacamole")
  return (
    <span className="flex h-12 shrink-0 items-center">
      <ProductLogos ids={ids} size="md" />
    </span>
  )
}

function Facts({ overview }: { overview: RdOverview }) {
  const { computers, tailnet, sessions } = overview
  const ready = computers.filter((computer) => computer.reach.state === "open").length
  const live = computers.reduce((sum, computer) => sum + computer.live, 0)
  const last = sessions.find((session) => session.endedAt > 0)
  const facts = [
    <span key="count" className="numeric">
      {plural(computers.length, "computer")}
    </span>,
    computers.length > 0 && (
      <span key="ready" className="numeric">
        {ready} ready now
      </span>
    ),
    live > 0 && (
      <span key="live" className="numeric font-medium text-success">
        {plural(live, "session")} open
      </span>
    ),
    tailnet.installed ? (
      <Link
        key="tailnet"
        href="/network/vpn"
        className="inline-flex items-center gap-1.5 rounded-sm focus-ring transition-colors hover:text-foreground hover:underline"
      >
        <ProductGlyph id="tailscale" className="size-3.5" />
        over Tailscale
      </Link>
    ) : (
      <Link
        key="tailnet"
        href="/network/vpn"
        className="rounded-sm font-medium text-warning focus-ring hover:underline"
      >
        this server is not on Tailscale
      </Link>
    ),
    <span key="ports">nothing open to the internet</span>,
    last && (
      <span key="last" className="numeric">
        last session {relativeTime(isoOf(last.endedAt))}
      </span>
    ),
  ]
    .filter(Boolean)
    .flatMap((fact, index) => (index === 0 ? [fact] : [<FactDot key={`dot-${index}`} />, fact]))
  return <>{facts}</>
}

function Verdict({ overview }: { overview: RdOverview }) {
  const verdict = overviewVerdict(overview)
  if (!verdict) return null
  return (
    <Status
      tone={
        verdict.tone === "success" ? "running" : verdict.tone === "warning" ? "warning" : "unknown"
      }
      live={"live" in verdict}
      label={verdict.label}
    />
  )
}

/**
 * The tailnet's desktops the dashboard does not know yet. Each is one press
 * from the add flow with its address, system and name already filled in.
 */
function OnTheTailnet({
  overview,
  offered,
}: {
  overview: RdOverview
  offered: RdOverview["tailnet"]["peers"]
}) {
  if (!overview.tailnet.installed) {
    return (
      <Section title="On your tailnet">
        <Notice title="This server is not on Tailscale">
          <p className="text-body text-muted-foreground">
            Remote Desktop reaches your computers over Tailscale, so neither Remote Desktop nor
            Screen Sharing is ever opened to the internet.{" "}
            <Link href="/network/vpn" className="font-medium text-foreground hover:underline">
              Set up Tailscale
            </Link>{" "}
            on this server and on each computer, or add a computer this server can already reach.
          </p>
        </Notice>
      </Section>
    )
  }
  if (offered.length === 0) return null
  return (
    <Section title="On your tailnet">
      <ul aria-label="Desktops on the tailnet" className="flex min-w-0 flex-col">
        {offered.map((peer, index) => {
          const os = peerOS(peer.os)!
          return (
            <li key={peer.id} className={index > 0 ? "border-t border-hairline" : undefined}>
              <div className="flex min-w-0 items-center gap-3 py-2.5">
                <ComputerMark
                  os={os}
                  size="sm"
                  reach={{
                    kind: peer.online ? "open" : "offline",
                    tone: peer.online ? "success" : "neutral",
                    label: peer.online ? "Online" : "Offline",
                  }}
                />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-body font-medium">{peerName(peer)}</p>
                  <p className="truncate text-hint text-muted-foreground">
                    {osLabel(os)} · <span className="font-mono">{peer.address}</span> ·{" "}
                    {peer.online
                      ? peer.direct
                        ? "online, direct"
                        : "online"
                      : peer.lastSeen
                        ? `seen ${relativeTime(isoOf(peer.lastSeen))}`
                        : "offline"}
                  </p>
                </div>
                <Button size="sm" variant="outline" asChild>
                  <Link
                    href={`${NEW_HREF}?peer=${encodeURIComponent(peer.id)}`}
                    aria-label={`Add ${peerName(peer)}`}
                  >
                    <Plus className="size-3.5" />
                    Add
                  </Link>
                </Button>
              </div>
            </li>
          )
        })}
      </ul>
    </Section>
  )
}

function EditComputer({
  computer,
  layouts,
  onClose,
  onSaved,
}: {
  computer: RdComputer | null
  layouts: string[]
  onClose: () => void
  onSaved: () => void
}) {
  const [draft, setDraft] = useState<ComputerDraft | null>(null)
  const [saving, setSaving] = useState(false)
  const [held, setHeld] = useState<string | null>(null)
  // A different computer opened in the dialog starts from its own fields.
  if (computer && held !== computer.id) {
    setHeld(computer.id)
    setDraft(draftOf(computer))
  }
  const save = async () => {
    if (!computer || !draft) return
    setSaving(true)
    try {
      await put(`/remote-desktop/computers/${computer.id}`, inputOf(draft, true))
      notify.success(`Saved ${draft.name}`)
      setHeld(null)
      onSaved()
    } catch (err) {
      notify.error("Could not save the computer", err)
    } finally {
      setSaving(false)
    }
  }
  return (
    <Modal
      open={computer !== null}
      onOpenChange={(open) => {
        if (!open) {
          setHeld(null)
          onClose()
        }
      }}
      title={computer ? `Edit ${computer.name}` : "Edit"}
      size="sm"
      footer={
        <Button pending={saving} onClick={() => void save()}>
          Save
        </Button>
      }
    >
      {draft && (
        <ComputerFields
          id="edit-computer"
          draft={draft}
          onChange={(next) => setDraft((current) => (current ? { ...current, ...next } : current))}
          layouts={layouts}
          editing
        />
      )}
    </Modal>
  )
}
