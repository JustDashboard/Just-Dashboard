"use client"

import { useEffect, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { ArrowRight, Plus, Trash } from "@/components/icons"
import { ApiError, del, get, post } from "@/lib/api"
import type { Board, BoardPreview, BoardResource, BoardSummary } from "@/lib/boards"
import { plural, relativeTime } from "@/lib/format"
import type { DbFleet, DbFleetEntry, DeploymentFleet, DeploymentSummary } from "@/lib/types"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { CONTROL } from "@/components/flow"
import { LogoGlyph } from "@/components/logo"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput, Section } from "@/components/page"
import { EmptyState, ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { BlurFade } from "@/components/ui/blur-fade"
import { Skeleton } from "@/components/ui/skeleton"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { VerbMenu, type Verb } from "@/components/verbs"
import { DrawingPreview } from "@/components/boards/drawing-preview"
import { EngineGlyph } from "@/components/database/kit/engine-mark"
import { engineFor } from "@/components/database/engine"
import { ProjectMark } from "@/components/deploy/project-mark"

/** As many columns as the width holds at twenty-two rems a card, one on a phone. */
const GRID = "grid min-w-0 gap-3 grid-cols-[repeat(auto-fill,minmax(min(22rem,100%),1fr))]"

/** A name filter earns its row once there are more drawings than one screen shows. */
const SEARCH_FROM = 7

/**
 * Every Excalidraw drawing on this server, each drawn as itself.
 *
 * A reading page (§16). It opens the way the Overview opens on the machine:
 * the product it is, as its own mark, with a line of facts where four stat
 * tiles stood — a count of drawings in a 24px figure said nothing a glance at
 * the cards below did not. Each card is a drawing to enter, so it carries the
 * lit edge of a thing you take; its picture is the drawing, sketched in as it
 * lands, and the things on this server it links to are drawn as themselves
 * under it.
 */
export function DrawingList() {
  const router = useRouter()
  const { can } = useAuth()
  const canWrite = can("service.control")
  const { confirm, dialog } = useConfirm()
  const [boards, setBoards] = useState<BoardSummary[] | null>(null)
  const [error, setError] = useState<Error | null>(null)
  const [creating, setCreating] = useState(false)
  const [query, setQuery] = useState("")
  const linked = useLinked(boards)

  useEffect(() => {
    let cancelled = false
    get<BoardSummary[]>("/boards/")
      .then((list) => !cancelled && setBoards(list))
      .catch(
        (cause) =>
          !cancelled && setError(cause instanceof Error ? cause : new Error(String(cause))),
      )
    return () => {
      cancelled = true
    }
  }, [])

  const shown = useMemo(() => {
    const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
    return (boards ?? []).filter((b) => words.every((w) => b.name.toLowerCase().includes(w)))
  }, [boards, query])

  async function create() {
    setCreating(true)
    try {
      const board = await post<Board>("/boards/", { name: "Untitled drawing" })
      router.push(`/excalidraw/${board.id}`)
    } catch (cause) {
      notify.error("Could not create the drawing", cause)
      setCreating(false)
    }
  }

  function remove(board: BoardSummary) {
    confirm({
      title: "Delete drawing",
      description: `Delete “${board.name}” and everything drawn on it? This cannot be undone.`,
      subject: {
        mark: (
          <DrawingPreview
            preview={board.preview}
            sketch={false}
            className="h-10 w-16 shrink-0 overflow-hidden rounded-md border"
          />
        ),
        name: board.name,
      },
      confirmLabel: "Delete drawing",
      action: async () => {
        try {
          await del(`/boards/${board.id}`)
        } catch (cause) {
          if (!(cause instanceof ApiError && cause.code === "board_not_found")) throw cause
        }
        setBoards((list) => list?.filter((b) => b.id !== board.id) ?? null)
      },
    })
  }

  const newDrawing = canWrite && (
    <Button onClick={() => void create()} pending={creating}>
      <Plus /> New drawing
    </Button>
  )

  return (
    <Page>
      <PageContext title="Excalidraw" />
      {error && !boards ? (
        <ErrorState error={error} />
      ) : !boards ? (
        <ListSkeleton />
      ) : (
        <>
          <Identity boards={boards} actions={newDrawing} />
          {boards.length === 0 ? (
            <EmptyState
              mark={
                <DrawingPreview
                  preview={SKETCH}
                  className="aspect-video w-full max-w-sm overflow-hidden rounded-lg border border-hairline"
                />
              }
              title="Start your first drawing"
              description="An endless canvas in Excalidraw’s hand: sketch how this server fits together, drop in cards that open its projects and databases, paste in screenshots. Everything is saved here, on this server."
              action={
                canWrite ? (
                  <Button onClick={() => void create()} pending={creating}>
                    New drawing
                  </Button>
                ) : undefined
              }
              className="py-10"
            />
          ) : (
            <Section
              title="Drawings"
              actions={
                boards.length >= SEARCH_FROM && (
                  <SearchInput
                    dense
                    value={query}
                    placeholder="Find a drawing"
                    aria-label="Find a drawing"
                    onChange={(event) => setQuery(event.target.value)}
                    containerClassName="w-full sm:w-64"
                  />
                )
              }
            >
              {shown.length === 0 ? (
                <EmptyState
                  title="No drawing matches"
                  description={`Nothing is called “${query.trim()}”.`}
                />
              ) : (
                <ul aria-label="Drawings" className={GRID}>
                  {shown.map((board, index) => (
                    <DrawingCard
                      key={board.id}
                      board={board}
                      index={index}
                      linked={linked}
                      onOpen={() => router.push(`/excalidraw/${board.id}`)}
                      verbs={
                        can("destructive")
                          ? [
                              {
                                key: "delete",
                                label: "Delete",
                                icon: Trash,
                                danger: true,
                                run: () => remove(board),
                              },
                            ]
                          : []
                      }
                    />
                  ))}
                </ul>
              )}
            </Section>
          )}
        </>
      )}
      {dialog}
    </Page>
  )
}

/**
 * The product the page is, and what it holds, in the line the Overview gives
 * the machine.
 */
function Identity({ boards, actions }: { boards: BoardSummary[]; actions: React.ReactNode }) {
  const shapes = boards.reduce((sum, b) => sum + (b.preview?.count ?? 0), 0)
  const links = new Set(
    boards.flatMap((b) => (b.preview?.resources ?? []).map((r) => `${r.kind}:${r.id ?? ""}`)),
  ).size
  const latest = boards[0]
  return (
    <HostIdentity
      mark="excalidraw"
      title="Excalidraw"
      aside={actions}
      facts={
        <>
          <span className="numeric">{plural(boards.length, "drawing")}</span>
          {shapes > 0 && (
            <>
              <FactDot />
              <span className="numeric">{plural(shapes, "shape")}</span>
            </>
          )}
          {links > 0 && (
            <>
              <FactDot />
              <span className="numeric">{plural(links, "server item")} linked</span>
            </>
          )}
          {latest && (
            <>
              <FactDot />
              <span className="min-w-0 truncate">
                <span className="font-medium text-foreground">{latest.name}</span> edited{" "}
                {relativeTime(latest.updatedAt)}
              </span>
            </>
          )}
          <FactDot />
          <span>Saved on this server</span>
        </>
      }
    />
  )
}

function ListSkeleton() {
  return (
    <>
      <Skeleton aria-hidden className="h-18 w-full rounded-xl" />
      <ul aria-hidden className={GRID}>
        {[0, 1, 2].map((index) => (
          <li key={index}>
            <Skeleton className="h-66 rounded-xl" />
          </li>
        ))}
      </ul>
    </>
  )
}

type Linked = {
  projects: Map<number, DeploymentSummary>
  databases: Map<number, DbFleetEntry>
}

/**
 * The projects and databases the drawings place cards for, read only when a
 * drawing has one — the same two reads the editor's insert panel makes. A
 * card keeps the name it was given when it was placed; these give it its
 * current mark, and a read that fails leaves the card's own name standing.
 */
function useLinked(boards: BoardSummary[] | null): Linked {
  const [linked, setLinked] = useState<Linked>({ projects: new Map(), databases: new Map() })
  const wants = useMemo(() => {
    const kinds = new Set(
      (boards ?? []).flatMap((b) => (b.preview?.resources ?? []).map((r) => r.kind)),
    )
    return { projects: kinds.has("project"), databases: kinds.has("database") }
  }, [boards])

  useEffect(() => {
    if (!wants.projects && !wants.databases) return
    let cancelled = false
    void Promise.allSettled([
      wants.projects ? get<DeploymentFleet>("/deploy/", { view: "fleet" }) : null,
      wants.databases ? get<DbFleet>("/databases/fleet") : null,
    ]).then(([fleet, databases]) => {
      if (cancelled) return
      setLinked({
        projects: new Map(
          fleet.status === "fulfilled" && fleet.value
            ? fleet.value.deployments.map((d) => [d.id, d])
            : [],
        ),
        databases: new Map(
          databases.status === "fulfilled" && databases.value
            ? databases.value.connections.map((c) => [c.id, c])
            : [],
        ),
      })
    })
    return () => {
      cancelled = true
    }
  }, [wants])

  return linked
}

function DrawingCard({
  board,
  index,
  linked,
  onOpen,
  verbs,
}: {
  board: BoardSummary
  index: number
  linked: Linked
  onOpen: () => void
  verbs: Verb[]
}) {
  const count = board.preview?.count ?? 0
  return (
    <li className="min-w-0">
      <BlurFade delay={Math.min(index, 11) * 0.03} className="h-full">
        <SpotlightBorder className="h-full">
          <div
            onClick={(event) => {
              const target = event.target as HTMLElement
              if (!event.currentTarget.contains(target) || target.closest(CONTROL)) return
              onOpen()
            }}
            className="group/choice flex h-full min-w-0 cursor-pointer flex-col rounded-xl"
          >
            <DrawingPreview
              preview={board.preview}
              className="h-48 w-full overflow-hidden rounded-t-xl border-b border-hairline"
            />
            <div className="flex min-w-0 items-start gap-2 px-3.5 py-3">
              <div className="min-w-0 flex-1">
                <button
                  type="button"
                  // The card's own handler already answers the pointer; this
                  // one is for the keyboard and must not open it twice.
                  onClick={(event) => {
                    event.stopPropagation()
                    onOpen()
                  }}
                  className="block max-w-full truncate rounded-sm text-left text-title leading-5 font-semibold focus-ring"
                >
                  {board.name}
                </button>
                <p className="mt-1 truncate text-xs text-muted-foreground">
                  {count > 0 && (
                    <>
                      <span className="numeric">{plural(count, "shape")}</span>
                      {" · "}
                    </>
                  )}
                  {"edited "}
                  {relativeTime(board.updatedAt)}
                </p>
              </div>
              <div className="-mt-1 -mr-1.5 flex shrink-0 items-center">
                {verbs.length > 0 && <VerbMenu verbs={verbs} label={`More for ${board.name}`} />}
                <ArrowRight
                  aria-hidden
                  className="size-3.5 text-muted-foreground transition-colors group-hover/choice:text-foreground"
                />
              </div>
            </div>
            <LinkedLine resources={board.preview?.resources ?? []} linked={linked} />
          </div>
        </SpotlightBorder>
      </BlurFade>
    </li>
  )
}

/**
 * What on this server the drawing has cards for, drawn as each thing is
 * drawn everywhere else: this server as the dashboard's own J, a project as
 * its mark, a database as its engine.
 */
function LinkedLine({ resources, linked }: { resources: BoardResource[]; linked: Linked }) {
  if (resources.length === 0) return null
  const named = resources.map((r) => nameOf(r, linked))
  return (
    <div className="mt-auto flex min-w-0 items-center gap-2 border-t border-hairline px-3.5 py-2 text-xs text-muted-foreground">
      <span className="flex shrink-0 items-center gap-1">
        {resources.slice(0, 5).map((r) => (
          <ResourceGlyph key={`${r.kind}:${r.id ?? ""}`} resource={r} linked={linked} />
        ))}
      </span>
      <span className="min-w-0 truncate">Links {listOf(named)}</span>
    </div>
  )
}

function ResourceGlyph({ resource, linked }: { resource: BoardResource; linked: Linked }) {
  if (resource.kind === "server") {
    return <LogoGlyph small className="h-3.5 w-auto shrink-0 text-brand" />
  }
  if (resource.kind === "project") {
    const project = resource.id === undefined ? undefined : linked.projects.get(resource.id)
    if (project) return <ProjectMark deployment={project} size="xs" />
  }
  if (resource.kind === "database") {
    const database = resource.id === undefined ? undefined : linked.databases.get(resource.id)
    if (database) return <EngineGlyph engine={engineFor(database)} />
  }
  return null
}

function nameOf(resource: BoardResource, linked: Linked) {
  if (resource.kind === "server") return "this server"
  const id = resource.id ?? -1
  const name =
    resource.kind === "project" ? linked.projects.get(id)?.name : linked.databases.get(id)?.name
  return name ?? (resource.kind === "project" ? "a project" : "a database")
}

/** "a, b and 3 more", short enough for one line under a card. */
export function listOf(names: string[]) {
  if (names.length <= 3) {
    return names.length === 1
      ? names[0]
      : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`
  }
  return `${names.slice(0, 2).join(", ")} and ${names.length - 2} more`
}

/**
 * What an empty page shows in place of a drawing: a small one, in Excalidraw's
 * palette and hand, of what the editor is for here — this server, a project
 * it runs and the database under it, joined by arrows.
 */
const SKETCH: BoardPreview = {
  background: "#ffffff",
  count: 9,
  resources: [],
  elements: [
    {
      type: "rectangle",
      x: 0,
      y: 40,
      w: 220,
      h: 110,
      stroke: "#1971c2",
      fill: "#a5d8ff",
      round: true,
    },
    {
      type: "text",
      x: 0,
      y: 77,
      w: 220,
      h: 36,
      text: "this server",
      fontSize: 28,
      fontFamily: 5,
      textAlign: "center",
      stroke: "#1971c2",
    },
    {
      type: "rectangle",
      x: 380,
      y: -40,
      w: 200,
      h: 90,
      stroke: "#2f9e44",
      fill: "#b2f2bb",
      fillStyle: "hachure",
      round: true,
    },
    {
      type: "text",
      x: 380,
      y: -12,
      w: 200,
      h: 34,
      text: "storefront",
      fontSize: 26,
      fontFamily: 5,
      textAlign: "center",
      stroke: "#2f9e44",
    },
    { type: "ellipse", x: 380, y: 140, w: 200, h: 100, stroke: "#e8590c", fill: "#ffd8a8" },
    {
      type: "text",
      x: 380,
      y: 173,
      w: 200,
      h: 34,
      text: "postgres",
      fontSize: 26,
      fontFamily: 5,
      textAlign: "center",
      stroke: "#e8590c",
    },
    {
      type: "arrow",
      x: 228,
      y: 80,
      w: 144,
      h: 76,
      stroke: "#1e1e1e",
      round: true,
      endArrow: true,
      points: [
        [0, 0],
        [70, -50],
        [144, -76],
      ],
    },
    {
      type: "arrow",
      x: 228,
      y: 120,
      w: 144,
      h: 70,
      stroke: "#1e1e1e",
      round: true,
      endArrow: true,
      strokeStyle: "dashed",
      points: [
        [0, 0],
        [72, 50],
        [144, 70],
      ],
    },
    {
      type: "freedraw",
      x: 10,
      y: 196,
      w: 200,
      h: 18,
      stroke: "#e03131",
      strokeWidth: 2,
      points: [
        [0, 6],
        [30, 0],
        [60, 10],
        [95, 2],
        [130, 12],
        [165, 3],
        [200, 9],
      ],
    },
  ],
}
