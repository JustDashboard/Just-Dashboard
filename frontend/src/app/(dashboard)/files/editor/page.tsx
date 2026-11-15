"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import {
  ArrowLeft,
  FolderOpen,
  MagnifyingGlass,
  Pencil,
  SidebarLeftClose,
  SidebarLeftOpen,
} from "@/components/icons"
import { Page } from "@/components/page"
import { PaneHeader } from "@/components/panel"
import { PaneBar, paneShown, type PaneStop } from "@/components/phone-panes"
import { Button } from "@/components/ui/button"
import { IconAction } from "@/components/icon-action"
import { FileTree } from "@/components/files/file-tree"
import { FileEditorSheet } from "@/components/files/file-editor"
import { ImageEditor } from "@/components/files/image-editor"
import { QuickOpen, type FileSearchMode } from "@/components/files/quick-open"
import { cleanPath, isImage, parentOf } from "@/components/files/media"
import { editorHref } from "@/components/files/search"
import { useAuth } from "@/hooks/use-auth"
import { useViewState } from "@/lib/view-state"
import { useConfirm } from "@/components/confirm-dialog"
import { EmptyState } from "@/components/state"
import { cn } from "@/lib/utils"

type EditorPane = "tree" | "editor"

const PANES: PaneStop<EditorPane>[] = [
  { key: "tree", label: "Files", icon: <FolderOpen /> },
  { key: "editor", label: "Editor", icon: <Pencil /> },
]

