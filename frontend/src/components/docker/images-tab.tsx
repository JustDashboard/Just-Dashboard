"use client"

import { useCallback, useMemo, useRef, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import {
  Box,
  CheckCircle,
  ChevronDoubleDown,
  Download,
  Layers,
  Question,
  RefreshClockwise,
  Servers,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import type { DockerImage, ImageDetail, ImageUpdateStatus } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import {
  EmptyState,
  ErrorState,
  LoadingPanel,
  LoadingRows,
  Notice,
  Spinner,
} from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader, PanelToolbar, Well } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { Detail, DetailList, SearchInput } from "@/components/page"
import type { ConfirmFn } from "@/components/docker/shared"
import { DiskPanel } from "@/components/docker/disk-panel"
import { Hint, Term } from "@/components/docker/explain"
import { BuildDialog } from "@/components/docker/build-dialog"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/**
 * What an operator types to confirm removing an image, and what the dialog
 * names it. It has to be the image's own tag rather than a fixed phrase — the
 * server keys the confirmation on the same value, for the reason it keys a
 * container's on its name: a phrase that is the same for every row can be
 * typed from muscle memory into the wrong dialog.
 */
function imagePhrase(image: DockerImage): string {
  const tag = image.repoTags[0]
  if (tag && tag !== "<none>:<none>") return tag
  return image.id.replace(/^sha256:/, "").slice(0, 12)
}

function primaryTag(image: DockerImage): string | undefined {
  const tag = image.repoTags.find((t) => t && t !== "<none>:<none>")
  return tag
}

export function ImagesTab({
  confirm,
  pulling: externalPulling,
  onPullingChange,
  building: externalBuilding,
  onBuildingChange,
}: {
  confirm: ConfirmFn
  pulling?: string | null
  onPullingChange?: (ref: string | null) => void
  building?: boolean
  onBuildingChange?: (open: boolean) => void
}) {
  const { can } = useAuth()
  const [filter, setFilter] = useSessionState("docker.images.query", "")
  const [selected, setSelected] = useSessionState<string | null>("docker.images.selected", null)
  // null is closed; a string (possibly empty) opens the dialog seeded with it.
  const [internalPulling, setInternalPulling] = useState<string | null>(null)
  const [internalBuilding, setInternalBuilding] = useState(false)
  const pulling = externalPulling ?? internalPulling
  const setPulling = onPullingChange ?? setInternalPulling
  const building = externalBuilding ?? internalBuilding
  const setBuilding = onBuildingChange ?? setInternalBuilding

  const { data, error, loading, refresh } = usePoll(
    (signal) => get<DockerImage[]>("/docker/images/", undefined, signal),
    30000,
  )

  /**
   * Whether the tags in use still point where they did when they were pulled.
   *
   * The one question a self-hoster actually has about an image, and the one no
   * free tool in this class answers: Watchtower answers it and then restarts
   * things unasked, Portainer's version is a paid feature. It is one registry
   * request per tag, cached for half an hour on the server, and it only covers
   * images containers are running — a stale layer nothing references is not
   * news.
   */
  const updates = usePoll<Record<string, ImageUpdateStatus>>(
    (signal) => get<Record<string, ImageUpdateStatus>>("/docker/images/updates", undefined, signal),
    // Slow: the answer changes when a publisher pushes, not when a page
    // refreshes, and Docker Hub rate-limits by address.
    15 * 60_000,
  )
  const [checking, setChecking] = useState(false)

  const recheck = async () => {
    setChecking(true)
    try {
      await get("/docker/images/updates", { refresh: true })
      updates.refresh()
      notify.success("Checked with the registries")
    } catch (err) {
      notify.error("Could not check for updates", err)
    } finally {
      setChecking(false)
    }
  }

  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    if (!needle) return data ?? []
    return (data ?? []).filter(
      (i) =>
        i.repoTags.some((t) => t.toLowerCase().includes(needle)) ||
        i.id.toLowerCase().includes(needle),
    )
  }, [data, filter])

  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />

  const outdated = Object.values(updates.data ?? {}).filter((u) => u.state === "outdated")

  return (
    <div className="space-y-4">
      {/* Above the image list on purpose: "where did the disk go" is the
          question that brings people to this tab, and images are only ever
          part of the answer. */}
      <DiskPanel confirm={confirm} onPruned={refresh} />

      {outdated.length > 0 && (
        <Notice
          title={`${outdated.length} running image${outdated.length === 1 ? " has" : "s have"} a newer version`}
          icon={ChevronDoubleDown}
        >
          <p>
            {outdated.map((u) => u.ref).join(", ")} — the tag now points somewhere else in the
            registry. Pulling moves this server to it; the containers using it keep running the old
            copy until they are recreated.
          </p>
        </Notice>
      )}

      {/* Plain: the image list is the whole of the page under the disk
          readings, and each image is a card with its own edge — a frame around
          framed cards is the nesting §12 refuses. */}
      <Panel plain className="animate-rise">
        <PanelHeader
          title="Images"
          actions={
            <>
              <IconAction
                label="Check the registries for newer versions"
                onClick={recheck}
                disabled={checking}
              >
                {checking ? <Spinner /> : <RefreshClockwise />}
              </IconAction>
              {can("destructive") && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() =>
                    confirm({
                      title: "Prune images",
                      confirmLabel: "Prune",
                      description: (
                        <p>
                          Removes <Term name="dangling">dangling images</Term> — layers left behind
                          when a tag moved to a newer copy. Nothing references them and no container
                          can need them.
                        </p>
                      ),
                      action: async () => {
                        const rep = await post<{ spaceReclaimed: number }>("/docker/images/prune")
                        notify.success(`Reclaimed ${bytes(rep.spaceReclaimed)}`)
                        refresh()
                      },
                    })
                  }
                >
                  <Trash className="size-4" />
                  Prune dangling
                </Button>
              )}
            </>
          }
        />
        <PanelToolbar>
          <SearchInput
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter by name or id"
          />
        </PanelToolbar>
        <PanelBody flush>
          {visible.length === 0 ? (
            <EmptyState icon={Box} title={filter ? "Nothing matches" : "No images"} />
          ) : (
            // One card per image at every width: each opens the image's own
            // panel, so each is a choice (§16), and the six columns the table
            // named are said by the card itself — the registry's verdict and
            // the size beside the name, the id, age and use beneath it.
            <ChoiceList aria-label="Images" className="animate-rise">
              {visible.map((image) => {
                const tag = primaryTag(image)
                return (
                  <ImageCard
                    key={image.id}
                    image={image}
                    update={tag ? updates.data?.[tag] : undefined}
                    confirm={confirm}
                    onOpen={() => setSelected(image.id)}
                    onPull={setPulling}
                    onChanged={refresh}
                  />
                )
              })}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      <ImageDetailPanel imageId={selected} onOpenChange={(o) => !o && setSelected(null)} />
      <BuildDialog open={building} onOpenChange={setBuilding} onBuilt={refresh} />
      <PullDialog
        open={pulling !== null}
        initial={pulling ?? ""}
        onClose={() => setPulling(null)}
        onDone={() => {
          refresh()
          updates.refresh()
        }}
      />
    </div>
  )
}

