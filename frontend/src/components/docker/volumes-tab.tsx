"use client"

import { useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { FolderOpen, Servers, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import { bytes, truncateMiddle } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { VolumeDetail } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useAuth } from "@/hooks/use-auth"
import { EmptyState, ErrorState, LoadingPanel, LoadingRows } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, ROW_BLEED, RowList } from "@/components/row-list"
import { SidePanel } from "@/components/side-panel"
import { Detail, DetailList, RowLink, SearchInput } from "@/components/page"
import { ChipCount, FilterChip } from "@/components/tabs"
import type { ConfirmFn } from "@/components/docker/shared"
import { Hint, Term } from "@/components/docker/explain"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
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
export function VolumesTab({
  confirm,
  creating: externalCreating,
  onCreatingChange,
}: {
  confirm: ConfirmFn
  creating?: boolean
  onCreatingChange?: (open: boolean) => void
}) {
  const { can } = useAuth()
  // In the URL so a deployment can link straight at the volume it depends on.
  const [selected, setSelected] = useQuerySelection("volume")
  const [internalCreating, setInternalCreating] = useState(false)
  const creating = externalCreating ?? internalCreating
  const setCreating = onCreatingChange ?? setInternalCreating
  const [filter, setFilter] = useSessionState("docker.volumes.query", "")
  const [state, setState] = useSessionState<"all" | "used" | "unused">(
    "docker.volumes.state",
    "all",
  )

  const { data, error, loading, refresh } = usePoll(
    (signal) => get<VolumeDetail[]>("/docker/volumes/", undefined, signal),
    30000,
  )
  const volumes = useMemo(() => data ?? [], [data])
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return volumes.filter((v) => {
      if (state === "used" && !v.inUse) return false
      if (state === "unused" && v.inUse) return false
      if (!needle) return true
      return v.name.toLowerCase().includes(needle)
    })
  }, [volumes, filter, state])
  const counts = useMemo(
    () => ({
      all: volumes.length,
      used: volumes.filter((v) => v.inUse).length,
      unused: volumes.filter((v) => !v.inUse).length,
    }),
    [volumes],
  )
  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />

  return (
    <div className="space-y-4">
      {/* Plain: the list is the page. */}
      <Panel className="animate-rise">
        <PanelHeader
          title="Volumes"
          actions={
            <>
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
        {volumes.length > 0 && (
          <PanelToolbar>
            <SearchInput
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Search volumes"
            />
            <div className="flex min-w-0 flex-wrap gap-1">
              {(["all", "used", "unused"] as const).map((key) =>
                key === "all" || counts[key] > 0 ? (
                  <FilterChip key={key} selected={state === key} onClick={() => setState(key)}>
                    {key === "all" ? "All" : key === "used" ? "Used" : "Unused"}
                    <ChipCount>{counts[key]}</ChipCount>
                  </FilterChip>
                ) : null,
              )}
            </div>
          </PanelToolbar>
        )}
        <PanelBody flush>
          {volumes.length === 0 ? (
            <EmptyState
              icon={Servers}
              title="No volumes"
              description={
                <>
                  A <Term name="volume">volume</Term> is storage Docker manages, kept outside a
                  container so it survives being recreated.
                </>
              }
            />
          ) : visible.length === 0 ? (
            <EmptyState
              icon={Servers}
              title="Nothing matches those filters"
              description="Clear the search, or look under a different state."
              action={
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setFilter("")
                    setState("all")
                  }}
                >
                  Clear filters
                </Button>
              }
            />
          ) : (
            <>
              <ul className="divide-y divide-hairline lg:hidden">
                {visible.map((volume) => (
                  <VolumeListItem
                    key={volume.name}
                    volume={volume}
                    confirm={confirm}
                    onOpen={() => setSelected(volume.name)}
                    onChanged={refresh}
                  />
                ))}
              </ul>

              <div className="group-data-[plain]/panel:-mx-4 hidden min-w-0 lg:block">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-full">Name</TableHead>
                      <TableHead className="text-right">Size</TableHead>
                      <TableHead>Used by</TableHead>
                      <TableHead className="w-px text-right">Actions</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {visible.map((volume) => (
                      <TableRow
                        key={volume.name}
                        className="group"
                        onActivate={() => setSelected(volume.name)}
                      >
                        <TableCell>
                          <RowLink mono onClick={() => setSelected(volume.name)}>
                            {truncateMiddle(volume.name, 40)}
                          </RowLink>
                        </TableCell>
                        <TableCell className="numeric text-right font-mono">
                          <VolumeSize volume={volume} />
                        </TableCell>
                        <TableCell>
                          <UsedByCell volume={volume} />
                        </TableCell>
                        <TableCell>
                          <span className="flex w-10 justify-end">
                            {can("destructive") &&
                              (volume.usedBy.length > 0 ? (
                                <IconAction
                                  label={`In use by ${volume.usedBy.length} container${volume.usedBy.length === 1 ? "" : "s"} — cannot be removed while mounted`}
                                  className="text-muted-foreground opacity-40"
                                  onClick={() => setSelected(volume.name)}
                                >
                                  <Trash />
                                </IconAction>
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
                                            Nothing mounts it right now. A volume outlives the
                                            container that created it, so this is often the data
                                            from something that was removed and rebuilt — check what
                                            is in it first if you are not sure.
                                          </p>
                                        </>
                                      ),
                                      action: async (c) => {
                                        await del(
                                          `/docker/volumes/${encodeURIComponent(volume.name)}`,
                                          {
                                            confirm: c,
                                          },
                                        )
                                        refresh()
                                      },
                                    })
                                  }
                                >
                                  <Trash />
                                </IconAction>
                              ))}
                          </span>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </>
          )}
        </PanelBody>
      </Panel>

      <VolumeDetailPanel name={selected} onOpenChange={(o) => !o && setSelected(null)} />
      <NewVolumeDialog open={creating} onOpenChange={setCreating} onCreated={refresh} />
    </div>
  )
}

