"use client"

import { useEffect, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { ArrowRight, Copy, Pencil, Plus, Trash } from "@/components/icons"
import { ApiError, del, get, patch, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import {
  previewOf,
  TEMPLATES,
  type StickyBoard,
  type StickySummary,
  type Template,
} from "@/lib/stickyboard"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { useConfirm } from "@/components/confirm-dialog"
import { CONTROL } from "@/components/flow"
import { Field } from "@/components/form"
import { Modal } from "@/components/modal"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput, Section } from "@/components/page"
import { EmptyState, ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { BlurFade } from "@/components/ui/blur-fade"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { VerbMenu, type Verb } from "@/components/verbs"
import { BoardPreview } from "@/components/stickyboard/board-preview"
import { StickyboardMark } from "@/components/stickyboard/glyphs"

/** As many columns as the width holds at twenty rems a card, one on a phone. */
const GRID = "grid min-w-0 gap-3 grid-cols-[repeat(auto-fill,minmax(min(20rem,100%),1fr))]"

/** A name filter earns its row once there are more boards than one screen shows. */
const SEARCH_FROM = 7

/**
 * Every stickyboard on this server, as cards drawn as the boards themselves.
 *
 * A reading page (§16). It opens the way the Overview opens on the machine:
 * the product as its own mark, with a line of facts where four stat tiles
 * stood. Every card is a board to enter, so each carries the lit edge of a
 * thing you take, and its picture is the board with what its notes say
 * written on them. Under the boards are the ways to start another — the
 * templates are choices between kinds, so they are cards, each drawn as the
 * board it makes, and taking one opens New board on it.
 */
export function BoardList() {
  const router = useRouter()
  const { can } = useAuth()
  const canWrite = can("service.control")
  const canDelete = can("destructive")
  const { confirm, dialog } = useConfirm()
  const [boards, setBoards] = useState<StickySummary[] | null>(null)
  const [error, setError] = useState<Error | null>(null)
  const [query, setQuery] = useState("")
  const [creating, setCreating] = useState<Template | null>(null)
  const [renaming, setRenaming] = useState<StickySummary | null>(null)

  useEffect(() => {
    let cancelled = false
    get<StickySummary[]>("/stickyboards/")
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

  async function duplicate(board: StickySummary) {
    try {
      const copy = await post<StickyBoard>(`/stickyboards/${board.id}/duplicate`, {
        name: `${board.name} copy`.slice(0, 100),
      })
      setBoards((list) => (list ? [copy, ...list] : [copy]))
      notify.success(`Duplicated “${board.name}”`)
    } catch (cause) {
      notify.error("Could not duplicate the board", cause)
    }
  }

  function remove(board: StickySummary) {
    confirm({
      title: "Delete board",
      description: `Delete “${board.name}” with its ${plural(board.notes, "note")} and ${plural(board.links, "link")}? This cannot be undone.`,
      subject: {
        mark: (
          <BoardPreview
            preview={board.preview}
            className="h-10 w-16 rounded-md border bg-background"
          />
        ),
        name: board.name,
      },
      confirmLabel: "Delete board",
      action: async () => {
        try {
          await del(`/stickyboards/${board.id}`)
        } catch (cause) {
          if (!(cause instanceof ApiError && cause.code === "stickyboard_not_found")) throw cause
        }
        setBoards((list) => list?.filter((b) => b.id !== board.id) ?? null)
      },
    })
  }

  return (
    <Page>
      <PageContext title="Stickyboard" />
      {error && !boards ? (
        <ErrorState error={error} />
      ) : !boards ? (
        <ListSkeleton />
      ) : (
        <>
          <Identity
            boards={boards}
            actions={
              canWrite && (
                <Button onClick={() => setCreating(TEMPLATES[0])}>
                  <Plus /> New board
                </Button>
              )
            }
          />
          {boards.length === 0 ? (
            <EmptyState
              mark={<StickyboardMark className="size-12" />}
              title="Put up your first board"
              description="A board is an endless wall for sticky notes: write, move, colour and link them, zoomed out to see everything or in to read one."
              action={
                canWrite ? (
                  <Button onClick={() => setCreating(TEMPLATES[0])}>New board</Button>
                ) : undefined
              }
            />
          ) : (
            <Section
              title="Boards"
              actions={
                boards.length >= SEARCH_FROM && (
                  <SearchInput
                    dense
                    value={query}
                    placeholder="Find a board"
                    aria-label="Find a board"
                    onChange={(event) => setQuery(event.target.value)}
                    containerClassName="w-full sm:w-64"
                  />
                )
              }
            >
              {shown.length === 0 ? (
                <EmptyState
                  title="No board matches"
                  description={`Nothing is called “${query.trim()}”.`}
                />
              ) : (
                <ul aria-label="Stickyboards" className={GRID}>
                  {shown.map((board, index) => (
                    <BoardCard
                      key={board.id}
                      board={board}
                      index={index}
                      onOpen={() => router.push(`/stickyboard/${board.id}`)}
                      verbs={[
                        ...(canWrite
                          ? [
                              {
                                key: "rename",
                                label: "Rename",
                                icon: Pencil,
                                run: () => setRenaming(board),
                              },
                              {
                                key: "duplicate",
                                label: "Duplicate",
                                icon: Copy,
                                run: () => void duplicate(board),
                              },
                            ]
                          : []),
                        ...(canDelete
                          ? [
                              {
                                key: "delete",
                                label: "Delete",
                                icon: Trash,
                                danger: true,
                                run: () => remove(board),
                              },
                            ]
                          : []),
                      ]}
                    />
                  ))}
                </ul>
              )}
            </Section>
          )}
          {canWrite && <StartFrom onPick={setCreating} />}
        </>
      )}
      {creating && (
        <CreateDialog
          initial={creating}
          onClose={() => setCreating(null)}
          onCreated={(board) => router.push(`/stickyboard/${board.id}`)}
        />
      )}
      {renaming && (
        <RenameDialog
          board={renaming}
          onClose={() => setRenaming(null)}
          onRenamed={(saved) =>
            setBoards((list) => list?.map((b) => (b.id === saved.id ? saved : b)) ?? null)
          }
        />
      )}
      {dialog}
    </Page>
  )
}

/** The product the page is, and what it holds, in the line the Overview gives the machine. */
function Identity({ boards, actions }: { boards: StickySummary[]; actions: React.ReactNode }) {
  const totals = boards.reduce(
    (sum, b) => ({ notes: sum.notes + b.notes, links: sum.links + b.links }),
    { notes: 0, links: 0 },
  )
  const latest = boards[0]
  return (
    <HostIdentity
      logo={
        <span className="flex size-12 shrink-0 items-center justify-center rounded-xl border border-hairline bg-background">
          <StickyboardMark className="size-8" />
        </span>
      }
      title="Stickyboard"
      aside={actions}
      facts={
        <>
          <span className="numeric">{plural(boards.length, "board")}</span>
          <FactDot />
          <span className="numeric">{plural(totals.notes, "sticky note")}</span>
          <FactDot />
          <span className="numeric">{plural(totals.links, "link")}</span>
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
            <Skeleton className="h-62 rounded-xl" />
          </li>
        ))}
      </ul>
    </>
  )
}

/**
 * The four starting points, drawn as the boards they make. Built once, so a
 * card's drawing is the board New board then creates from it.
 */
function StartFrom({ onPick }: { onPick: (template: Template) => void }) {
  const previews = useMemo(() => TEMPLATES.map((t) => previewOf(t.build())), [])
  return (
    <Section title="Start from">
      <ChoiceGrid columns="fill">
        {TEMPLATES.map((template, index) => (
          <ChoiceCard
            key={template.id}
            index={index}
            onClick={() => onPick(template)}
            aria-label={`New ${template.name.toLowerCase()} board`}
            className="gap-2"
          >
            <BoardPreview
              preview={previews[index]}
              className="h-28 w-full rounded-md border border-hairline bg-background"
            />
            <ChoiceCardTitle>{template.name}</ChoiceCardTitle>
            <ChoiceCardHint>{template.hint}</ChoiceCardHint>
          </ChoiceCard>
        ))}
      </ChoiceGrid>
    </Section>
  )
}

function BoardCard({
  board,
  index,
  onOpen,
  verbs,
}: {
  board: StickySummary
  index: number
  onOpen: () => void
  verbs: Verb[]
}) {
  // The colours the board is written in, most used first: a wall of pink
  // questions and yellow tasks says which it is before its name does.
  const colours = useMemo(() => {
    const used = new Map<string, number>()
    for (const note of board.preview.notes) {
      if (note.kind === "sticky") used.set(note.color, (used.get(note.color) ?? 0) + 1)
    }
    return [...used.entries()].sort((a, b) => b[1] - a[1]).map(([colour]) => colour)
  }, [board.preview])
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
            <BoardPreview
              preview={board.preview}
              className="h-44 w-full rounded-t-xl border-b border-hairline bg-background"
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
                <p className="mt-1 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                  {colours.length > 0 && (
                    <span className="flex shrink-0 items-center -space-x-0.5">
                      {colours.slice(0, 5).map((colour) => (
                        <span
                          key={colour}
                          data-note-color={colour}
                          className="size-2.5 rounded-sm border border-(--note-edge) bg-(--note)"
                        />
                      ))}
                    </span>
                  )}
                  <span className="min-w-0 truncate">
                    <span className="numeric">{plural(board.notes, "note")}</span>
                    {" · "}
                    <span className="numeric">{plural(board.links, "link")}</span>
                    {" · edited "}
                    {relativeTime(board.updatedAt)}
                  </span>
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
          </div>
        </SpotlightBorder>
      </BlurFade>
    </li>
  )
}

