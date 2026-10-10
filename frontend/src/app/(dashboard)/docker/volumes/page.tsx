"use client"

import { useCallback, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { Archive, Copy, Database, FolderOpen, Plus, Trash } from "@/components/icons"
import { del, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BackupResourceReport, ComposeStack, Container, VolumeDetail } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useArrivals } from "@/hooks/use-arrivals"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { useConfirm } from "@/components/confirm-dialog"
import { Hint, Term } from "@/components/docker/explain"
import { VolumeBand } from "@/components/docker/volume-band"
import { VolumeSheet } from "@/components/docker/volume-sheet"
import { VolumeTable } from "@/components/docker/volume-table"
import {
  FIRST_DIRECTION,
  STANDING_LABEL,
  backupOf,
  holderOf,
  holders,
  inShow,
  pruneShare,
  pruneSurprise,
  standing,
  visibleVolumes,
  withLiveStates,
  type Backup,
  type Show,
  type SortKey,
  type Standing,
  type VolumeSort,
} from "@/components/docker/volumes"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Modal } from "@/components/modal"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { containerProduct } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import type { Verb } from "@/components/verbs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"

/**
 * Reading register, design-system §15, on the Images page's shape: Docker's
 * identity line with the verdict at its end, a band of two questions, and one
 * framed table whose rows open a sheet.
 *
 * The page was a title, three chips and a column of grey cards, each a name
 * over "1 container", and nothing on it moved or was told apart by anything
 * but its words. Each part went where it is said better. Which volumes a prune
 * would delete is the verdict, and a press narrows the table to them; who
 * holds the data and where every volume stands is the band, each line a
 * filter; and the cards are a table, its widest column the containers that
 * mount each volume, by name, state and path.
 *
 * It also stopped saying something false. The cards called a volume whose
 * only container was stopped one "prune would delete", and the prune dialog
 * said a stopped stack's data would go. The daemon counts a stopped
 * container's mount as a use and its prune keeps that volume; what it takes
 * is a volume nothing mounts at all, which is what a stack taken down with
 * `docker compose down` leaves. Those are the ones drawn in amber here.
 */

