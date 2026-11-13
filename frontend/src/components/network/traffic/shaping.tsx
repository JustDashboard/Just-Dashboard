"use client"

import { useEffect, useState } from "react"
import { del, get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { NetworkLink, ShapeDevice, ShapingView } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useViewState } from "@/lib/view-state"
import { Pencil, SettingsSliders, Trash } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu, type Verb } from "@/components/verbs"
import { linkGlyph, linkProduct } from "@/components/network/marks"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { RX, TX } from "@/components/network/rate-pair"
import { CongestionPanel } from "./congestion"
import { ShapeSheet } from "./shape-sheet"
import { limitWords, queueLine, queueState } from "./words"

/**
 * How fast each device may send and receive, which queue holds the packets
 * while it waits, and how TCP paces itself over them.
 *
 * A device is one card: its mark, its name over the queue it runs and what
 * it is to this connection, its upload and download limits — upload is the
 * device's egress, download is policed on its ingress or queued on an IFB —
 * and the worst thing to know about it as a dot and a word. It was a row of a
 * framed table whose first cell ran to nine wrapped lines of evidence and
 * whose three limit columns a phone dropped. The evidence is one press away,
 * on the device's sheet, which a reader can open and only an administrator
 * can change (`shape-sheet.tsx`).
 *
 * The verbs are the card's menu: edit, and for a device the dashboard shaped,
 * clear, which the server counts as destructive and so is drawn only for an
 * account that may.
 *
 * A later read that fails keeps the cards, says so, and refuses every change
 * until a read succeeds: the limits on screen are what a change would be
 * measured against.
 */