/** Reading register: a scrolling tree and editor are working panes; counts live in their status strips. */
export default function FileEditorPage() {
  const params = useSearchParams()
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const path = params.get("path")
  const requestedRoot = cleanPath(params.get("root") || (path ? parentOf(path) : "/"))
  const root =
    path && requestedRoot !== "/" && !path.startsWith(requestedRoot + "/")
      ? parentOf(path)
      : requestedRoot
  const line = Number(params.get("line")) || undefined
  const [sidebar, setSidebar] = useViewState("files.fullEditor.sidebar", true)
  // Which pane a phone shows. Both stay mounted while the other is looked
  // at: the editor that is out of sight still holds its unsaved draft.
  const [pane, setPane] = useState<EditorPane>("editor")
  const [find, setFind] = useState(false)
  const [searchMode, setSearchMode] = useState<FileSearchMode>("names")
  const [refresh, setRefresh] = useState(0)
  const dirty = useRef(false)
  const setDirty = useCallback((value: boolean) => {
    dirty.current = value
  }, [])
  const requestNavigate = useCallback(
    (href: string) => {
      if (new URL(href, window.location.href).href === window.location.href) return
      const navigate = () => {
        dirty.current = false
        router.push(href)
      }
      if (!dirty.current) return navigate()
      confirm({
        title: "Leave without saving?",
        description: <p>The current file has changes that have not been written to disk.</p>,
        confirmLabel: "Discard and leave",
        action: async () => navigate(),
      })
    },
    [confirm, router],
  )

  useEffect(() => {
    const keyboard = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey)) return
      if (event.key.toLowerCase() === "p" || (event.shiftKey && event.key.toLowerCase() === "f")) {
        event.preventDefault()
        event.stopPropagation()
        setSearchMode(event.shiftKey ? "content" : "names")
        setFind(true)
      }
    }
    window.addEventListener("keydown", keyboard, true)
    return () => window.removeEventListener("keydown", keyboard, true)
  }, [])

  useEffect(() => {
    const navigation = (
      window as Window & {
        navigation?: EventTarget & { traverseTo: (key: string) => unknown }
      }
    ).navigation
    const traverse = (event: Event) => {
      const move = event as Event & {
        navigationType: string
        destination: { key: string; url: string }
      }
      if (!dirty.current || move.navigationType !== "traverse" || !move.cancelable) return
      move.preventDefault()
      confirm({
        title: "Leave without saving?",
        description: <p>The current file has changes that have not been written to disk.</p>,
        confirmLabel: "Discard and leave",
        action: async () => {
          dirty.current = false
          navigation?.traverseTo(move.destination.key)
        },
      })
    }
    const unload = (event: BeforeUnloadEvent) => {
      if (!dirty.current) return
      event.preventDefault()
      event.returnValue = ""
    }
    const click = (event: MouseEvent) => {
      if (
        !dirty.current ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey
      )
        return
      const link = (event.target as Element | null)?.closest("a")
      if (!link || link.target === "_blank" || link.hasAttribute("download")) return
      const href = link.getAttribute("href")
      if (
        !href ||
        href.startsWith("#") ||
        href === window.location.pathname + window.location.search
      )
        return
      event.preventDefault()
      event.stopImmediatePropagation()
      requestNavigate(href)
    }
    window.addEventListener("beforeunload", unload)
    navigation?.addEventListener("navigate", traverse)
    document.addEventListener("click", click, true)
    return () => {
      window.removeEventListener("beforeunload", unload)
      navigation?.removeEventListener("navigate", traverse)
      document.removeEventListener("click", click, true)
    }
  }, [confirm, requestNavigate])

  if (!path)
    return (
      <Page>
        <EmptyState
          title="Choose a file to edit"
          action={<Button onClick={() => router.push("/files")}>Browse files</Button>}
        />
      </Page>
    )

  return (
    <Page fill className="max-w-none gap-2 p-0 md:px-0 md:py-0 lg:px-3 lg:py-3">
      {/* A frame is needed because both the tree and editor own their scrolling.
          Below `lg` it runs to the screen's edges and shows one of the two,
          picked on the bar at its foot: beside each other on a phone the tree
          was a drawer over the code, opened from a glyph in the strip. */}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-card lg:rounded-xl lg:border">
        <PaneHeader className="gap-2 px-2 py-1.5">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => requestNavigate("/files?path=" + encodeURIComponent(parentOf(path)))}
          >
            <ArrowLeft className="size-4" />
            Files
          </Button>
          <IconAction
            label={sidebar ? "Hide file tree" : "Show file tree"}
            className="hidden size-8 lg:inline-flex"
            aria-pressed={sidebar}
            onClick={() => setSidebar(!sidebar)}
          >
            {sidebar ? <SidebarLeftClose /> : <SidebarLeftOpen />}
          </IconAction>
          <span
            className="min-w-0 flex-1 truncate font-mono text-hint text-muted-foreground"
            title={root}
          >
            {root}
          </span>
          <Button
            variant="outline"
            size="sm"
            className="shrink-0"
            onClick={() => {
              setSearchMode("names")
              setFind(true)
            }}
          >
            <MagnifyingGlass className="size-4" />
            Find file
          </Button>
        </PaneHeader>
        <div className="flex min-h-0 min-w-0 flex-1">
          {/* Hidden at a desk is a desk's choice: the bar reaches the tree
              whether or not the column was put away. */}
          {(sidebar || pane === "tree") && (
            <aside
              aria-label="File explorer"
              className={cn(
                "flex shrink-0 flex-col border-hairline max-lg:min-h-0 max-lg:min-w-0 max-lg:flex-1 lg:w-64 lg:border-r",
                !sidebar && "lg:hidden",
                paneShown(pane === "tree"),
              )}
            >
              <FileTree
                key={root}
                root={root}
                statusMap={{}}
                activeFile={path}
                canWrite={can("file.write")}
                canDelete={can("destructive")}
                onOpenFile={(next) => {
                  // Choosing a file is asking to see it, the open one included.
                  setPane("editor")
                  if (next !== path) requestNavigate(editorHref(next, root))
                }}
                onConfirm={(request) =>
                  confirm({
                    title: request.title,
                    description: request.body,
                    confirmLabel: request.confirmLabel,
                    action: async () => {
                      await request.run()
                    },
                  })
                }
                onChanged={() => setRefresh((v) => v + 1)}
                refreshTick={refresh}
                onOpenInFiles={(dir) => requestNavigate("/files?path=" + encodeURIComponent(dir))}
              />
            </aside>
          )}
          <div className={cn("flex min-h-0 min-w-0 flex-1", paneShown(pane === "editor"))}>
            {isImage(path) && can("file.write") ? (
              <ImageEditor
                key={params.toString()}
                path={path}
                root={root}
                destination
                onDirtyChange={setDirty}
                onClose={() => undefined}
                onSaved={() => {
                  setRefresh((v) => v + 1)
                }}
              />
            ) : (
              <FileEditorSheet
                key={params.toString()}
                grouped
                path={path}
                root={root}
                destination
                revealLine={line}
                onDirtyChange={setDirty}
                onOpenChange={() => undefined}
                onSaved={() => setRefresh((v) => v + 1)}
              />
            )}
          </div>
        </div>
        <PaneBar label="File editor" stops={PANES} active={pane} onPick={setPane} />
      </div>
      <QuickOpen
        open={find}
        onOpenChange={setFind}
        root={root}
        initialMode={searchMode}
        onOpenPath={(next, isDir, matchLine) =>
          requestNavigate(
            isDir ? "/files?path=" + encodeURIComponent(next) : editorHref(next, root, matchLine),
          )
        }
      />
      {dialog}
    </Page>
  )
}