/**
 * One image, as a card that opens its detail.
 *
 * The mark is the product the image is — nginx, Postgres, Grafana — because a
 * server's image list is a column of registry paths that all start the same
 * way, and the logo is found before the path is read. An untagged layer is no
 * product and keeps the plain glyph.
 *
 * Nothing the table carried is dropped. The registry's verdict and the size
 * sit beside the name, because "is this the old copy and how much will
 * deleting it give back" is why anybody is on this page; the id, the age and
 * what is running from it are the second line.
 */
function ImageCard({
  image,
  update,
  confirm,
  onOpen,
  onPull,
  onChanged,
}: {
  image: DockerImage
  update: ImageUpdateStatus | undefined
  confirm: ConfirmFn
  onOpen: () => void
  onPull: (ref: string) => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  const tag = primaryTag(image)
  const removable = can("destructive") && image.containers === 0
  const name = image.repoTags.length ? image.repoTags.join(", ") : "untagged"

  return (
    <ChoiceRow
      verb={`Open ${name}`}
      onSelect={onOpen}
      leading={<ProductLogo id={tag ? imageProduct(tag) : undefined} fallback={Layers} size="sm" />}
      title={<span className="font-mono">{image.repoTags.length ? name : <em>untagged</em>}</span>}
      description={
        <>
          <span className="font-mono">{shortId(image.id)}</span> · {relativeTime(image.created)} ·{" "}
          {image.containers > 0
            ? `used by ${image.containers} container${image.containers === 1 ? "" : "s"}`
            : "not in use"}
        </>
      }
      trailing={
        <>
          <span className="hidden w-32 sm:block">
            <UpdateState status={update} dangling={image.dangling} />
          </span>
          <span className="numeric w-20 text-right font-mono text-hint">{bytes(image.size)}</span>
        </>
      }
      actions={
        <span className="flex shrink-0 items-center gap-0.5">
          {can("service.control") && tag && (
            <IconAction label={`Pull a fresh ${tag}`} onClick={() => onPull(tag)}>
              <Download />
            </IconAction>
          )}
          {/*
            An image a container was built from is not deletable in any useful
            sense: Docker refuses, and forcing it leaves a running container
            whose image is gone and which cannot start again. The slot stays
            with a disabled control rather than collapsing, so cards do not
            shift with state.
          */}
          {can("destructive") &&
            (removable ? (
              <IconAction
                label="Remove image"
                className="text-destructive"
                onClick={() =>
                  confirm({
                    title: "Delete image",
                    confirmLabel: "Delete",
                    description: (
                      <>
                        <p>
                          Deletes <b>{imagePhrase(image)}</b>.
                        </p>
                        <p>
                          No container is using it. Anything that needs it later has to pull or
                          rebuild it — which for an image built here and never pushed means
                          rebuilding from source.
                        </p>
                      </>
                    ),
                    action: async (c) => {
                      await del(`/docker/images/${encodeURIComponent(image.id)}`, { confirm: c })
                      onChanged()
                    },
                  })
                }
              >
                <Trash />
              </IconAction>
            ) : (
              <IconAction
                label={`Used by ${image.containers} container${image.containers === 1 ? "" : "s"} — cannot be removed while in use`}
                className="text-muted-foreground opacity-40"
                onClick={onOpen}
              >
                <Trash />
              </IconAction>
            ))}
        </span>
      }
    />
  )
}

/** The handle `docker` prints, without the digest's algorithm in front of it. */
function shortId(id: string) {
  return id.replace(/^sha256:/, "").slice(0, 12)
}

/** The version column: what the registry says about this tag now. */ function UpdateState({
  status,
  dangling,
}: {
  status: ImageUpdateStatus | undefined
  dangling: boolean
}) {
  if (dangling) {
    return <Tag>dangling</Tag>
  }
  if (!status) return <span className="text-hint text-muted-foreground">—</span>

  if (status.state === "outdated") {
    return <Status verdict="warning" label="update available" />
  }
  if (status.state === "pinned") {
    // A digest reference cannot change, so "no update" and "not checked" are
    // both the wrong thing to say about it. This is the state the images tab
    // should be teaching people to want.
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="flex cursor-help items-center gap-1.5 text-hint text-muted-foreground">
            <CheckCircle className="size-3" />
            pinned
          </span>
        </TooltipTrigger>
        <TooltipContent className="max-w-xs">{status.reason}</TooltipContent>
      </Tooltip>
    )
  }
  if (status.state === "current") {
    return (
      <span className="flex items-center gap-1.5 text-hint text-muted-foreground">
        <CheckCircle className="size-3 text-success" />
        current
      </span>
    )
  }
  // "local" and "unknown" both mean "no answer", and the difference between
  // them is the whole point of saying which.
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="flex cursor-help items-center gap-1.5 text-hint text-muted-foreground">
          <Question className="size-3" />
          {status.state === "local" ? "built here" : "not checked"}
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{status.reason}</TooltipContent>
    </Tooltip>
  )
}