function CreateDialog({
  initial,
  onClose,
  onCreated,
}: {
  initial: Template
  onClose: () => void
  onCreated: (board: StickyBoard) => void
}) {
  const [name, setName] = useState("")
  const [template, setTemplate] = useState<Template>(initial)
  const [pending, setPending] = useState(false)
  // Built once while the dialog is open, so each card's drawing and the board
  // created from it are the same board.
  const built = useMemo(() => new Map(TEMPLATES.map((t) => [t.id, t.build()])), [])
  const title = name.trim() || (template.id === "blank" ? "Untitled board" : template.name)

  async function create() {
    setPending(true)
    try {
      const board = await post<StickyBoard>("/stickyboards/", {
        name: title,
        document: built.get(template.id),
      })
      onCreated(board)
    } catch (cause) {
      notify.error("Could not create the board", cause)
      setPending(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title="New board"
      size="lg"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => void create()} pending={pending}>
            Create board
          </Button>
        </>
      }
    >
      <form
        className="space-y-5"
        onSubmit={(event) => {
          event.preventDefault()
          void create()
        }}
      >
        <Field label="Name" htmlFor="sticky-name">
          <Input
            id="sticky-name"
            value={name}
            maxLength={100}
            placeholder={template.id === "blank" ? "Untitled board" : template.name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <div className="space-y-2" role="group" aria-label="Start from">
          <p className="text-body font-medium">Start from</p>
          <ChoiceGrid columns={2}>
            {TEMPLATES.map((option, index) => (
              <ChoiceCard
                key={option.id}
                index={index}
                selected={option.id === template.id}
                onClick={() => setTemplate(option)}
                className="gap-2"
              >
                <BoardPreview
                  preview={previewOf(built.get(option.id)!)}
                  className="h-24 w-full rounded-md border border-hairline bg-background"
                />
                <ChoiceCardTitle>{option.name}</ChoiceCardTitle>
                <ChoiceCardHint>{option.hint}</ChoiceCardHint>
              </ChoiceCard>
            ))}
          </ChoiceGrid>
        </div>
      </form>
    </Modal>
  )
}

function RenameDialog({
  board,
  onClose,
  onRenamed,
}: {
  board: StickySummary
  onClose: () => void
  onRenamed: (board: StickySummary) => void
}) {
  const [name, setName] = useState(board.name)
  const [pending, setPending] = useState(false)
  const trimmed = name.trim()

  async function save() {
    if (!trimmed) return
    setPending(true)
    try {
      onRenamed(await patch<StickySummary>(`/stickyboards/${board.id}`, { name: trimmed }))
      onClose()
    } catch (cause) {
      notify.error("Could not rename the board", cause)
      setPending(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title="Rename board"
      size="sm"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => void save()} pending={pending} disabled={!trimmed}>
            Rename
          </Button>
        </>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        <Field
          label="Name"
          htmlFor="sticky-rename"
          error={!trimmed ? "A board needs a name" : undefined}
        >
          <Input
            id="sticky-rename"
            value={name}
            maxLength={100}
            autoFocus
            onFocus={(event) => event.currentTarget.select()}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
      </form>
    </Modal>
  )
}
