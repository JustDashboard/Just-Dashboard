"use client"

import { useState } from "react"
import Link from "next/link"
import { FolderOpen, Plus, Servers, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import { bytes, truncateMiddle } from "@/lib/format"
import type { VolumeDetail } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useAuth } from "@/hooks/use-auth"
import { EmptyState, ErrorState, LoadingPanel, LoadingRows } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { Detail, DetailList, RowLink } from "@/components/page"
import type { ConfirmFn } from "@/components/docker/shared"
import { Hint, Term } from "@/components/docker/explain"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * Volumes, with the one fact that decides every action on them: what is using
 * this, and will deleting it destroy something.
 *
 * Docker's own reference count answers for *running* containers only, so a
 * volume belonging to a stopped stack reads as unused — and that is precisely
 * the volume somebody prunes by accident, along with the only copy of whatever
 * was in it. The list joins against every container, running or not, on the
 * server rather than in the browser.
 */
export function VolumesTab({ confirm }: { confirm: ConfirmFn }) {
  const { can } = useAuth()
  // In the URL so a deployment can link straight at the volume it depends on.
  const [selected, setSelected] = useQuerySelection("volume")
  const [creating, setCreating] = useState(false)

  const { data, error, loading, refresh } = usePoll(
    (signal) => get<VolumeDetail[]>("/docker/volumes/", undefined, signal),
    30000,
  )
  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />

  const unused = (data ?? []).filter((v) => !v.inUse)

  return (
    <div className="space-y-4">
      <Panel>
        <PanelHeader
          title="Volumes"
          actions={
            <>
              {unused.length > 0 && <Tag tone="warning">{unused.length} unused</Tag>}
              {can("service.control") && (
                <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
                  <Plus className="size-4" />
                  New volume
                </Button>
              )}
              {can("destructive") && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() =>
                    confirm({
                      title: "Prune volumes",
                      phrase: "prune volumes",
                      confirmLabel: "Prune",
                      description: (
                        <>
                          <p className="text-destructive">
                            Deletes every volume no <em>running</em> container is using, including
                            named ones. The data in them is gone permanently.
                          </p>
                          <p>
                            That is Docker&apos;s definition, not this dashboard&apos;s: a volume
                            belonging to a stack that is merely stopped counts as unused and will be
                            destroyed. Check the list first — the ones at risk are the rows marked
                            &ldquo;unused&rdquo;.
                          </p>
                        </>
                      ),
                      action: async (c) => {
                        const rep = await post<{ spaceReclaimed: number }>(
                          "/docker/volumes/prune",
                          undefined,
                          { confirm: c },
                        )
                        notify.success(`Reclaimed ${bytes(rep.spaceReclaimed)}`)
                        refresh()
                      },
                    })
                  }
                >
                  <Trash className="size-4" />
                  Prune unused
                </Button>
              )}
            </>
          }
        />
        <PanelBody flush>
          <Table containerClassName="max-h-[calc(100svh-24rem)]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-full">Name</TableHead>
                <TableHead className="text-right">Size</TableHead>
                <TableHead>Used by</TableHead>
                <TableHead className="w-px" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data?.map((volume) => (
                <TableRow
                  key={volume.name}
                  className="group"
                  onActivate={() => setSelected(volume.name)}
                >
                  <TableCell>
                    <div className="max-w-[24rem] min-w-0">
                      <RowLink mono onClick={() => setSelected(volume.name)}>
                        {truncateMiddle(volume.name, 40)}
                      </RowLink>
                      <p className="truncate font-mono text-hint text-muted-foreground">
                        {volume.mountpoint}
                      </p>
                    </div>
                  </TableCell>
                  <TableCell className="numeric text-right font-mono">
                    <VolumeSize volume={volume} />
                  </TableCell>
                  <TableCell>
                    <UsedByCell volume={volume} />
                  </TableCell>
                  <TableCell>
                    {/*
                      Docker refuses to remove a volume something mounts, so
                      the button is disabled rather than offered and answered
                      with a 409. What replaces it is the reason: "in use by 2
                      containers" is the sentence the operator needed, and the
                      old UI made them press a button to find it out.
                    */}
                    {can("destructive") &&
                      (volume.usedBy.length > 0 ? (
                        <span className="block text-hint whitespace-nowrap text-muted-foreground">
                          in use by {volume.usedBy.length}
                        </span>
                      ) : (
                        <IconAction
                          reveal
                          label="Remove"
                          className="text-destructive"
                          onClick={() =>
                            confirm({
                              title: "Delete volume",
                              phrase: volume.name,
                              confirmLabel: "Delete",
                              description: (
                                <>
                                  <p className="text-destructive">
                                    Everything stored in <b>{volume.name}</b> is destroyed
                                    permanently.
                                  </p>
                                  <p>
                                    Nothing mounts it right now. A volume outlives the container
                                    that created it, so this is often the data from something that
                                    was removed and rebuilt — check what is in it first if you are
                                    not sure.
                                  </p>
                                </>
                              ),
                              action: async (c) => {
                                await del(`/docker/volumes/${encodeURIComponent(volume.name)}`, {
                                  confirm: c,
                                })
                                refresh()
                              },
                            })
                          }
                        >
                          <Trash />
                        </IconAction>
                      ))}
                  </TableCell>
                </TableRow>
              ))}
              {!data?.length && (
                <TableRow>
                  <TableCell colSpan={4} className="p-0">
                    <EmptyState
                      icon={Servers}
                      title="No volumes"
                      description={
                        <>
                          A <Term name="volume">volume</Term> is storage Docker manages, kept
                          outside a container so it survives being recreated.
                        </>
                      }
                    />
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </PanelBody>
      </Panel>

      <VolumeDetailPanel name={selected} onOpenChange={(o) => !o && setSelected(null)} />
      <NewVolumeDialog open={creating} onOpenChange={setCreating} onCreated={refresh} />
    </div>
  )
}