/* ---------------------------------------------------------------- detail -- */

/**
 * How this image is named, and what follows from that.
 *
 * The four cases are genuinely different and were being drawn the same way: a
 * registry tag can be pulled and checked; a digest reference is pinned and
 * cannot go out of date; an image built here has no registry copy to fall back
 * on if it is removed; and a dangling image has no name at all and is reachable
 * only by its id.
 */
function ImageReference({ data }: { data: ImageDetail }) {
  return (
    // Not fenced. It was a tinted box, and for a moving tag the `Notice`
    // inside it made a warning box inside a box: the side panel is the frame,
    // the name at the top is the block's title, and the notice stands alone.
    <section className="space-y-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1 truncate font-mono text-body font-medium">{data.ref}</span>
        {data.dangling && <Tag>untagged</Tag>}
        {data.kind === "digest" && <Tag tone="success">digest pinned</Tag>}
        {data.movingTag && <Tag tone="warning">moving tag</Tag>}
        {data.localBuild && !data.dangling && <Tag>built here</Tag>}
      </div>

      {data.repoTags.length > 1 && (
        <p className="text-hint leading-relaxed text-muted-foreground">
          Also tagged {data.repoTags.slice(1).join(", ")}. Removing one tag leaves the image; it
          goes when the last tag does.
        </p>
      )}

      {data.movingTag && (
        <Notice title="Moving tag" tone="warning">
          <p>
            <span className="font-mono">{data.ref}</span> may resolve to another image in the future
            — two servers running &ldquo;the same&rdquo; tag can be running different software, and
            there is no version to roll back to.
          </p>
          {data.repoDigests.length > 0 && (
            <p className="mt-1">Pin by digest to make an update something you choose.</p>
          )}
        </Notice>
      )}

      {data.localBuild && !data.dangling && (
        <Hint>
          This copy was built on this server and never pushed, so there is no registry copy to pull
          back. Deleting it means rebuilding from source.
        </Hint>
      )}

      {data.dangling && (
        <Hint>
          Every tag this image had has been taken by a rebuild, so nothing refers to it by name. It
          is reachable only by its id and is what a cleanup sweep removes first.
        </Hint>
      )}

      {data.repoDigests.length > 0 && (
        <details className="text-hint">
          <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
            Registry digests
          </summary>
          <ul className="mt-1 space-y-0.5 font-mono break-all text-muted-foreground">
            {data.repoDigests.map((digest) => (
              <li key={digest}>{digest}</li>
            ))}
          </ul>
          <p className="mt-1 text-muted-foreground">
            Using one of these in place of the tag is what &ldquo;pin the digest&rdquo; means: the
            reference then names one exact image and cannot move.
          </p>
        </details>
      )}
    </section>
  )
}

