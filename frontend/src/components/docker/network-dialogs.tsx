"use client"

import { useEffect, useState } from "react"
import { notify } from "@/lib/toast"
import { del, errorMessage, get, post } from "@/lib/api"
import type { Container, DockerNetwork } from "@/lib/types"
import {
  isChangePreview,
  isPrunePreview,
  prunePlan,
  type NetworkChangePreview,
  type NetworkPruneResult,
} from "@/lib/docker-networks"
import { usePoll } from "@/hooks/use-poll"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { ErrorState, LoadingRows } from "@/components/state"
import { Hint } from "@/components/docker/explain"
import { ConflictList, NetworkChangeDialog } from "@/components/docker/network-conflicts"
import { ownerLabel } from "@/components/docker/networks"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/**
 * Attaching a container, read before it is done. What attaching would run
 * into — a name another member already answers to, an alias Docker's resolver
 * cannot serve, the dashboard's own network, a full pool — is the server's
 * preview of the draft as it stands, a moment after typing stops; the button
 * is off while that reading is missing, stale or refuses. A failed read of
 * the candidates or of the preview keeps the container and the alias, and
 * says so, rather than claiming nothing is left to attach.
 */
export function AttachDialog({
  open,
  networkId,
  networkName,
  onOpenChange,
  onAttached,
  attached,
}: {
  open: boolean
  networkId: string | null
  networkName?: string
  onOpenChange: (open: boolean) => void
  onAttached: () => void
  attached: Set<string>
}) {
  const candidates = usePoll<Container[]>(
    (signal) => get("/docker/containers/", undefined, signal),
    30_000,
    [networkId, open],
    { enabled: open && Boolean(networkId) },
  )
  const [picked, setPicked] = useState("")
  const [alias, setAlias] = useState("")
  const [busy, setBusy] = useState(false)
  const [settledAlias, setSettledAlias] = useState("")
  const [rereadSince, setRereadSince] = useState(0)

  // One draft per network: a container picked for another one is not this one's.
  const [draftFor, setDraftFor] = useState(networkId)
  if (networkId && networkId !== draftFor) {
    setDraftFor(networkId)
    setPicked("")
    setAlias("")
    setSettledAlias("")
  }

  useEffect(() => {
    const timer = setTimeout(() => setSettledAlias(alias.trim()), 300)
    return () => clearTimeout(timer)
  }, [alias])
  const preview = usePoll<NetworkChangePreview>(
    (signal) =>
      get<NetworkChangePreview>(
        `/docker/networks/${encodeURIComponent(networkId ?? "")}/connect`,
        settledAlias ? { container: picked, alias: settledAlias } : { container: picked },
        signal,
      ),
    0,
    [networkId, picked, settledAlias],
    { enabled: open && Boolean(networkId) && Boolean(picked) },
  )
  const previewReady =
    !preview.loading &&
    !preview.error &&
    isChangePreview(preview.data) &&
    preview.data.container === picked
  const previewCurrent =
    previewReady && settledAlias === alias.trim() && (preview.lastSuccess ?? 0) > rereadSince

  const attach = async () => {
    setBusy(true)
    try {
      await post(`/docker/networks/${networkId}/connect`, {
        container: picked,
        aliases: alias.trim() ? [alias.trim()] : undefined,
      })
      notify.success(`Attached to ${networkName}`)
      onAttached()
      onOpenChange(false)
      setPicked("")
      setAlias("")
    } catch (err) {
      notify.error("Could not attach it", err)
      setRereadSince(Date.now())
      preview.refresh()
    } finally {
      setBusy(false)
    }
  }

  const available = candidates.data?.filter((c) => !attached.has(c.id)) ?? []

  return (
    <Modal
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="sm"
      title="Attach a container"
      description={
        <>
          It joins <b>{networkName}</b> immediately, without restarting, and can reach everything
          else on it by name.
        </>
      }
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            onClick={attach}
            disabled={
              busy ||
              !picked ||
              Boolean(candidates.error) ||
              !available.some((c) => c.id === picked) ||
              !previewCurrent ||
              Boolean(preview.data?.blocked)
            }
            pending={busy}
          >
            Attach
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {candidates.data && (
          <NetworkReadWarning
            error={candidates.error}
            refresh={candidates.refresh}
            lastSuccess={candidates.lastSuccess}
            reading="container candidates"
          />
        )}
        {!candidates.data && candidates.error && (
          <div className="space-y-2">
            <ErrorState error={candidates.error} />
            <Button size="xs" variant="outline" onClick={candidates.refresh}>
              Try again
            </Button>
          </div>
        )}
        {candidates.loading && <LoadingRows rows={2} />}
        <div className="space-y-1.5">
          <Label className="text-xs" htmlFor="network-attach-container">
            Container
          </Label>
          <Select
            value={picked}
            onValueChange={setPicked}
            disabled={candidates.loading || Boolean(candidates.error)}
          >
            <SelectTrigger id="network-attach-container" className="w-full">
              <SelectValue placeholder="Pick one" />
            </SelectTrigger>
            <SelectContent>
              {available.map((c) => (
                <SelectItem key={c.id} value={c.id}>
                  {c.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {candidates.data && !candidates.error && available.length === 0 && (
            <Hint>Every container is already on this network.</Hint>
          )}
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs" htmlFor="network-attach-alias">
            Extra name (optional)
          </Label>
          <Input
            id="network-attach-alias"
            value={alias}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder="db"
            onChange={(e) => setAlias(e.target.value)}
          />
          <Hint>
            An additional hostname the others can use. Useful when an application&apos;s config
            expects a name that is not the container&apos;s.
          </Hint>
        </div>
        {picked && (
          <section aria-label="Before attaching" className="space-y-1.5">
            <p className="eyebrow">Before attaching</p>
            {preview.loading && !previewReady && <LoadingRows rows={1} />}
            {preview.error && (
              <div role="alert" className="space-y-1.5 text-hint">
                <p className="text-destructive">
                  What attaching would run into could not be read: {errorMessage(preview.error)}
                </p>
                <Button size="xs" variant="outline" onClick={preview.refresh}>
                  Check again
                </Button>
              </div>
            )}
            {previewReady && preview.data && (
              <ConflictList
                conflicts={preview.data.conflicts}
                emptyLabel="No name, address or ownership conflict on this network."
              />
            )}
          </section>
        )}
      </div>
    </Modal>
  )
}

/**
 * Removing one network, after what removing it disturbs has been read: a
 * stopped container that still names it and would fail to start, a shared
 * address reservation that records it, the dashboard's own stack, a
 * deployment that still exists. A refusal is the server's too.
 */
export function RemoveNetworkDialog({
  network,
  onOpenChange,
  onRemoved,
}: {
  network: DockerNetwork | null
  onOpenChange: (open: boolean) => void
  onRemoved: (network: DockerNetwork) => void
}) {
  // Held through the close, so the dialog does not empty while it fades.
  const [last, setLast] = useState(network)
  if (network && network !== last) setLast(network)
  const shown = network ?? last
  return (
    <NetworkChangeDialog
      open={network !== null}
      onOpenChange={onOpenChange}
      target={shown?.id ?? ""}
      title={`Remove ${shown?.name ?? "network"}`}
      description="What removing this network disturbs, read before it is removed."
      intro={
        shown && (
          <p className="text-body">
            Removes <b>{shown.name}</b> and returns{" "}
            {shown.subnets[0] ? (
              <span className="font-mono">{shown.subnets[0]}</span>
            ) : (
              "its subnet"
            )}{" "}
            to the pool. A compose project recreates its own network on the next deploy.
          </p>
        )
      }
      confirmLabel="Remove"
      emptyLabel="Nothing names this network: no container, stopped or running, and no deployment."
      load={(signal) =>
        get<NetworkChangePreview>(
          `/docker/networks/${encodeURIComponent(shown?.id ?? "")}/removal`,
          undefined,
          signal,
        )
      }
      onConfirm={async () => {
        if (!shown) return
        await del(`/docker/networks/${encodeURIComponent(shown.id)}`)
        notify.success(`${shown.name} removed`)
        onRemoved(shown)
      }}
    />
  )
}

/**
 * The reviewed prune: the networks the Engine's own prune would take, split
 * into the ones nothing names — removed — and the ones kept, each with why.
 * Only the removable ones' IDs are sent, and the backend rechecks each, so a
 * network that gained a dependent since is kept rather than removed.
 */
export function PruneDialog({
  open,
  onOpenChange,
  onPruned,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onPruned: () => void
}) {
  const reading = usePoll<unknown>(
    (signal) => get("/docker/networks/prune", undefined, signal),
    0,
    [open],
    { enabled: open },
  )
  const [busy, setBusy] = useState(false)
  const [rereadSince, setRereadSince] = useState(0)
  const stale = (reading.lastSuccess ?? 0) <= rereadSince
  const preview = isPrunePreview(reading.data) ? reading.data : undefined
  const error =
    reading.error ??
    (reading.data !== undefined && !preview
      ? new Error("The prune preview came back in a shape this page cannot read.")
      : undefined)
  const plan = preview ? prunePlan(preview.candidates) : undefined
  const prune = async () => {
    if (!plan || plan.removed.length === 0) return
    setBusy(true)
    try {
      const result = await post<NetworkPruneResult>("/docker/networks/prune", {
        ids: plan.removed.map((c) => c.id),
      })
      const removed = result.items.length
      notify.success(
        removed
          ? `Removed ${removed} network${removed === 1 ? "" : "s"}${result.skipped.length ? `, kept ${result.skipped.length} that changed since` : ""}`
          : "Nothing was removed",
      )
      onPruned()
      onOpenChange(false)
    } catch (err) {
      notify.error("Could not prune networks", err)
      setRereadSince(Date.now())
      reading.refresh()
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      size="sm"
      title="Remove unused networks"
      description="The networks a prune would remove, and the ones it keeps because something still names them."
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={prune}
            disabled={busy || stale || Boolean(error) || !plan || plan.removed.length === 0}
            pending={busy}
          >
            {plan && plan.removed.length > 0 ? `Remove ${plan.removed.length}` : "Remove"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {reading.loading && <LoadingRows rows={2} />}
        {error && (
          <div role="alert" className="space-y-2 text-hint">
            <p className="text-destructive">{errorMessage(error)}</p>
            <p className="text-muted-foreground">Nothing was removed.</p>
            <Button size="xs" variant="outline" onClick={reading.refresh}>
              Try again
            </Button>
          </div>
        )}
        {plan && (
          <>
            <section className="space-y-1.5">
              <p className="eyebrow">Removed</p>
              {plan.removed.length === 0 ? (
                <Hint>No network is unused by every container and every deployment.</Hint>
              ) : (
                <ul aria-label="Networks removed" className="space-y-1">
                  {plan.removed.map((c) => (
                    <li key={c.id} className="text-hint">
                      <span className="font-mono">{c.name}</span>
                      <span className="text-muted-foreground"> · {ownerLabel(c.owner)}</span>
                      {c.conflicts.length > 0 && (
                        <span className="block text-muted-foreground">
                          {c.conflicts.map((conflict) => conflict.message).join(" ")}
                        </span>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </section>
            {plan.kept.length > 0 && (
              <section className="space-y-1.5">
                <p className="eyebrow">Kept</p>
                <ul aria-label="Networks kept" className="space-y-1.5">
                  {plan.kept.map((c) => (
                    <li key={c.id} className="text-hint leading-relaxed">
                      <span className="font-mono">{c.name}</span>
                      <span className="block text-muted-foreground">{c.reason}</span>
                    </li>
                  ))}
                </ul>
                <Hint>
                  Docker&apos;s own prune would remove these too. Each can still be removed on its
                  own after its preview.
                </Hint>
              </section>
            )}
          </>
        )}
      </div>
    </Modal>
  )
}
