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
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { BlurFade } from "@/components/ui/blur-fade"
import { Input } from "@/components/ui/input"
import { NumberTicker } from "@/components/ui/number-ticker"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { VerbMenu, type Verb } from "@/components/verbs"
import { BoardPreview } from "@/components/stickyboard/board-preview"
import { StickyGlyph } from "@/components/stickyboard/glyphs"

/** As many columns as the width holds at twenty rems a card, one on a phone. */
const GRID = "grid min-w-0 gap-3 grid-cols-[repeat(auto-fill,minmax(min(20rem,100%),1fr))]"

/**
 * Every stickyboard on this server, as cards drawn as the boards themselves.
 *
 * A reading page (§16): it opens on what the boards hold, as figures, and
 * every card is a board to enter, so each carries the lit edge of a thing you
 * take. New board asks for a name and a starting point — the templates are
 * choices between kinds, so they are cards, each drawn as the board it makes.
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
  const [creating, setCreating] = useState(false)
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

  const totals = useMemo(
    () =>
      (boards ?? []).reduce(
        (sum, b) => ({ notes: sum.notes + b.notes, links: sum.links + b.links }),
        { notes: 0, links: 0 },
      ),
    [boards],
  )

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

  const latest = boards?.[0]

  return (
    <Page>
      <PageContext
        title="Stickyboard"
        actions={
          canWrite && (
            <Button onClick={() => setCreating(true)}>
              <Plus /> New board
            </Button>
          )
        }
      />
      {error && !boards ? (
        <ErrorState error={error} />
      ) : !boards ? (
        <LoadingPanel />
      ) : boards.length === 0 ? (
        <EmptyState
          mark={
            <span
              data-note-color="yellow"
              className="flex size-10 items-center justify-center rounded-md border border-(--note-edge) bg-(--note) text-(--note-ink)"
            >
              <StickyGlyph className="size-5" />
            </span>
          }
          title="No stickyboards yet"
          description="A board is an endless wall for sticky notes: write, move, colour and link them, zoomed out to see everything or in to read one."
          action={
            canWrite ? <Button onClick={() => setCreating(true)}>New board</Button> : undefined
          }
        />
      ) : (
        <>
          <StatGrid columns={4} dense>
            <StatTile label="Boards" value={<NumberTicker value={boards.length} />} />
            <StatTile label="Sticky notes" value={<NumberTicker value={totals.notes} />} />
            <StatTile label="Links" value={<NumberTicker value={totals.links} />} />
            <StatTile
              label="Last edited"
              value={latest ? relativeTime(latest.updatedAt) : "—"}
              hint={latest?.name}
            />
          </StatGrid>
          <div className="flex min-w-0 flex-col gap-4">
            <Toolbar>
              <SearchInput
                value={query}
                placeholder="Find a board"
                aria-label="Find a board"
                onChange={(event) => setQuery(event.target.value)}
              />
              <span className="ml-auto text-hint text-muted-foreground">
                {query.trim()
                  ? `${shown.length} of ${plural(boards.length, "board")}`
                  : plural(boards.length, "board")}
              </span>
            </Toolbar>
            {shown.length === 0 ? (
              <EmptyState
                title="No board matches"
                description={`Nothing is called “${query.trim()}”.`}
              />
            ) : (
              <ul className={GRID}>
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
          </div>
        </>
      )}
      {creating && (
        <CreateDialog
          onClose={() => setCreating(false)}
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
  return (
    <li className="min-w-0">
      <BlurFade delay={Math.min(index, 11) * 0.03} className="h-full">
        <SpotlightBorder radius={320} className="h-full">
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
              className="h-40 w-full rounded-t-xl border-b border-hairline bg-background"
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
                  <span className="numeric">{plural(board.notes, "note")}</span>
                  {" · "}
                  <span className="numeric">{plural(board.links, "link")}</span>
                  {" · edited "}
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
          </div>
        </SpotlightBorder>
      </BlurFade>
    </li>
  )
}

function CreateDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void
  onCreated: (board: StickyBoard) => void
}) {
  const [name, setName] = useState("")
  const [template, setTemplate] = useState<Template>(TEMPLATES[0])
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
