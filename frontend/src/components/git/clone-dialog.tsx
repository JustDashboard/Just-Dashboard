"use client"

import { useMemo, useState } from "react"
import { CloudDownload, GitHubMark } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { GitHubRepoSummary, GitHubStatus, GitResult } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Modal } from "@/components/modal"
import { Disclosure, Field, FieldRow, FormNote } from "@/components/form"
import { Textarea } from "@/components/ui/textarea"
import { SearchInput } from "@/components/page"
import { Notice, Spinner } from "@/components/state"
import { Tag } from "@/components/tag"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Mode = "clone" | "init"

/**
 * Getting a repository onto this server.
 *
 * The list page can only show what is already checked out, and until this
 * existed the way to add something was a shell. A clone lands in one of the
 * configured roots — that is the boundary the page works inside, and a
 * checkout outside it would be one the page could then not operate on — and
 * with a GitHub account signed in the repository is picked from a list rather
 * than transcribed. The second tab starts an empty repository in a folder
 * that already exists, for a project that began on this server.
 */
type CloneDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  github?: GitHubStatus
  /** The new repository's path. */
  onDone: (path: string) => void
}

export function CloneDialog(props: CloneDialogProps) {
  // A fresh body per opening: a failed attempt's error and a half-typed URL
  // do not come back the next time the dialog opens.
  return <CloneDialogBody key={String(props.open)} {...props} />
}

