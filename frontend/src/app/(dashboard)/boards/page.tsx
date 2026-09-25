"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Layout, Plus } from "@/components/icons"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { get, post } from "@/lib/api"
import type { Board, BoardSummary } from "@/lib/boards"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"

export default function BoardsPage() {
  const router = useRouter()
  const { can } = useAuth()
  const [boards, setBoards] = useState<BoardSummary[] | null>(null)
  const [error, setError] = useState<Error | null>(null)
  const [creating, setCreating] = useState(false)

  useEffect(() => {
    let cancelled = false
    get<BoardSummary[]>("/boards/")
      .then((list) => {
        if (!cancelled) setBoards(list)
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause : new Error(String(cause)))
      })
    return () => {
      cancelled = true
    }
  }, [])

  async function create() {
    setCreating(true)
    try {
      const board = await post<Board>("/boards/", { name: "Untitled board" })
      router.push(`/boards/${board.id}`)
    } catch (cause) {
      notify.error("Could not create board", cause)
      setCreating(false)
    }
  }

  return (
    <Page>
      <PageContext
        title="Boards"
        actions={
          can("service.control") && (
            <Button onClick={create} pending={creating}>
              <Plus /> New board
            </Button>
          )
        }
      />
      <div className="space-y-1">
        <h2 className="text-xl font-semibold text-foreground">Boards</h2>
        <p className="text-sm text-muted-foreground">
          Sketch your server, keep notes, and link projects and databases on a shared canvas.
        </p>
      </div>
      {error && !boards ? (
        <ErrorState error={error} />
      ) : !boards ? (
        <LoadingPanel />
      ) : boards.length === 0 ? (
        <EmptyState
          icon={Layout}
          title="No boards yet"
          description="Create a board to map an idea or document how this server fits together."
          action={can("service.control") ? <Button onClick={create}>New board</Button> : undefined}
        />
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {boards.map((board) => (
            <Link
              key={board.id}
              href={`/boards/${board.id}`}
              className="group flex min-h-32 flex-col justify-between rounded-xl border border-border bg-card p-5 focus-ring transition-colors hover:bg-control-hover"
            >
              <div className="flex items-center gap-3">
                <Layout className="size-5 text-brand" />
                <span className="min-w-0 truncate font-medium text-foreground">{board.name}</span>
              </div>
              <span className="text-xs text-muted-foreground">
                Edited {relativeTime(board.updatedAt)}
              </span>
            </Link>
          ))}
        </div>
      )}
    </Page>
  )
}