/**
 * A volume's size, with "not measured" distinguished from "empty".
 *
 * An em dash was doing duty for three different answers — zero bytes, a size
 * Docker has not walked yet, and a driver that cannot report one — and the
 * operator deciding whether a volume is safe to delete could not tell which
 * they were looking at. Docker only fills the figure in for local volumes it
 * has walked, so the absence is common and worth naming.
 */
/**
 * Whether this volume is likely to hold a database's own files.
 *
 * A guess from the mount path and the containers using it, and treated as one:
 * it decides whether a warning is shown, never whether an action is allowed.
 * Getting it wrong in one direction costs a sentence somebody did not need; in
 * the other it costs a corrupted database, so it leans towards warning.
 */
function looksLikeDatabase(volume: VolumeDetail): boolean {
  if (volume.usedBy.length === 0) return false
  const hints = /(postgres|mysql|mariadb|mongo|redis|elastic|clickhouse|cassandra|influx|couch)/i
  return (
    hints.test(volume.name) ||
    volume.usedBy.some((u) => hints.test(u.name) || hints.test(u.destination))
  )
}

function VolumeSize({ volume }: { volume: VolumeDetail }) {
  if (volume.size > 0) return <>{bytes(volume.size)}</>
  if (volume.driver !== "local") {
    return <span className="text-hint text-muted-foreground">not measurable</span>
  }
  return <span className="text-hint text-muted-foreground">not measured</span>
}

function UsedByCell({ volume }: { volume: VolumeDetail }) {
  if (volume.usedBy.length === 0) {
    return <Status tone="stopped" label="unused" />
  }
  const running = volume.usedBy.filter((u) => u.state === "running").length
  return (
    <span className="flex flex-wrap items-center gap-1">
      <Status
        tone={running > 0 ? "running" : "warning"}
        label={`${volume.usedBy.length} container${volume.usedBy.length === 1 ? "" : "s"}`}
      />
      {running === 0 && (
        // The row Docker's own prune would delete while calling it unused.
        <span className="text-hint text-muted-foreground">stopped — prune would delete this</span>
      )}
    </span>
  )
}