export default function DockerVolumesPage() {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const wide = useMediaQuery("(min-width: 1280px)")
  const widest = useMediaQuery("(min-width: 1536px)")
  const [query, setQuery] = useSessionState("docker.volumes.query", "")
  const [show, setShow] = useViewState<Show>("docker.volumes.show", "all")
  const [only, setOnly] = useSessionState<Standing | "">("docker.volumes.standing", "")
  const [holder, setHolder] = useSessionState("docker.volumes.holder", "")
  const [sort, setSort] = useViewState<VolumeSort>("docker.volumes.sort", {
    key: "size",
    desc: true,
  })
  // In the URL so a deployment can link straight at the volume it depends on.
  const [selected, setSelected] = useQuerySelection("volume")
  const [creating, setCreating] = useState(false)
  const [removing, setRemoving] = useState<string>()
  const [pruning, setPruning] = useState(false)
  const [live, setLive] = useState<Container[]>()

  const list = usePoll(
    (signal) => get<VolumeDetail[]>("/docker/volumes/", undefined, signal),
    30_000,
  )
  const stacks = usePoll(
    (signal) => get<ComposeStack[]>("/docker/stacks/", undefined, signal),
    60_000,
  )
  /*
   * Whether a job covers each volume, as the Backups page counts it. Discovery
   * walks every module and can take seconds, and the answer moves when a job
   * is written or runs, so it is asked for rarely; a reader the route refuses
   * simply gets no backup column.
   */
  const coverage = usePoll(
    (signal) => get<BackupResourceReport>("/backups/resources", undefined, signal),
    5 * 60_000,
  )
  const engine = usePoll(
    (signal) => get<{ serverVersion?: string }>("/docker/ping", undefined, signal),
    0,
  ).data

  // Container state from the socket the Containers page reads, so a dot here
  // changes the moment Docker's does rather than at the next poll of the list.
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "containers") setLive(envelope.data as Container[])
  }, [])
  useSocket("/docker/containers/stream", { onMessage })

  const states = useMemo(() => new Map((live ?? []).map((c) => [c.id, c.state])), [live])
  const productOf = useMemo(
    () => new Map((live ?? []).map((c) => [c.id, containerProduct(c)])),
    [live],
  )
  const volumes = useMemo(() => withLiveStates(list.data ?? [], states), [list.data, states])
  const backups = useMemo(() => {
    const map = new Map<string, Backup>()
    for (const resource of coverage.data?.resources ?? []) {
      if (resource.kind !== "volume") continue
      const backup = backupOf(resource)
      if (backup) map.set(resource.id, backup)
    }
    return map
  }, [coverage.data])
  const known = useMemo(() => new Set((stacks.data ?? []).map((s) => s.name)), [stacks.data])
  // A remembered "Not backed up" waits for the coverage it filters by rather
  // than drawing an empty table until the Backups page answers.
  const showing = show === "unprotected" && !coverage.data ? "all" : show
  const visible = useMemo(
    () => visibleVolumes(volumes, { query, show: showing, standing: only, holder, sort, backups }),
    [volumes, query, showing, only, holder, sort, backups],
  )
  const arrived = useArrivals(volumes.map((v) => v.name))

  const refresh = () => {
    list.refresh()
    coverage.refresh()
  }

  const share = pruneShare(volumes)
  const stored = volumes.reduce((sum, v) => sum + Math.max(v.size, 0), 0)
  const mounted = volumes.filter((v) => v.usedBy.length > 0).length
  const covered = volumes.filter((v) => backups.get(v.name)?.state === "protected").length
  const count = (s: Show) => volumes.filter((v) => inShow(v, s, backups.get(v.name))).length
  const holderName = holder
    ? (holders(volumes).find((h) => h.key === holder)?.name ?? holder.replace(/^\w+:/, ""))
    : ""

  const narrow = (next: { show?: Show; only?: Standing | ""; holder?: string }) => {
    setShow(next.show ?? "all")
    setOnly(next.only ?? "")
    setHolder(next.holder ?? "")
  }

  const remove = (volume: VolumeDetail) =>
    confirm({
      title: "Delete volume",
      confirmLabel: "Delete",
      description: (
        <>
          <p className="text-destructive">
            Everything stored in <b>{volume.name}</b>
            {volume.size > 0 && <> — {bytes(volume.size)} —</>} is destroyed permanently.
          </p>
          <p>
            {standing(volume) === "down"
              ? `It is what ${holderOf(volume).name} left when it was taken down; deploying that stack again would start it empty.`
              : "Nothing mounts it. A volume outlives the container that created it, so this is often the data from something that was removed and rebuilt — look at what is in it first if you are not sure."}
          </p>
        </>
      ),
      action: async (phrase) => {
        setRemoving(volume.name)
        try {
          await del(`/docker/volumes/${encodeURIComponent(volume.name)}`, { confirm: phrase })
          notify.success(`${volume.name} deleted`)
          if (selected === volume.name) setSelected(null)
          refresh()
        } finally {
          setRemoving(undefined)
        }
        return "reported"
      },
    })

  /**
   * The list is read again before the confirmation opens, so what it names is
   * what nothing mounts now rather than half a minute ago. Docker still
   * decides when the prune runs, so what it actually deleted is compared with
   * what was named, and a difference is said rather than left to be found.
   */
  const prune = async () => {
    let fresh: VolumeDetail[]
    try {
      fresh = await get<VolumeDetail[]>("/docker/volumes/")
    } catch (err) {
      notify.error("Could not read the volumes", err)
      return
    }
    list.refresh()
    const named = pruneShare(fresh)
    if (named.volumes.length === 0) {
      notify.info("A prune would delete nothing: every volume is mounted")
      return
    }
    confirm({
      title: "Prune volumes",
      confirmLabel: "Prune",
      description: (
        <>
          <p className="text-destructive">
            Deletes {plural(named.volumes.length, "volume")}
            {named.size > 0 && <> and the {bytes(named.size)} in them</>}, permanently:
          </p>
          <ul className="max-h-48 space-y-0.5 overflow-auto font-mono text-xs">
            {named.volumes.map((v) => (
              <li key={v.name} className="flex min-w-0 justify-between gap-3">
                <span className="truncate">{v.name}</span>
                <span className="shrink-0 text-muted-foreground">
                  {standing(v) === "down" ? `${holderOf(v).name} · ` : ""}
                  {v.size > 0 ? bytes(v.size) : "—"}
                </span>
              </li>
            ))}
          </ul>
          <p>
            Each is a local volume no container mounts, running or stopped — Docker&apos;s own
            meaning of unused. A volume a stopped container holds is kept.
            {named.volumes.some((v) => standing(v) === "down") &&
              " The ones naming a stack are what it left when it was taken down: deploying it again would start it empty."}
          </p>
          <p className="text-muted-foreground">
            Docker decides as the prune runs: a volume unmounted after this list was read goes too,
            and the result names it.
          </p>
        </>
      ),
      action: async (phrase) => {
        setPruning(true)
        try {
          const rep = await post<{ spaceReclaimed: number; items: string[] }>(
            "/docker/volumes/prune",
            undefined,
            { confirm: phrase },
          )
          const { extra, kept } = pruneSurprise(
            named.volumes.map((v) => v.name),
            rep.items,
          )
          const done = `Deleted ${plural(rep.items.length, "volume")}, reclaimed ${bytes(rep.spaceReclaimed)}`
          const said = [
            extra.length > 0 &&
              `Not on the list, and deleted because nothing mounted ${extra.length === 1 ? "it" : "them"} by then: ${extra.join(", ")}.`,
            kept.length > 0 &&
              `Kept because a container mounted ${kept.length === 1 ? "it" : "them"} by then: ${kept.join(", ")}.`,
          ].filter(Boolean)
          const description = said.length > 0 ? said.join(" ") : undefined
          if (extra.length > 0) notify.warning(done, { description })
          else notify.success(done, { description })
          refresh()
        } finally {
          setPruning(false)
        }
        return "reported"
      },
    })
  }

  /**
   * What can be done to a volume, declared once for its row and its sheet
   * (§13). Remove is the one glyph on a row; it stays drawn while something
   * mounts the volume, disabled with the reason as its name, because Docker
   * refuses it and a control that vanishes says nothing. Backing it up hands
   * its path to the Backups form.
   */
  const verbsFor = (volume: VolumeDetail): Verb[] => {
    const verbs: Verb[] = []
    const held = volume.usedBy.length > 0
    if (can("destructive")) {
      verbs.push({
        key: "remove",
        label: held ? `Remove — mounted by ${plural(volume.usedBy.length, "container")}` : "Remove",
        icon: Trash,
        inline: true,
        danger: true,
        disabled: held || removing === volume.name,
        run: () => remove(volume),
      })
    }
    if (
      can("system.admin") &&
      volume.driver === "local" &&
      backups.get(volume.name)?.state !== "protected"
    ) {
      verbs.push({
        key: "backup",
        label: "Back up…",
        icon: Archive,
        run: () => router.push(`/backups?source=${encodeURIComponent(volume.mountpoint)}`),
      })
    }
    verbs.push({
      key: "files",
      label: "Open in Files",
      icon: FolderOpen,
      run: () => router.push(`/files?path=${encodeURIComponent(volume.mountpoint)}`),
    })
    verbs.push({
      key: "copy-name",
      label: "Copy name",
      icon: Copy,
      run: () => void copyText(volume.name, "Name copied"),
    })
    verbs.push({
      key: "copy-path",
      label: "Copy path",
      icon: Copy,
      run: () => void copyText(volume.mountpoint, "Path copied"),
    })
    return verbs
  }
  // The sheet's footer is a row of words: no copying, and the destructive one last.
  const sheetVerbs = (volume: VolumeDetail) =>
    verbsFor(volume)
      .filter((v) => !v.key.startsWith("copy"))
      .sort((a, b) => Number(Boolean(a.danger)) - Number(Boolean(b.danger)))

  const onSort = (key: SortKey) =>
    setSort((current) =>
      current.key === key ? { key, desc: !current.desc } : { key, desc: FIRST_DIRECTION[key] },
    )

  const filtered = query !== "" || showing !== "all" || only !== "" || holder !== ""

  return (
    <Workspace
      name="Volumes"
      refresh={refresh}
      escape={() => {
        if (holder) {
          setHolder("")
          return true
        }
        if (only) {
          setOnly("")
          return true
        }
        if (show !== "all") {
          setShow("all")
          return true
        }
        if (!query) return false
        setQuery("")
        return true
      }}
    >
      <Page>
        {dialog}
        <PageContext eyebrow="Docker" title="Volumes" />

        {/* The engine the volumes belong to, as the line Images opens on:
            Docker as the mark, what it stores as facts, and at the right end
            the verdict — a press narrows the table to what a prune would
            delete — beside the one way a volume is made here. */}
        <HostIdentity
          className="animate-rise sm:[&>div:first-child]:min-w-64 [&>div:last-child]:max-w-full [&>div:last-child]:min-w-0 [&>div:last-child]:shrink"
          mark="docker"
          fallback={Database}
          title={
            <>
              Docker
              {engine?.serverVersion && (
                <span className="ml-2 font-mono text-body font-normal text-muted-foreground">
                  {engine.serverVersion}
                </span>
              )}
            </>
          }
          facts={
            list.data && (
              <>
                <span className="numeric font-medium text-foreground">
                  {plural(volumes.length, "volume")}
                </span>
                <FactDot />
                <span className="numeric">{bytes(stored)} stored</span>
                <FactDot />
                <span className="numeric">{mounted} mounted</span>
                {coverage.data && (
                  <>
                    <FactDot />
                    <span className="numeric">
                      {covered} of {volumes.length} backed up
                    </span>
                  </>
                )}
              </>
            )
          }
          aside={
            <div className="flex max-w-full flex-wrap items-center gap-2">
              <span className="w-full text-body sm:mr-2 sm:w-auto">
                {share.volumes.length > 0 ? (
                  <button
                    type="button"
                    aria-label={`Only the ${plural(share.volumes.length, "volume")} a prune would delete`}
                    onClick={() => narrow({ show: "prunable" })}
                    className="rounded-sm focus-ring hover:underline"
                  >
                    <Status
                      verdict="warning"
                      label={`${plural(share.volumes.length, "volume")} a prune would delete`}
                    />
                  </button>
                ) : list.data && volumes.length > 0 ? (
                  <Status verdict="ok" label="A prune would delete nothing" />
                ) : null}
              </span>
              {can("service.control") && (
                <Button size="sm" onClick={() => setCreating(true)}>
                  <Plus className="size-4" />
                  Create volume
                </Button>
              )}
              <WorkspaceHelp compact />
            </div>
          }
        />

        {list.loading && !list.data ? (
          <LoadingPanel />
        ) : list.error && !list.data ? (
          <ErrorState error={list.error} onRetry={list.refresh} />
        ) : (
          <>
            {volumes.length > 0 && (
              <VolumeBand
                volumes={volumes}
                productOf={productOf}
                holder={holder}
                onHolder={(key) => narrow({ holder: key })}
                selected={only}
                onSelect={(next) => narrow({ only: next })}
                pruning={pruning}
                onPrune={can("destructive") ? prune : undefined}
              />
            )}

            {/* Framed, as every table is (§2): its container scrolls, and an
                edge says where the columns that did not fit went. */}
            <Panel className="animate-rise">
              <PanelHeader
                title="Volumes"
                actions={
                  filtered &&
                  volumes.length > 0 && (
                    <span className="numeric text-hint text-muted-foreground">
                      {visible.length} of {volumes.length}
                    </span>
                  )
                }
              />
              {volumes.length > 0 && (
                <PanelToolbar>
                  <SearchInput
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder="Volume, container or path"
                    aria-label="Find volumes"
                  />
                  <div className="flex min-w-0 flex-wrap items-center gap-1">
                    <FilterChip selected={showing === "all"} onClick={() => setShow("all")}>
                      All <ChipCount>{volumes.length}</ChipCount>
                    </FilterChip>
                    {(
                      [
                        ["running", "In use"],
                        ["stopped", "Stopped"],
                        ["unmounted", "Not mounted"],
                        ...(coverage.data ? [["unprotected", "Not backed up"]] : []),
                      ] as [Show, string][]
                    ).map(([key, label]) =>
                      count(key) > 0 ? (
                        <FilterChip
                          key={key}
                          selected={showing === key}
                          onClick={() => setShow(key)}
                        >
                          {label} <ChipCount>{count(key)}</ChipCount>
                        </FilterChip>
                      ) : null,
                    )}
                  </div>
                  {showing === "prunable" && (
                    <FilterChip
                      selected
                      onClick={() => setShow("all")}
                      aria-label="Clear prune filter"
                    >
                      A prune would delete ×
                    </FilterChip>
                  )}
                  {holder && (
                    <FilterChip
                      selected
                      onClick={() => setHolder("")}
                      aria-label="Clear holder filter"
                    >
                      {holderName} ×
                    </FilterChip>
                  )}
                  {only && (
                    <FilterChip
                      selected
                      onClick={() => setOnly("")}
                      aria-label="Clear mount filter"
                    >
                      {STANDING_LABEL[only]} ×
                    </FilterChip>
                  )}
                </PanelToolbar>
              )}
              <PanelBody flush>
                {volumes.length === 0 ? (
                  <EmptyState
                    icon={Database}
                    className="m-4"
                    title="No volumes on this server"
                    description={
                      <>
                        A <Term name="volume">volume</Term> is storage Docker manages, kept outside
                        a container so it survives being recreated.
                      </>
                    }
                    action={
                      can("service.control") && (
                        <Button size="sm" onClick={() => setCreating(true)}>
                          <Plus className="size-4" />
                          Create volume
                        </Button>
                      )
                    }
                  />
                ) : visible.length === 0 ? (
                  <EmptyState
                    icon={Database}
                    className="m-4"
                    title="Nothing matches"
                    description="Clear the search or the filters to see every volume."
                    action={
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => {
                          setQuery("")
                          narrow({})
                        }}
                      >
                        Clear filters
                      </Button>
                    }
                  />
                ) : (
                  <VolumeTable
                    volumes={visible}
                    productOf={productOf}
                    backups={backups}
                    wide={wide}
                    widest={widest}
                    arrived={arrived}
                    removing={removing}
                    sort={sort}
                    onSort={onSort}
                    verbs={verbsFor}
                    onOpen={setSelected}
                    onHolder={(key) => narrow({ holder: key })}
                  />
                )}
              </PanelBody>
            </Panel>
          </>
        )}

        <VolumeSheet
          name={selected}
          states={states}
          productOf={productOf}
          backup={selected ? backups.get(selected) : undefined}
          stacks={known}
          verbs={sheetVerbs}
          onOpenChange={(open) => !open && setSelected(null)}
        />
        <NewVolumeDialog open={creating} onOpenChange={setCreating} onCreated={refresh} />
      </Page>
    </Workspace>
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
      description="Storage Docker manages, ready to mount into a container. Empty until something writes to it."
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
