"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { get, post, errorMessage } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { GitResult } from "@/lib/types"
import type { PreviewContext } from "@/components/git/preview-panel"
import { PreviewHeader } from "@/components/git/preview-header"
import { Field } from "@/components/form"
import { ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { FilterChip } from "@/components/tabs"
import { DiffView } from "@/components/files/diff-view"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Submodule = {
  name: string
  path: string
  url: string
  head?: string
  expected?: string
  initialized: boolean
  dirty: boolean
}
export function SubmodulePreview({ ctx, onClose }: { ctx: PreviewContext; onClose: () => void }) {
  const router = useRouter()
  const rows = usePoll(
    (signal) => get<Submodule[]>("/git/submodules", { path: ctx.repoPath }, signal),
    0,
    [ctx.repoPath],
  )
  const [path, setPath] = useState("")
  const [url, setURL] = useState("")
  const act = async (action: string, path: string, url?: string) => {
    try {
      await ctx.run("Submodule updated", () =>
        post<GitResult>(
          ["remove", "deinit"].includes(action) ? "/git/submodule/remove" : "/git/submodule",
          { action, path, url },
          { query: { path: ctx.repoPath } },
        ),
      )
    } finally {
      rows.refresh()
    }
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        title="Submodules"
        mono={false}
        subtitle="Repositories pinned inside this checkout"
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 overflow-auto">
        {rows.error && <ErrorState error={rows.error} className="m-3" />}
        {rows.loading && <LoadingRows className="p-3" rows={3} />}
        {rows.data?.length === 0 && (
          <p className="p-3 text-hint text-muted-foreground">No submodules.</p>
        )}
        <ul className="divide-y divide-hairline">
          {rows.data?.map((r) => (
            <li key={r.path} className="space-y-2 p-3">
              <p className="font-mono text-xs break-all">{r.path}</p>
              <p className="text-hint break-all text-muted-foreground">{r.url}</p>
              <p className="text-hint text-muted-foreground">
                {r.initialized
                  ? r.dirty
                    ? "Uncommitted changes"
                    : r.head === r.expected
                      ? "At the recorded commit"
                      : "Different from the recorded commit"
                  : "Not initialized"}
                {r.head ? ` · ${r.head.slice(0, 7)}` : ""}
              </p>
              <div className="flex flex-wrap gap-1">
                {r.initialized && (
                  <Button
                    size="xs"
                    variant="ghost"
                    onClick={() =>
                      router.push(`/git?repo=${encodeURIComponent(`${ctx.repoPath}/${r.path}`)}`)
                    }
                  >
                    Open in Git
                  </Button>
                )}
                {ctx.canControl && (
                  <>
                    <Button
                      size="xs"
                      variant="outline"
                      disabled={!!ctx.busy || r.dirty}
                      onClick={() => void act("update", r.path).catch(() => undefined)}
                    >
                      {r.initialized ? "Update to recorded commit" : "Initialize"}
                    </Button>
                    <Button
                      size="xs"
                      variant="ghost"
                      disabled={!!ctx.busy}
                      onClick={() => void act("sync", r.path).catch(() => undefined)}
                    >
                      Sync URL
                    </Button>
                  </>
                )}
                {ctx.canDestruct &&
                  ["deinit", "remove"].map((action) => (
                    <Button
                      key={action}
                      size="xs"
                      variant="ghost"
                      disabled={!!ctx.busy || r.dirty || (action === "deinit" && !r.initialized)}
                      onClick={() =>
                        ctx.confirm({
                          title: `${action === "deinit" ? "Deinitialize" : "Remove"} ${r.path}?`,
                          description:
                            action === "deinit"
                              ? "Remove this clean working copy, retaining its Git data and registration for initialization later."
                              : "Stage removal of the submodule and its registration. Its Git data remains available for recovery.",
                          action: () => act(action, r.path),
                        })
                      }
                    >
                      {action === "deinit" ? "Deinitialize" : "Remove"}
                    </Button>
                  ))}
              </div>
            </li>
          ))}
        </ul>
        {ctx.canControl && (
          <form
            className="space-y-3 border-t border-hairline p-3"
            onSubmit={(e) => {
              e.preventDefault()
              void act("add", path, url).catch(() => undefined)
            }}
          >
            <Field label="Repository URL">
              <Input
                aria-label="Submodule URL"
                value={url}
                onChange={(e) => setURL(e.target.value)}
                placeholder="https://github.com/owner/library.git"
              />
            </Field>
            <Field label="Directory inside this repository">
              <Input
                aria-label="Submodule directory"
                value={path}
                onChange={(e) => setPath(e.target.value)}
                placeholder="vendor/library"
              />
            </Field>
            <Button
              size="sm"
              variant="outline"
              disabled={!!ctx.busy || !url.trim() || !path.trim()}
            >
              Add submodule
            </Button>
          </form>
        )}
      </div>
    </div>
  )
}

export function LFSPreview({ ctx, onClose }: { ctx: PreviewContext; onClose: () => void }) {
  const data = usePoll(
    (signal) =>
      get<{
        available: boolean
        version?: string
        status?: string
        files?: string
        patterns?: string
      }>("/git/lfs", { path: ctx.repoPath }, signal),
    0,
    [ctx.repoPath],
  )
  const [pattern, setPattern] = useState("")
  const act = async (action: string) => {
    try {
      await ctx.run("Git LFS updated", () =>
        post<GitResult>("/git/lfs", { action, pattern }, { query: { path: ctx.repoPath } }),
      )
      data.refresh()
    } catch {
      data.refresh()
    }
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        title="Git LFS"
        mono={false}
        subtitle={data.data?.version || "Large file storage"}
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 space-y-3 overflow-auto p-3">
        {data.error && <ErrorState error={data.error} />}
        {data.loading && <LoadingRows rows={3} />}
        {data.data && !data.data.available && (
          <p className="text-hint text-muted-foreground">
            Git LFS is not installed in this runtime. Install git-lfs to manage large files here.
          </p>
        )}
        {data.data?.available && (
          <>
            {ctx.canControl && ctx.canWrite && (
              <div className="flex flex-wrap gap-2">
                <Button
                  size="xs"
                  variant="outline"
                  disabled={!!ctx.busy}
                  onClick={() => void act("install")}
                >
                  Enable for this repository
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  disabled={!!ctx.busy}
                  onClick={() => void act("fetch")}
                >
                  Fetch objects
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  disabled={!!ctx.busy}
                  onClick={() => void act("pull")}
                >
                  Download working files
                </Button>
              </div>
            )}
            <p className="eyebrow">Status</p>
            <pre className="overflow-auto font-mono text-hint">
              {data.data.status || "No pending LFS changes."}
            </pre>
            <p className="eyebrow">Tracked patterns</p>
            <pre className="overflow-auto font-mono text-hint">
              {data.data.patterns || "No patterns."}
            </pre>
            <p className="eyebrow">Files</p>
            <pre className="overflow-auto font-mono text-hint">
              {data.data.files || "No LFS files."}
            </pre>
            {ctx.canControl && ctx.canWrite && (
              <div className="space-y-3 border-t border-hairline pt-3">
                <Field label="File pattern">
                  <Input
                    aria-label="LFS file pattern"
                    value={pattern}
                    onChange={(e) => setPattern(e.target.value)}
                    placeholder="*.psd"
                  />
                </Field>
                <p className="text-hint text-muted-foreground">
                  Tracking changes .gitattributes. Stage it with your files in Changes; existing
                  commits are left as they are.
                </p>
                <div className="flex gap-2">
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={!!ctx.busy || !pattern.trim()}
                    onClick={() => void act("track")}
                  >
                    Track pattern
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={!!ctx.busy || !pattern.trim()}
                    onClick={() => void act("untrack")}
                  >
                    Stop tracking pattern
                  </Button>
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}

export function PatchPreview({ ctx, onClose }: { ctx: PreviewContext; onClose: () => void }) {
  const [tab, setTab] = useState("export")
  const [mode, setMode] = useState("staged")
  const [ref, setRef] = useState("HEAD")
  const [body, setBody] = useState("")
  const [staged, setStaged] = useState(true)
  const [check, setCheck] = useState<{ version: string; summary: string; files: string[] }>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  const run = async (fn: () => Promise<void>) => {
    setBusy(true)
    setError(undefined)
    try {
      await fn()
    } catch (e) {
      setError(new Error(errorMessage(e)))
    } finally {
      setBusy(false)
    }
  }
  const download = () => {
    const url = URL.createObjectURL(new Blob([body], { type: "text/x-patch" }))
    const a = document.createElement("a")
    a.href = url
    a.download = "changes.patch"
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        title="Patch exchange"
        mono={false}
        subtitle="Export changes or check a patch before applying it"
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 space-y-3 overflow-auto p-3">
        <div className="flex gap-1">
          <FilterChip
            selected={tab === "export"}
            onClick={() => {
              setTab("export")
              setBody("")
              setCheck(undefined)
            }}
          >
            Export
          </FilterChip>
          {ctx.canControl && ctx.canWrite && (
            <FilterChip
              selected={tab === "import"}
              onClick={() => {
                setTab("import")
                setBody("")
                setCheck(undefined)
              }}
            >
              Import
            </FilterChip>
          )}
        </div>
        {error && <ErrorState error={error} />}
        {tab === "export" ? (
          <>
            <Field label="Changes to export">
              <Select
                value={mode}
                onValueChange={(value) => {
                  setMode(value)
                  setBody("")
                }}
              >
                <SelectTrigger aria-label="Patch source">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="staged">Staged changes</SelectItem>
                  <SelectItem value="working">Unstaged tracked changes</SelectItem>
                  <SelectItem value="commit">One commit</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            {mode === "commit" && (
              <Field label="Commit">
                <Input
                  aria-label="Patch commit"
                  value={ref}
                  onChange={(e) => {
                    setRef(e.target.value)
                    setBody("")
                  }}
                />
              </Field>
            )}
            <Button
              size="sm"
              variant="outline"
              disabled={busy}
              onClick={() =>
                void run(async () => {
                  const patch = await get<{ body: string }>("/git/patch/export", {
                    path: ctx.repoPath,
                    mode,
                    ref,
                  })
                  setBody(patch.body)
                  if (!patch.body) throw new Error("No changes to export.")
                })
              }
            >
              Prepare patch
            </Button>
            {body && (
              <>
                <Button size="sm" variant="outline" onClick={download}>
                  Download patch
                </Button>
                <DiffView body={body} lineNumbers />
              </>
            )}
          </>
        ) : (
          <>
            <Field label="Patch file">
              <Input
                type="file"
                aria-label="Import patch file"
                accept=".patch,.diff,text/plain,text/x-patch"
                onChange={(e) => {
                  const file = e.target.files?.[0]
                  if (!file) return
                  void run(async () => {
                    if (file.size > 2 * 1024 * 1024)
                      throw new Error("Choose a patch smaller than 2 MiB.")
                    setBody(await file.text())
                    setCheck(undefined)
                  })
                }}
              />
            </Field>
            <Field label="Patch content">
              <Textarea
                aria-label="Patch content"
                value={body}
                onChange={(e) => {
                  setBody(e.target.value)
                  setCheck(undefined)
                }}
                className="font-mono"
                rows={8}
              />
            </Field>
            <label className="flex items-center gap-2 text-xs">
              <input
                type="checkbox"
                checked={staged}
                onChange={(e) => {
                  setStaged(e.target.checked)
                  setCheck(undefined)
                }}
              />
              Stage imported changes
            </label>
            <p className="text-hint text-muted-foreground">
              Commit or stash existing changes first. Patches apply to this checkout, with a 2 MiB
              limit.
            </p>
            <Button
              size="sm"
              variant="outline"
              disabled={busy || !!ctx.busy || !body}
              onClick={() =>
                void run(async () =>
                  setCheck(
                    await post(
                      "/git/patch/check",
                      { body, staged },
                      { query: { path: ctx.repoPath } },
                    ),
                  ),
                )
              }
            >
              Check patch
            </Button>
            {check && (
              <>
                <pre className="overflow-auto font-mono text-hint">{check.summary}</pre>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy || !!ctx.busy}
                  onClick={() =>
                    ctx.confirm({
                      title: "Apply this patch?",
                      description: `Change ${check.files.length} files in this checkout${staged ? " and stage the result" : ""}.`,
                      confirmLabel: "Apply patch",
                      action: async () => {
                        await ctx.run("Patch applied", () =>
                          post<GitResult>(
                            "/git/patch/import",
                            { body, staged, version: check.version },
                            { query: { path: ctx.repoPath } },
                          ),
                        )
                        setCheck(undefined)
                        setBody("")
                      },
                    })
                  }
                >
                  Apply patch
                </Button>
              </>
            )}
          </>
        )}
      </div>
    </div>
  )
}