function ImageDetailPanel({
  imageId,
  onOpenChange,
}: {
  imageId: string | null
  onOpenChange: (open: boolean) => void
}) {
  const { data, error, loading } = usePoll<ImageDetail>(
    (signal) =>
      get<ImageDetail>(`/docker/images/${encodeURIComponent(imageId ?? "")}`, undefined, signal),
    0,
    [imageId],
    { enabled: imageId !== null },
  )

  return (
    <SidePanel
      open={imageId !== null}
      onOpenChange={onOpenChange}
      width="xl"
      title={data?.repoTags[0] ?? "Image"}
      description={data?.id.replace("sha256:", "").slice(0, 24)}
    >
      {error && <ErrorState error={error} />}
      {loading && !data && <LoadingRows />}
      {data && (
        <div className="space-y-6">
          {/*
            What this image can be asked to do, before anything about what it
            contains. An image built here and never pushed cannot be pulled
            back if it is deleted; one named only by a digest cannot go out of
            date; a dangling one has no name at all. All three used to look
            identical to a tagged registry image.
          */}
          <ImageReference data={data} />

          <section className="space-y-2">
            <p className="eyebrow">Details</p>
            <DetailList className="gap-y-2">
              <Detail label="Size" className="text-body font-medium">
                {bytes(data.size)}
              </Detail>
              <Detail label="Created" className="text-body">
                {relativeTime(data.created)}
              </Detail>
              <Detail label="Platform" className="text-body">
                {data.os ?? "?"}/{data.architecture ?? "?"}
              </Detail>
              <Detail label="Runs as" className="text-body">
                {data.user || "root"}
              </Detail>
              <Detail label="Working dir" className="font-mono text-body">
                {data.workingDir || "/"}
              </Detail>
              <Detail label="Entrypoint">
                <span className="font-mono text-body break-all">
                  {data.entrypoint.length ? data.entrypoint.join(" ") : "—"}
                </span>
              </Detail>
              <Detail label="Command">
                <span className="font-mono text-body break-all">
                  {data.command.length ? data.command.join(" ") : "—"}
                </span>
              </Detail>
            </DetailList>
          </section>

          {data.exposedPorts.length > 0 && (
            <section className="space-y-2">
              <p className="eyebrow">Ports the image expects to serve on</p>
              <div className="flex flex-wrap gap-1.5">
                {data.exposedPorts.map((p) => (
                  <Tag key={p} mono>
                    {p}
                  </Tag>
                ))}
              </div>
              <Hint className="leading-relaxed">
                Publishing one of these is what makes the service reachable. Nothing is published
                automatically.
              </Hint>
            </section>
          )}

          {data.volumePaths.length > 0 && (
            <section className="space-y-2">
              <p className="eyebrow">Paths the image expects storage at</p>
              <div className="flex flex-wrap gap-1.5">
                {data.volumePaths.map((p) => (
                  <Tag key={p} mono>
                    {p}
                  </Tag>
                ))}
              </div>
              <Hint className="leading-relaxed">
                Without a <Term name="volume">volume</Term> mounted here, Docker creates an unnamed
                one — the data survives, under a name nobody will recognise later.
              </Hint>
            </section>
          )}

          <section className="space-y-2">
            <p className="eyebrow">Used by</p>
            {data.usedBy.length === 0 ? (
              <Hint className="leading-relaxed">
                No container was created from this image. Safe to delete unless you are keeping it
                deliberately.
              </Hint>
            ) : (
              <RowList>
                {data.usedBy.map((c) => (
                  <Row
                    key={c.id}
                    title={c.name}
                    trailing={<Status state={c.state} />}
                    className="px-0 py-2"
                  />
                ))}
              </RowList>
            )}
          </section>

          <section className="space-y-2">
            <p className="eyebrow">
              <Term name="image_layer">Layers</Term>
            </p>
            <Hint className="leading-relaxed">
              Read bottom-up: each line is an instruction from the Dockerfile that built it. The
              large ones are where the size went.
            </Hint>
            <div className="space-y-0.5">
              {data.layers.map((layer, i) => (
                <div
                  key={`${layer.id}-${i}`}
                  className="flex items-start gap-3 py-1 font-mono text-xs leading-relaxed"
                >
                  <span className="numeric w-16 shrink-0 text-right text-muted-foreground">
                    {layer.size > 0 ? bytes(layer.size, 0) : "—"}
                  </span>
                  <span className="min-w-0 flex-1 break-all">
                    {layer.createdBy || layer.comment}
                  </span>
                </div>
              ))}
            </div>
          </section>

          {data.env.length > 0 && (
            <section className="space-y-1.5">
              <p className="eyebrow">Environment baked into the image</p>
              <Well className="max-h-48">{data.env.join("\n")}</Well>
            </section>
          )}
        </div>
      )}
    </SidePanel>
  )
}