/**
 * One volume on a screen too narrow for the table.
 *
 * A row and not a card, mirroring `ContainerCard`: no frame of its own, a
 * hairline between it and the next, a wash under the pointer, and the name as
 * a real `<button>` so the row is announced as its name rather than as
 * everything inside it. The removal control is drawn at rest rather than
 * revealed, because a row whose controls appear only on hover is a row whose
 * controls a phone cannot reach.
 */
function VolumeListItem({
  volume,
  confirm,
  onOpen,
  onChanged,
}: {
  volume: VolumeDetail
  confirm: ConfirmFn
  onOpen: () => void
  onChanged: () => void
}) {
  const { can } = useAuth()

  return (
    <li
      className={cn(
        "group min-w-0 space-y-1.5 px-4 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
      )}
    >
      <div className="flex min-w-0 items-start justify-between gap-2">
        <button
          type="button"
          onClick={onOpen}
          className="block max-w-full truncate rounded-sm text-left font-mono text-body focus-ring hover:text-primary"
        >
          {truncateMiddle(volume.name, 40)}
        </button>
        {can("destructive") &&
          (volume.usedBy.length > 0 ? (
            <IconAction
              label={`In use by ${volume.usedBy.length} container${volume.usedBy.length === 1 ? "" : "s"} — cannot be removed while mounted`}
              className="text-muted-foreground opacity-40"
              onClick={onOpen}
            >
              <Trash />
            </IconAction>
          ) : (
            <IconAction
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
                        Everything stored in <b>{volume.name}</b> is destroyed permanently.
                      </p>
                      <p>
                        Nothing mounts it right now. A volume outlives the container that created
                        it, so this is often the data from something that was removed and rebuilt —
                        check what is in it first if you are not sure.
                      </p>
                    </>
                  ),
                  action: async (c) => {
                    await del(`/docker/volumes/${encodeURIComponent(volume.name)}`, { confirm: c })
                    onChanged()
                  },
                })
              }
            >
              <Trash />
            </IconAction>
          ))}
      </div>

      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
        <UsedByCell volume={volume} />
        <span className="numeric font-mono text-hint text-muted-foreground">
          <VolumeSize volume={volume} />
        </span>
      </div>
    </li>
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
                  Browse files
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
              <RowList>
                {data.usedBy.map((u) => (
                  <Row
                    key={`${u.id}-${u.destination}`}
                    className="px-0 py-2"
                    title={u.name}
                    subtitle={`at ${u.destination}${u.stack ? ` · ${u.stack}` : ""}`}
                    mono
                    trailing={
                      <>
                        {u.readOnly && <Tag>read-only</Tag>}
                        <Status state={u.state} />
                      </>
                    }
                  />
                ))}
              </RowList>
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
      title="Create volume"
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