function CloneDialogBody({ open, onOpenChange, github, onDone }: CloneDialogProps) {
  const [mode, setMode] = useState<Mode>("clone")
  const [url, setUrl] = useState("")
  const [parent, setParent] = useState("")
  const [name, setName] = useState("")
  const [dir, setDir] = useState("")
  const [query, setQuery] = useState("")
  const [branch, setBranch] = useState("")
  const [depth, setDepth] = useState("")
  const [sparse, setSparse] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const roots = usePoll(
    (signal) => get<{ roots: string[] }>("/git/roots", undefined, signal),
    0,
    [],
    { enabled: open },
  )
  const signedIn = Boolean(github?.available && github.account?.loggedIn)
  const repos = usePoll(
    (signal) => get<GitHubRepoSummary[]>("/git/github/repos", { limit: 200 }, signal),
    0,
    [],
    { enabled: open && signedIn && mode === "clone" },
  )

  // The first root is the destination until another is chosen.
  const rootList = roots.data?.roots ?? []
  const destination = parent || rootList[0] || ""

  // The directory name follows the URL until it is typed over.
  const suggested = useMemo(() => nameFromUrl(url), [url])
  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase()
    const list = repos.data ?? []
    if (!needle) return list.slice(0, 30)
    return list
      .filter(
        (r) =>
          r.nameWithOwner.toLowerCase().includes(needle) ||
          (r.description ?? "").toLowerCase().includes(needle),
      )
      .slice(0, 30)
  }, [repos.data, query])

  const clone = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const res = await post<{ path: string; result: GitResult }>("/git/clone", {
        url: url.trim(),
        parent: destination,
        name: name.trim() || undefined,
        branch: branch.trim() || undefined,
        depth: depth ? Number(depth) : undefined,
        sparse: sparse
          .split("\n")
          .map((line) => line.trim())
          .filter(Boolean),
      })
      notify.success("Cloned", { description: res.path })
      onDone(res.path)
      onOpenChange(false)
      setUrl("")
      setName("")
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const init = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post<GitResult>("/git/init", { path: dir.trim() })
      notify.success("Repository initialised", { description: dir.trim() })
      onDone(dir.trim())
      onOpenChange(false)
      setDir("")
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const validDepth =
    depth === "" ||
    (Number.isInteger(Number(depth)) && Number(depth) >= 1 && Number(depth) <= 100000)
  const canClone = url.trim().length > 0 && destination.length > 0 && validDepth && !busy
  const canInit = dir.trim().startsWith("/") && !busy

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="Add a repository"
      size="md"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          {mode === "clone" ? (
            <Button onClick={clone} disabled={!canClone} pending={busy}>
              <CloudDownload className="size-4" />
              Clone
            </Button>
          ) : (
            <Button onClick={init} disabled={!canInit} pending={busy}>
              Initialise
            </Button>
          )}
        </>
      }
    >
      <div className="space-y-3">
        <div className="-mx-4 -mt-4 flex border-b border-hairline px-2">
          <button
            type="button"
            className={tabClasses(mode === "clone", "h-9")}
            onClick={() => setMode("clone")}
          >
            Clone from a URL
          </button>
          <button
            type="button"
            className={tabClasses(mode === "init", "h-9")}
            onClick={() => setMode("init")}
          >
            Start an empty one
          </button>
        </div>

        {error && (
          <Notice title={mode === "clone" ? "The clone failed" : "git refused"} tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}

        {mode === "clone" ? (
          <>
            {signedIn && (
              <div className="space-y-1.5">
                <div className="flex items-center gap-2">
                  <GitHubMark className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="text-body font-medium">Your repositories</span>
                  {repos.loading && !repos.data && <Spinner className="size-3.5" />}
                </div>
                <SearchInput
                  dense
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Filter by name…"
                  aria-label="Filter repositories"
                  containerClassName="w-full sm:w-full"
                />
                {repos.error && <FormNote tone="warning">{errorMessage(repos.error)}</FormNote>}
                {repos.data && (
                  <ul className="max-h-40 divide-y divide-hairline overflow-y-auto rounded-md border border-hairline">
                    {visible.map((r) => (
                      <li key={r.nameWithOwner}>
                        <button
                          type="button"
                          onClick={() => {
                            setUrl(r.cloneUrl)
                            setName(r.name)
                          }}
                          className={cn(
                            "flex w-full min-w-0 items-center gap-2 px-2.5 py-1.5 text-left focus-ring-inset transition-colors hover:bg-row-hover",
                            url === r.cloneUrl && "bg-accent",
                          )}
                        >
                          <span className="min-w-0 flex-1">
                            <span className="block truncate font-mono text-xs">
                              {r.nameWithOwner}
                            </span>
                            {r.description && (
                              <span className="block truncate text-hint text-muted-foreground">
                                {r.description}
                              </span>
                            )}
                          </span>
                          {r.private && <Tag>private</Tag>}
                          {r.pushedAt && (
                            <span className="shrink-0 text-hint text-muted-foreground">
                              {relativeTime(r.pushedAt)}
                            </span>
                          )}
                        </button>
                      </li>
                    ))}
                    {visible.length === 0 && (
                      <li className="px-2.5 py-3 text-center text-hint text-muted-foreground">
                        Nothing matches.
                      </li>
                    )}
                  </ul>
                )}
              </div>
            )}
            <Field
              label="Repository URL"
              htmlFor="clone-url"
              hint="https://, ssh:// or git@host:owner/repo.git. A private HTTPS repository needs the GitHub sign-in above."
            >
              <Input
                id="clone-url"
                autoComplete="off"
                spellCheck={false}
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://github.com/owner/repo.git"
                className="font-mono"
              />
            </Field>
            <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
              <Field label="Into" hint="One of the folders the Git page looks in.">
                <Select value={destination} onValueChange={setParent}>
                  <SelectTrigger className="w-full font-mono">
                    <SelectValue
                      placeholder={rootList.length ? "Choose a folder" : "No roots exist"}
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {rootList.map((r) => (
                      <SelectItem key={r} value={r} className="font-mono">
                        {r}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field
                label="Folder name"
                htmlFor="clone-name"
                hint="Left empty, git picks it from the URL."
              >
                <Input
                  id="clone-name"
                  autoComplete="off"
                  spellCheck={false}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={suggested || "repo"}
                  className="font-mono"
                />
              </Field>
            </div>
            <Disclosure
              summary="Clone options"
              facts="Choose a branch, limit history or check out selected folders."
            >
              <div className="space-y-3">
                <FieldRow>
                  <Field
                    label="Branch or tag"
                    htmlFor="clone-branch"
                    hint="Leave empty for the remote’s default branch."
                  >
                    <Input
                      id="clone-branch"
                      value={branch}
                      onChange={(e) => setBranch(e.target.value)}
                      placeholder="main"
                      className="font-mono"
                    />
                  </Field>
                  <Field
                    label="History depth"
                    htmlFor="clone-depth"
                    hint="Leave empty for full history. A shallow clone initially fetches one branch."
                    error={!validDepth ? "Enter a whole number from 1 to 100000." : undefined}
                  >
                    <Input
                      id="clone-depth"
                      type="number"
                      min={1}
                      max={100000}
                      value={depth}
                      onChange={(e) => setDepth(e.target.value)}
                      placeholder="Full history"
                    />
                  </Field>
                </FieldRow>
                <Field
                  label="Sparse folders"
                  htmlFor="clone-sparse"
                  hint="Optional: one repository-relative folder per line. Root files are included too."
                >
                  <Textarea
                    id="clone-sparse"
                    rows={3}
                    value={sparse}
                    onChange={(e) => setSparse(e.target.value)}
                    placeholder={"src\npackages/shared"}
                    className="font-mono"
                  />
                </Field>
              </div>
            </Disclosure>
            {destination && (
              <FormNote>
                Lands at{" "}
                <span className="font-mono text-foreground">
                  {destination}/{name.trim() || suggested || "…"}
                </span>
                , owned by the account that owns {destination}.
              </FormNote>
            )}
          </>
        ) : (
          <Field
            label="Folder"
            htmlFor="init-dir"
            hint="An existing folder inside one of the Git roots that is not a repository yet. It starts on a branch called main."
          >
            <Input
              id="init-dir"
              autoComplete="off"
              spellCheck={false}
              value={dir}
              onChange={(e) => setDir(e.target.value)}
              placeholder={rootList[0] ? `${rootList[0]}/my-project` : "/srv/my-project"}
              className="font-mono"
            />
          </Field>
        )}
      </div>
    </Modal>
  )
}

/** What git would call a clone of this URL: the last path element, without .git. */
function nameFromUrl(url: string): string {
  const trimmed = url
    .trim()
    .replace(/\/+$/, "")
    .replace(/\.git$/, "")
  const i = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf(":"))
  return i >= 0 ? trimmed.slice(i + 1) : trimmed
}
