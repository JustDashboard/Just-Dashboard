"use client"

import { useEffect, useState } from "react"
import { Code, Cross, FloppyDisk, GitMerge } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, put } from "@/lib/api"
import { bytes } from "@/lib/format"
import type { FileContent } from "@/lib/types"
import { CodeEditor } from "@/components/code-editor"
import { DiffView } from "@/components/files/diff-view"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import { PaneHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/** What the preview column is showing: a diff, or a file open for editing. */
export type GitPreview =
  | {
      kind: "diff"
      title: string
      subtitle?: string
      body: string
      /** One file's diff, titled with its name — the renderer can drop git's
       *  header rather than repeat it. */
      singleFile?: boolean
    }
  | { kind: "file"; path: string }

/**
 * The right-hand column of the repo workspace: whatever the operator last
 * clicked. A changed file or a commit shows as a diff; a file picked from the
 * tree opens in place for editing. This is what keeps the whole thing on one
 * screen — the point of the integrated tree is that looking at a file, or its
 * diff, never sends you to another page.
 */
export function PreviewPanel({
  preview,
  canWrite,
  onClose,
  onChanged,
}: {
  preview: GitPreview | null
  canWrite: boolean
  onClose: () => void
  onChanged: () => void
}) {
  if (!preview) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyState
          icon={GitMerge}
          title="Nothing selected"
          description="Click a changed file to see what changed, a commit to see what it did, or a file in the tree to open it here."
        />
      </div>
    )
  }

  if (preview.kind === "diff") {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <PreviewHeader
          icon={GitMerge}
          title={preview.title}
          subtitle={preview.subtitle}
          onClose={onClose}
        />
        <DiffView body={preview.body} singleFile={preview.singleFile} className="min-h-0 flex-1" />
      </div>
    )
  }

  return (
    <FilePreview
      key={preview.path}
      path={preview.path}
      canWrite={canWrite}
      onClose={onClose}
      onChanged={onChanged}
    />
  )
}

function FilePreview({
  path,
  canWrite,
  onClose,
  onChanged,
}: {
  path: string
  canWrite: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const [file, setFile] = useState<FileContent>()
  const [draft, setDraft] = useState("")
  const [error, setError] = useState<Error>()
  const [saving, setSaving] = useState(false)

  // Keyed on the path by the caller, so it mounts fresh per file and this only
  // ever fetches.
  useEffect(() => {
    const controller = new AbortController()
    get<FileContent>("/files/read", { path }, controller.signal)
      .then((f) => {
        setFile(f)
        setDraft(f.content)
      })
      .catch((err) => !controller.signal.aborted && setError(err))
    return () => controller.abort()
  }, [path])

  const dirty = file !== undefined && draft !== file.content

  const save = async () => {
    setSaving(true)
    try {
      await put("/files/write", { path, content: draft })
      notify.success("Saved", { description: path })
      setFile((f) => (f ? { ...f, content: draft } : f))
      // A save can change the working tree, so let the status refresh.
      onChanged()
    } catch (err) {
      notify.error("Could not save", err)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        icon={Code}
        title={path.split("/").pop() ?? path}
        subtitle={path}
        onClose={onClose}
        trailing={
          <>
            {dirty && <Tag tone="warning">unsaved</Tag>}
            {file && !file.binary && canWrite && (
              <Button size="xs" onClick={save} disabled={!dirty || saving} pending={saving}>
                <FloppyDisk className="size-3.5" />
                Save
              </Button>
            )}
          </>
        }
      />
      <div className="min-h-0 flex-1">
        {error && <ErrorState error={error} className="m-3" />}
        {!file && !error && <LoadingRows className="p-3" rows={8} />}
        {file?.binary && (
          <div className="p-4 text-xs text-muted-foreground">
            This is a binary file ({bytes(file.size)}); it is not shown here.
          </div>
        )}
        {file && !file.binary && (
          <CodeEditor
            className="h-full"
            value={draft}
            onChange={setDraft}
            language={file.language}
            readOnly={!canWrite}
          />
        )}
      </div>
    </div>
  )
}

function PreviewHeader({
  icon: Icon,
  title,
  subtitle,
  trailing,
  onClose,
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  subtitle?: string
  trailing?: React.ReactNode
  onClose: () => void
}) {
  return (
    <PaneHeader className="gap-2 px-3">
      <Icon className="size-3.5 shrink-0 text-primary" />
      <div className="min-w-0 flex-1">
        <p className="truncate font-mono text-body" title={title}>
          {title}
        </p>
        {subtitle && (
          <p className="truncate text-hint text-muted-foreground" title={subtitle}>
            {subtitle}
          </p>
        )}
      </div>
      {trailing}
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            size="sm"
            variant="ghost"
            className="size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground"
            aria-label="Close"
            onClick={onClose}
          >
            <Cross className="size-4" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Close the preview</TooltipContent>
      </Tooltip>
    </PaneHeader>
  )
}