/* ------------------------------------------------------------------ pull -- */

/**
 * A pull, with the layer progress a pull actually has.
 *
 * Downloading a gigabyte behind a spinner is indistinguishable from a hang,
 * and it is the difference between "this is slow" and "this is broken". The
 * socket has existed on the backend all along; nothing used it.
 */
function PullDialog({
  open,
  initial,
  onClose,
  onDone,
}: {
  open: boolean
  initial: string
  onClose: () => void
  onDone: () => void
}) {
  const [ref, setRef] = useState(initial)
  const pull = usePullProgress()

  // The dialog is keyed on `initial` by its parent, so this runs once per
  // opening rather than fighting the input on every render.
  const [seeded, setSeeded] = useState(initial)
  if (seeded !== initial) {
    setSeeded(initial)
    setRef(initial)
  }

  const start = async () => {
    const ok = await pull.pull(ref)
    if (ok) {
      notify.success(`${ref} pulled`)
      onDone()
    } else {
      notify.error(`Could not pull ${ref}`)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => {
        // Never close mid-pull: the socket is what drives it, and unmounting
        // the dialog would abandon a download in progress with no way back to
        // its output.
        if (pull.active || next) return
        pull.reset()
        onClose()
      }}
      size="lg"
      title="Pull an image"
      description="Downloads it to this server. Containers already running an older copy keep running it
            until they are recreated."
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={pull.active}>
            Close
          </Button>
          <Button onClick={start} disabled={!ref.trim() || pull.active} pending={pull.active}>
            <Servers className="size-4" />
            Pull
          </Button>
        </>
      }
    >
      <div className="space-y-2">
        <Input
          value={ref}
          spellCheck={false}
          placeholder="nginx:alpine"
          className="font-mono"
          onChange={(e) => setRef(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && ref.trim() && !pull.active && start()}
        />
        <Hint>
          Leave the version off and you get <span className="font-mono">latest</span>, which is{" "}
          <Term name="tag">whatever the publisher last pushed</Term>.
        </Hint>
      </div>
      {(pull.active || pull.lines.length > 0) && (
        <Well className="max-h-56">
          {pull.lines.length === 0 ? "Starting…" : pull.lines.join("\n")}
        </Well>
      )}
    </Modal>
  )
}

