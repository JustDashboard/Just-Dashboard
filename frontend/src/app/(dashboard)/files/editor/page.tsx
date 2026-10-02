"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { ArrowLeft, MagnifyingGlass, SidebarLeftClose, SidebarLeftOpen } from "@/components/icons"
import { Page } from "@/components/page"
import { PaneHeader } from "@/components/panel"
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
  const [mobileTree, setMobileTree] = useState(false)
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
      if (event.key === "Escape") setMobileTree(false)
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
    <Page fill className="max-w-none gap-2 px-2 py-2 md:px-3 md:py-3">
      {/* A frame is needed because both the tree and editor own their scrolling. */}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-xl border bg-card">
        <PaneHeader className="flex-wrap gap-2 px-2 py-1.5">
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
            className="hidden size-8 md:inline-flex"
            aria-pressed={sidebar}
            onClick={() => setSidebar(!sidebar)}
          >
            {sidebar ? <SidebarLeftClose /> : <SidebarLeftOpen />}
          </IconAction>
          <IconAction
            label={mobileTree ? "Hide file tree" : "Show file tree"}
            className="size-8 md:hidden"
            aria-expanded={mobileTree}
            onClick={() => setMobileTree(!mobileTree)}
          >
            {mobileTree ? <SidebarLeftClose /> : <SidebarLeftOpen />}
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
            onClick={() => {
              setSearchMode("names")
              setFind(true)
            }}
          >
            <MagnifyingGlass className="size-4" />
            Find file
          </Button>
        </PaneHeader>
        <div className="relative flex min-h-0 min-w-0 flex-1">
          {mobileTree && (
            <button
              type="button"
              aria-label="Close file tree"
              className="absolute inset-0 z-10 bg-background/70 md:hidden"
              onClick={() => setMobileTree(false)}
            />
          )}
          {(sidebar || mobileTree) && (
            <aside
              aria-label="File explorer"
              className={cn(
                "absolute inset-y-0 left-0 z-20 w-56 shrink-0 flex-col border-r border-hairline bg-card md:static md:z-auto md:w-64",
                mobileTree ? "flex" : "hidden",
                sidebar ? "md:flex" : "md:hidden",
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
                  setMobileTree(false)
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
