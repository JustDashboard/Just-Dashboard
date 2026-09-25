"use client"

import { useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { Servers, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import { bytes, truncateMiddle } from "@/lib/format"
import type { Container, VolumeDetail } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useAuth } from "@/hooks/use-auth"
import { EmptyState, ErrorState, LoadingPanel, LoadingRows } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { Detail, DetailList, SearchInput } from "@/components/page"
import { ChipCount, FilterChip } from "@/components/tabs"
import {
  DatabaseStorageWarning,
  looksLikeDatabase,
  type ConfirmFn,
} from "@/components/docker/shared"
import { Hint, Term } from "@/components/docker/explain"
import { FileBrowser } from "@/components/files/inline-browser"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

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
  actions,
}: {
  confirm: ConfirmFn
  creating?: boolean
  onCreatingChange?: (open: boolean) => void
  actions?: React.ReactNode
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
  // Which image each container runs, so a volume can be drawn as the product
  // that keeps its data in it: `pgdata` under a Postgres logo is found before
  // its name is read. Joined here rather than on the server because the
  // volume listing already names its users and nothing else needs the image.
  const containers = usePoll(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    60_000,
  )
  const imageOf = useMemo(
    () => new Map((containers.data ?? []).map((c) => [c.id, c.image])),
    [containers.data],
  )
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
      {/* Plain: the list is the page, and each volume is a card with its own
          edge — a frame around framed cards is the nesting §12 refuses. */}
      <Panel plain className="animate-rise">
        <PanelHeader
          title="Volumes"
          actions={
            <>
              {actions}
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
            // One card per volume at every width: each opens the volume and
            // what is in it, so each is a choice (§16).
            <ChoiceList aria-label="Volumes" className="animate-rise">
              {visible.map((volume) => (
                <VolumeCard
                  key={volume.name}
                  volume={volume}
                  product={volumeProduct(volume, imageOf)}
                  confirm={confirm}
                  onOpen={() => setSelected(volume.name)}
                  onChanged={refresh}
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      <VolumeDetailPanel name={selected} onOpenChange={(o) => !o && setSelected(null)} />
      <NewVolumeDialog open={creating} onOpenChange={setCreating} onCreated={refresh} />
    </div>
  )
}

/**
 * The product a volume holds the data of: the image of the first container
 * that mounts it, when that image is one with a logo. A volume nothing mounts,
 * or one mounted only by images with no product, is storage and nothing more.
 */
function volumeProduct(volume: VolumeDetail, imageOf: Map<string, string>) {
  for (const user of volume.usedBy) {
    const image = imageOf.get(user.id)
    const product = image ? imageProduct(image) : undefined
    if (product && product !== "docker") return product
  }
  return undefined
}

/**
 * One volume, as a card that opens it.
 *
 * Who uses it is the second line at every width, not a column that a phone
 * drops: "is anything still using this" is the question a volume is opened
 * with, and the answer — including the stopped stack Docker's own prune would
 * delete the data of — belongs under the name. The removal control is drawn
 * at rest rather than revealed, because a control a phone cannot hover is a
 * control a phone cannot reach.
 */
function VolumeCard({
  volume,
  product,
  confirm,
  onOpen,
  onChanged,
}: {
  volume: VolumeDetail
  product?: string
  confirm: ConfirmFn
  onOpen: () => void
  onChanged: () => void
}) {
  const { can } = useAuth()

  return (
    <ChoiceRow
      verb={volume.name}
      onSelect={onOpen}
      leading={<ProductLogo id={product} fallback={Servers} size="sm" />}
      title={<span className="font-mono">{truncateMiddle(volume.name, 48)}</span>}
      description={
        <span className="flex min-w-0 items-center gap-2">
          <UsedByCell volume={volume} />
          {volume.driver !== "local" && <span>· {volume.driver}</span>}
        </span>
      }
      trailing={
        <span className="numeric w-24 text-right font-mono text-hint">
          <VolumeSize volume={volume} />
        </span>
      }
      actions={
        can("destructive") &&
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
                      Nothing mounts it right now. A volume outlives the container that created it,
                      so this is often the data from something that was removed and rebuilt — check
                      what is in it first if you are not sure.
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
        ))
      }
    />
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

  // A volume nothing mounts is not a database anybody is running, whatever it
  // is called: the warning is about writing underneath a live process.
  const databaseFiles =
    data !== undefined &&
    data.usedBy.length > 0 &&
    looksLikeDatabase(data.name, ...data.usedBy.flatMap((u) => [u.name, u.destination]))

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

            It is the contents rather than a link to them. "Browse files" was
            a button that closed this panel, changed page, and asked the
            operator to recognise the volume again by a path under
            /var/lib/docker; the answer it was fetching is four lines long and
            fits here. The button survives inside the browser, pointed at
            whichever directory you reached.
          */}
          {data.mountpoint && (
            <section className="space-y-1.5">
              <p className="eyebrow">Contents</p>
              {databaseFiles && <DatabaseStorageWarning />}
              <FileBrowser
                root={data.mountpoint}
                label={data.name}
                emptyNote="Nothing has been written to this volume yet."
              />
            </section>
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