/**
 * Pulls an image over the progress socket, with the layer progress a pull
 * actually has.
 *
 * Downloading a gigabyte behind a spinner is indistinguishable from a hang,
 * and it is the difference between "this is slow" and "this is broken".
 * (Previously lived beside the removed standalone create-container flow, which
 * was its only other caller.)
 */
function usePullProgress() {
  const [ref, setRef] = useState<string>()
  const [lines, setLines] = useState<string[]>([])
  const [done, setDone] = useState(false)
  const resolveRef = useRef<(ok: boolean) => void>(undefined)

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "progress") {
      const msg = envelope.data as { id?: string; status: string; progress?: string }
      setLines((prev) => {
        const text = [msg.id, msg.status, msg.progress].filter(Boolean).join(" ")
        return [...prev.slice(-200), text]
      })
    } else if (envelope.type === "done") {
      setDone(true)
      resolveRef.current?.(true)
    } else if (envelope.type === "error") {
      setLines((prev) => [...prev, envelope.error ?? "pull failed"])
      setDone(true)
      resolveRef.current?.(false)
    }
  }, [])

  const query = useMemo(() => ({ ref: ref ?? "" }), [ref])
  useSocket("/docker/images/pull", { onMessage, enabled: Boolean(ref), query })

  const pull = (image: string) => {
    setLines([])
    setDone(false)
    setRef(image)
    return new Promise<boolean>((resolve) => {
      resolveRef.current = resolve
    })
  }
  const reset = () => {
    setRef(undefined)
    setLines([])
    setDone(false)
  }
  return { pull, reset, lines, done, active: Boolean(ref) && !done }
}