function VolumeDetailPanel({
  name,
  onOpenChange,
}: {
  name: string | null
  onOpenChange: (open: boolean) => void
}) {
  const { data, error, loading } = usePoll<VolumeDetail>(
    (signal) =>
      get<VolumeDetail>(`/docker/volumes/${encodeURIComponent(name ?? "")}`, undefined, signal),
    0,
    [name],
    { enabled: name !== null },
  )

  return (
    <SidePanel
      open={name !== null}
      onOpenChange={onOpenChange}
      title={name ?? "Volume"}
      description={data?.mountpoint}
    >
      {error && <ErrorState error={error} />}
      {loading && !data && <LoadingRows />}
      {data && (
        <div className="space-y-5">
          <DetailList>
            <Detail label="Size">
              <VolumeSize volume={data} />
            </Detail>
            <Detail label="Driver">{data.driver}</Detail>
            <Detail label="Created">{data.createdAt || "—"}</Detail>
            <Detail label="On disk at">
              <span className="font-mono break-all">{data.mountpoint}</span>
            </Detail>
          </DetailList>

          {/*
            A volume is a directory on this server, and this dashboard has a
            file manager. Being able to look inside one — to check a backup
            landed, to read a config a container wrote — is the difference
            between a volume being an opaque handle and being storage.

            The warning is not decoration. A database's files are consistent
            only from the database's point of view; editing one underneath a
            running Postgres is how a volume stops being restorable, and the
            file manager gives no hint that this directory is different from
            any other.
          */}
          {data.mountpoint && (
            <div className="space-y-1.5">
              <Button size="sm" variant="outline" asChild>
                <Link href={`/files?path=${encodeURIComponent(data.mountpoint)}`}>
                  <FolderOpen className="size-3.5" />
                  Browse its contents
                </Link>
              </Button>
              {looksLikeDatabase(data) && (
                <Hint className="text-warning">
                  This looks like a database volume and something is using it. Reading is safe;
                  changing or deleting a file underneath a running database corrupts it in ways that
                  only show up later. Stop the container first if you need to write here.
                </Hint>
              )}
            </div>
          )}

          <section className="space-y-1.5">
            <p className="eyebrow">Used by</p>
            {data.usedBy.length === 0 ? (
              <Hint>
                Nothing mounts this volume. It is safe to delete only if you know what was in it — a
                volume outlives the container that created it, so this is often the data from
                something that was removed and rebuilt.
              </Hint>
            ) : (
              <div className="space-y-1">
                {data.usedBy.map((u) => (
                  <div
                    key={`${u.id}-${u.destination}`}
                    className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-hairline px-2.5 py-1.5 text-xs"
                  >
                    <span className="min-w-0">
                      <span className="truncate font-medium">{u.name}</span>
                      <span className="ml-2 font-mono text-hint text-muted-foreground">
                        at {u.destination}
                      </span>
                      {u.stack && (
                        <span className="ml-2 text-hint text-muted-foreground">· {u.stack}</span>
                      )}
                    </span>
                    <span className="flex shrink-0 gap-1">
                      {u.readOnly && <Tag>read-only</Tag>}
                      <Status state={u.state} />
                    </span>
                  </div>
                ))}
              </div>
            )}
          </section>
        </div>
      )}
    </SidePanel>
  )
}

function NewVolumeDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: () => void
}) {
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)

  const create = async () => {
    setBusy(true)
    try {
      await post("/docker/volumes/", { name })
      notify.success(`${name} created`)
      onCreated()
      onOpenChange(false)
      setName("")
    } catch (err) {
      notify.error("Could not create the volume", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="sm"
      title="New volume"
      description="Storage Docker manages, ready to mount into a container. Empty until something writes to
            it."
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={create} disabled={busy || !name.trim()} pending={busy}>
            Create
          </Button>
        </>
      }
    >
      <div className="space-y-1.5">
        <Label htmlFor="volume-name" className="text-xs">
          Name
        </Label>
        <Input
          id="volume-name"
          value={name}
          spellCheck={false}
          className="font-mono"
          placeholder="my-app-data"
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && name.trim() && create()}
        />
        <Hint>
          Name it after what will be in it. Volumes outlive the containers that use them, and in six
          months the name is all you will have to go on.
        </Hint>
      </div>
    </Modal>
  )
}