export function Shaping({ links }: { links: NetworkLink[] | undefined }) {
  const shaping = usePoll<ShapingView>(
    (signal) => get("/network/shaping", undefined, signal),
    15_000,
  )
  const [open, setOpen] = useState<string>()
  const { can } = useAuth()
  const admin = can("system.admin")
  const mayClear = admin && can("destructive")
  const { confirm, dialog } = useConfirm()
  const [remembered, remember] = useViewState("network.traffic.queues", 4)
  const view = shaping.data
  const count = view?.devices.length
  useEffect(() => {
    if (count !== undefined && count !== remembered) remember(count)
  }, [count, remembered, remember])
  const stale = Boolean(shaping.error)
  const device = view?.devices.find((d) => d.name === open)

  const clear = (d: ShapeDevice) =>
    confirm({
      title: `Clear the limits on ${d.name}`,
      confirmLabel: "Clear",
      description: (
        <p>
          <span className="font-mono">{d.name}</span> goes back to the kernel&rsquo;s own queue with
          no upload or download limit, and is not shaped again at boot.
        </p>
      ),
      action: async () => {
        await del(`/network/shaping/${encodeURIComponent(d.name)}`, {
          ...(d.sqm ? { networkApply: "pending" as const } : {}),
        })
        shaping.refresh()
      },
    })

  return (
    <div className="flex min-w-0 flex-col gap-8">
      {view && (
        <NetworkReadWarning
          error={shaping.error}
          refresh={shaping.refresh}
          lastSuccess={shaping.lastSuccess}
        />
      )}
      <Panel plain aria-label="Queues">
        <PanelHeader
          title="Queues"
          actions={
            view && (
              <span className="numeric text-hint text-muted-foreground">
                {plural(view.devices.length, "device")}
              </span>
            )
          }
        />
        <PanelBody>
          {!view ? (
            shaping.error ? (
              <ErrorState error={shaping.error} onRetry={shaping.refresh} retryable />
            ) : (
              <QueueGhosts rows={Math.max(1, remembered)} />
            )
          ) : view.devices.length === 0 ? (
            <EmptyState
              icon={SettingsSliders}
              title="No device to shape"
              description="The kernel lists no device with a queue of its own."
            />
          ) : (
            <ChoiceList aria-label="Queues by device">
              {view.devices.map((d) => {
                const editable = admin && d.shapeable
                const verbs: Verb[] = [
                  ...(editable
                    ? [
                        {
                          key: "edit",
                          label: "Edit limits",
                          icon: Pencil,
                          disabled: stale,
                          run: () => setOpen(d.name),
                        },
                      ]
                    : []),
                  ...(mayClear && d.shapeable && d.managed
                    ? [
                        {
                          key: "clear",
                          label: "Clear limits",
                          icon: Trash,
                          danger: true,
                          disabled: stale,
                          run: () => clear(d),
                        },
                      ]
                    : []),
                ]
                return (
                  <QueueCard
                    key={d.name}
                    device={d}
                    link={links?.find((l) => l.name === d.name)}
                    verb={editable ? `Edit the limits on ${d.name}` : `Open the queue on ${d.name}`}
                    verbs={verbs}
                    onOpen={() => setOpen(d.name)}
                  />
                )
              })}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      <CongestionPanel bbr={view?.bbr} onChanged={shaping.refresh} stale={stale} />

      {device && view && (
        <ShapeSheet
          key={device.name}
          device={device}
          qdiscs={view.qdiscs}
          onClose={() => setOpen(undefined)}
          onSaved={shaping.refresh}
          stale={stale}
        />
      )}
      {dialog}
    </div>
  )
}

/** A limit in its column: the direction's arrow in its hue, then the words. */
function Limit({
  arrow,
  hue,
  kbit,
  shown,
}: {
  arrow: string
  hue: string
  kbit: number
  shown: boolean
}) {
  return (
    <span className="numeric inline-flex min-w-0 items-baseline gap-1.5 text-hint whitespace-nowrap">
      <span aria-hidden style={{ color: hue }}>
        {arrow}
      </span>
      {!shown ? (
        <span className="text-muted-foreground">—</span>
      ) : kbit > 0 ? (
        <span className="text-foreground">{limitWords(kbit)}</span>
      ) : (
        <span className="text-muted-foreground">{limitWords(kbit)}</span>
      )}
    </span>
  )
}

function QueueCard({
  device: d,
  link,
  verb,
  verbs,
  onOpen,
}: {
  device: ShapeDevice
  link: NetworkLink | undefined
  verb: string
  verbs: Verb[]
  onOpen: () => void
}) {
  const state = queueState(d)
  const upload = (
    <span aria-label={`Upload ${d.shapeable ? limitWords(d.egressKbit) : "not shapeable"}`}>
      <Limit arrow="↑" hue={TX} kbit={d.egressKbit} shown={d.shapeable} />
    </span>
  )
  const download = (
    <span aria-label={`Download ${d.shapeable ? limitWords(d.ingressKbit) : "not shapeable"}`}>
      <Limit arrow="↓" hue={RX} kbit={d.ingressKbit} shown={d.shapeable} />
    </span>
  )
  return (
    <ChoiceRow
      leading={
        <ProductLogo
          id={linkProduct({ kind: d.kind, owner: link?.owner ?? "kernel" })}
          size="sm"
          fallback={linkGlyph(d.kind)}
        />
      }
      title={<span className="font-mono">{d.name}</span>}
      verb={verb}
      description={<span className="font-mono">{queueLine(d)}</span>}
      onSelect={onOpen}
      trailing={
        <span className="hidden items-center gap-4 sm:flex">
          <span className="w-28">{upload}</span>
          <span className="w-28">{download}</span>
          <Status tone={state.tone} label={state.label} className="w-44" />
        </span>
      }
      actions={
        verbs.length > 0 ? (
          <VerbMenu verbs={verbs} label={`Queue on ${d.name}`} />
        ) : (
          // The menu's place is kept, so every card's columns start on one edge.
          <span aria-hidden className="size-7" />
        )
      }
    >
      {/* A phone has no room for the columns beside the name: they are the
          card's second line there, on every card alike. */}
      <span className="flex min-w-0 items-center gap-4 sm:hidden">
        {upload}
        {download}
        <Status tone={state.tone} label={state.label} className="ml-auto min-w-0 truncate" />
      </span>
    </ChoiceRow>
  )
}

/** The cards before the queues are read: as many as were drawn last time, each a card's height. */
function QueueGhosts({ rows }: { rows: number }) {
  return (
    <ul aria-hidden className="space-y-2">
      {Array.from({ length: rows }, (_, i) => (
        <li
          key={i}
          className="flex h-[60px] items-center gap-3 rounded-xl border bg-choice-surface px-3 max-sm:h-[94px] max-sm:items-start max-sm:pt-3"
        >
          <Skeleton className="size-8 rounded-lg" />
          <span className="flex flex-col gap-2">
            <Skeleton className="h-3.5 w-24" />
            <Skeleton className="h-3 w-36" />
          </span>
        </li>
      ))}
    </ul>
  )
}
