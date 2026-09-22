"use client"

import { useState } from "react"
import { get, post, errorMessage } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { PreviewContext } from "@/components/git/preview-panel"
import { PreviewHeader } from "@/components/git/preview-header"
import { HistoryPaging } from "@/components/git/inspect-panels"
import { Field } from "@/components/form"
import { ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { FilterChip } from "@/components/tabs"
import { DiffView } from "@/components/files/diff-view"
import { Modal } from "@/components/modal"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Config = {
  configured: boolean
  kind: string
  url: string
  project: string
  login: string
  defaultBranch: string
}
type Request = {
  number: number
  title: string
  body: string
  state: string
  head: string
  base: string
  sha: string
  url: string
  author: string
  draft: boolean
}

export function ForgePreview({ ctx, onClose }: { ctx: PreviewContext; onClose: () => void }) {
  const account = usePoll(
    (signal) => get<Config>("/git/forge/", { path: ctx.repoPath }, signal),
    0,
    [ctx.repoPath],
  )
  const [setup, setSetup] = useState(false)
  const [creating, setCreating] = useState(false)
  const [number, setNumber] = useState<number | null>(null)
  const [state, setState] = useState("open")
  const [page, setPage] = useState(1)
  const list = usePoll(
    (signal) =>
      get<{ requests: Request[]; hasMore: boolean }>(
        "/git/forge/requests",
        { path: ctx.repoPath, state, page },
        signal,
      ),
    60000,
    [ctx.repoPath, state, page, account.data?.kind],
    { enabled: !!account.data?.configured },
  )
  const a = account.data
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        title={
          a?.configured
            ? `${a.kind === "gitlab" ? "GitLab" : "Gitea"} · ${a.project}`
            : "GitLab and Gitea"
        }
        mono={false}
        subtitle={
          a?.configured ? `Signed in as ${a.login}` : "Connect an account for this repository"
        }
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 overflow-auto">
        {account.error && <ErrorState error={account.error} className="m-3" />}
        {account.loading && <LoadingRows className="p-3" rows={3} />}
        <div className="flex flex-wrap gap-2 p-3">
          {ctx.canAdmin && (
            <Button size="xs" variant="outline" onClick={() => setSetup(true)}>
              {a?.configured ? "Change account" : "Connect account"}
            </Button>
          )}
          {ctx.canAdmin && a?.configured && (
            <Button
              size="xs"
              variant="ghost"
              onClick={() =>
                ctx.confirm({
                  title: "Disconnect this provider account?",
                  description:
                    "Remove its saved token from this repository's dashboard connection.",
                  action: async () => {
                    await post("/git/forge/disconnect", {}, { query: { path: ctx.repoPath } })
                    setNumber(null)
                    account.refresh()
                  },
                })
              }
            >
              Disconnect
            </Button>
          )}
          {ctx.canControl && a?.configured && (
            <Button size="xs" variant="outline" onClick={() => setCreating(true)}>
              New request
            </Button>
          )}
        </div>
        {a && !a.configured && (
          <p className="px-3 pb-3 text-hint text-muted-foreground">
            {ctx.canAdmin
              ? "Connect a GitLab or Gitea token to read requests, comment, approve and merge. Git pushes continue to use the checkout owner's existing SSH keys or credential helper."
              : "An administrator can connect a GitLab or Gitea account here."}
          </p>
        )}
        {a?.configured &&
          (number ? (
            <ForgeRequest
              key={`${a.kind}:${number}`}
              number={number}
              config={a}
              ctx={ctx}
              onBack={() => setNumber(null)}
            />
          ) : (
            <>
              <div className="flex gap-1 border-y border-hairline px-3 py-1.5">
                {["open", "closed", "all"].map((v) => (
                  <FilterChip
                    key={v}
                    selected={state === v}
                    onClick={() => {
                      setState(v)
                      setPage(1)
                    }}
                  >
                    {v[0].toUpperCase() + v.slice(1)}
                  </FilterChip>
                ))}
              </div>
              {list.error && <ErrorState error={list.error} className="m-3" />}
              {list.loading && <LoadingRows className="p-3" rows={3} />}
              <ul className="divide-y divide-hairline">
                {list.data?.requests.map((r) => (
                  <li key={r.number}>
                    <button
                      type="button"
                      className="w-full space-y-1 px-3 py-2 text-left focus-ring-inset hover:bg-row-hover"
                      onClick={() => setNumber(r.number)}
                    >
                      <span className="block text-xs">{r.title}</span>
                      <span className="block text-hint text-muted-foreground">
                        #{r.number} · {r.head} → {r.base} · {r.state} · {r.author}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
              {list.data && (
                <>
                  <HistoryPaging
                    start={(page - 1) * 50}
                    count={list.data.requests.length}
                    hasMore={list.data.hasMore}
                    busy={list.loading}
                    onPrevious={() => setPage(page - 1)}
                    onNext={() => setPage(page + 1)}
                  />
                  {list.data.requests.length === 0 && (
                    <p className="p-3 text-hint text-muted-foreground">No requests in this view.</p>
                  )}
                </>
              )}
            </>
          ))}
      </div>
      {setup && (
        <ForgeSetup
          ctx={ctx}
          config={a}
          onClose={() => setSetup(false)}
          onSaved={() => {
            setSetup(false)
            setNumber(null)
            account.refresh()
            list.refresh()
          }}
        />
      )}
      {creating && a && (
        <ForgeCreate
          ctx={ctx}
          config={a}
          onClose={() => setCreating(false)}
          onCreated={(r) => {
            setCreating(false)
            list.refresh()
            setNumber(r.number)
          }}
        />
      )}
    </div>
  )
}

function ForgeSetup({
  ctx,
  config,
  onClose,
  onSaved,
}: {
  ctx: PreviewContext
  config?: Config
  onClose: () => void
  onSaved: () => void
}) {
  const [kind, setKind] = useState(config?.kind || "gitlab")
  const [url, setURL] = useState(config?.url || "https://gitlab.com")
  const [project, setProject] = useState(config?.project || "")
  const [token, setToken] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  return (
    <Modal
      open
      onOpenChange={(o) => !o && !busy && onClose()}
      title="Connect GitLab or Gitea"
      description="The token is encrypted on this server and scoped to this checkout and its Linux owner."
    >
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          setBusy(true)
          setError(undefined)
          try {
            await post(
              "/git/forge/account",
              { kind, url, project, token },
              { query: { path: ctx.repoPath } },
            )
            setToken("")
            onSaved()
          } catch (e) {
            setError(new Error(errorMessage(e)))
          } finally {
            setBusy(false)
          }
        }}
      >
        <Field label="Provider">
          <Select value={kind} onValueChange={setKind}>
            <SelectTrigger aria-label="Git provider">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="gitlab">GitLab</SelectItem>
              <SelectItem value="gitea">Gitea</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field label="Server URL">
          <Input
            aria-label="Provider server URL"
            value={url}
            onChange={(e) => setURL(e.target.value)}
            placeholder="https://git.example.com"
          />
        </Field>
        <Field label="Project">
          <Input
            aria-label="Provider project"
            value={project}
            onChange={(e) => setProject(e.target.value)}
            placeholder="owner/repository"
          />
        </Field>
        <Field label="Access token">
          <Input
            aria-label="Provider access token"
            type="password"
            autoComplete="new-password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
        </Field>
        <p className="text-hint text-muted-foreground">
          {kind === "gitlab"
            ? "Use a token with api permission and access to the project."
            : "Use a token with repository and issue write permissions, plus user read permission."}{" "}
          Git clone and push keep using the existing Git credentials.
        </p>
        {error && <ErrorState error={error} />}
        <Button
          disabled={busy || !token.trim() || !project.trim() || !url.trim()}
          pending={busy}
          size="sm"
        >
          Connect account
        </Button>
      </form>
    </Modal>
  )
}

function ForgeCreate({
  ctx,
  config,
  onClose,
  onCreated,
}: {
  ctx: PreviewContext
  config: Config
  onClose: () => void
  onCreated: (r: Request) => void
}) {
  const [title, setTitle] = useState("")
  const [body, setBody] = useState("")
  const [head, setHead] = useState(ctx.branch)
  const [base, setBase] = useState(config.defaultBranch || "main")
  const [draft, setDraft] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  return (
    <Modal
      open
      onOpenChange={(o) => !o && !busy && onClose()}
      title="New request"
      description="Push the source branch first, then propose it for review."
    >
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          setBusy(true)
          try {
            onCreated(
              await post<Request>(
                "/git/forge/requests",
                { title, body, head, base, draft },
                { query: { path: ctx.repoPath } },
              ),
            )
          } catch (e) {
            setError(new Error(errorMessage(e)))
          } finally {
            setBusy(false)
          }
        }}
      >
        <Field label="Title">
          <Input
            aria-label="Request title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            maxLength={255}
          />
        </Field>
        <Field label="Source branch">
          <Input
            aria-label="Request source branch"
            value={head}
            onChange={(e) => setHead(e.target.value)}
          />
        </Field>
        <Field label="Target branch">
          <Input
            aria-label="Request target branch"
            value={base}
            onChange={(e) => setBase(e.target.value)}
          />
        </Field>
        <Field label="Description">
          <Textarea
            aria-label="Request description"
            value={body}
            onChange={(e) => setBody(e.target.value)}
            maxLength={60000}
          />
        </Field>
        <label className="flex items-center gap-2 text-xs">
          <input type="checkbox" checked={draft} onChange={(e) => setDraft(e.target.checked)} />
          Draft
        </label>
        {error && <ErrorState error={error} />}
        <Button
          size="sm"
          disabled={busy || !title.trim() || !head.trim() || !base.trim()}
          pending={busy}
        >
          Open request
        </Button>
      </form>
    </Modal>
  )
}

function ForgeRequest({
  ctx,
  config,
  number,
  onBack,
}: {
  ctx: PreviewContext
  config: Config
  number: number
  onBack: () => void
}) {
  // Details stay at the revision explicitly loaded by the reader.
  const request = usePoll(
    (signal) => get<Request>(`/git/forge/requests/${number}`, { path: ctx.repoPath }, signal),
    0,
    [ctx.repoPath, number],
  )
  const [tab, setTab] = useState("files")
  const [page, setPage] = useState(1)
  const [file, setFile] = useState<string | null>(null)
  const [action, setAction] = useState("comment")
  const [method, setMethod] = useState("merge")
  const [body, setBody] = useState("")
  const r = request.data
  const files = usePoll(
    (signal) =>
      get<{
        files: { path: string; previous?: string; diff: string; omitted: boolean }[]
        body?: string
        hasMore: boolean
      }>(`/git/forge/requests/${number}/files`, { path: ctx.repoPath, head: r?.sha, page }, signal),
    0,
    [ctx.repoPath, number, r?.sha, page],
    { enabled: !!r && tab === "files" },
  )
  const conversation = usePoll(
    (signal) =>
      get<{
        comments: { id: number; body: string; author: string; at: string }[]
        hasMore: boolean
      }>(`/git/forge/requests/${number}/conversation`, { path: ctx.repoPath, page }, signal),
    0,
    [ctx.repoPath, number, page],
    { enabled: tab === "conversation" },
  )
  const selected = files.data?.files.find((f) => f.path === file)
  return (
    <div className="border-t border-hairline">
      <div className="flex gap-2 p-3">
        <Button size="xs" variant="ghost" onClick={onBack}>
          Back to requests
        </Button>
        <Button
          size="xs"
          variant="ghost"
          onClick={() => {
            request.refresh()
            files.refresh()
            conversation.refresh()
          }}
        >
          Reload
        </Button>
        {r && (
          <Button size="xs" variant="ghost" asChild>
            <a href={r.url} target="_blank" rel="noreferrer">
              Open on {config.kind === "gitlab" ? "GitLab" : "Gitea"}
            </a>
          </Button>
        )}
      </div>
      {request.error && <ErrorState error={request.error} className="m-3" />}
      {request.loading && <LoadingRows className="p-3" rows={3} />}
      {r && (
        <>
          <div className="space-y-2 px-3 pb-3">
            <p className="text-sm">{r.title}</p>
            <p className="text-hint text-muted-foreground">
              #{number} · {r.head} → {r.base} · {r.state} · {r.sha.slice(0, 7)}
            </p>
            <p className="text-xs break-words whitespace-pre-wrap">{r.body || "No description."}</p>
          </div>
          <div className="flex gap-1 border-y border-hairline px-3 py-1.5">
            <FilterChip
              selected={tab === "files"}
              onClick={() => {
                setTab("files")
                setPage(1)
              }}
            >
              Changed files
            </FilterChip>
            <FilterChip
              selected={tab === "conversation"}
              onClick={() => {
                setTab("conversation")
                setPage(1)
              }}
            >
              Conversation
            </FilterChip>
          </div>
          {tab === "files" ? (
            <>
              {files.error && <ErrorState error={files.error} className="m-3" />}
              {files.loading && <LoadingRows className="p-3" rows={3} />}
              {files.data?.body && <DiffView body={files.data.body} lineNumbers />}
              <ul className="divide-y divide-hairline">
                {files.data?.files.map((f) => (
                  <li key={f.path}>
                    <button
                      type="button"
                      className="w-full px-3 py-2 text-left font-mono text-hint focus-ring-inset hover:bg-row-hover"
                      aria-pressed={file === f.path}
                      onClick={() => setFile(file === f.path ? null : f.path)}
                    >
                      {f.path}
                    </button>
                  </li>
                ))}
              </ul>
              {selected &&
                (selected.omitted ? (
                  <p className="p-3 text-hint text-muted-foreground">
                    The provider omitted this file&apos;s text diff. Open it on the provider to
                    inspect it.
                  </p>
                ) : (
                  <DiffView body={selected.diff} lineNumbers singleFile />
                ))}
              {files.data && config.kind === "gitlab" && (
                <HistoryPaging
                  start={(page - 1) * 50}
                  count={files.data.files.length}
                  hasMore={files.data.hasMore}
                  busy={files.loading}
                  onPrevious={() => setPage(page - 1)}
                  onNext={() => setPage(page + 1)}
                />
              )}
            </>
          ) : (
            <>
              {conversation.error && <ErrorState error={conversation.error} className="m-3" />}
              {conversation.loading && <LoadingRows className="p-3" rows={3} />}
              <ul className="divide-y divide-hairline">
                {conversation.data?.comments.map((c) => (
                  <li className="space-y-1 p-3" key={c.id}>
                    <p className="text-hint text-muted-foreground">{c.author}</p>
                    <p className="text-xs break-words whitespace-pre-wrap">{c.body}</p>
                  </li>
                ))}
              </ul>
              {conversation.data && (
                <HistoryPaging
                  start={(page - 1) * 50}
                  count={conversation.data.comments.length}
                  hasMore={conversation.data.hasMore}
                  busy={conversation.loading}
                  onPrevious={() => setPage(page - 1)}
                  onNext={() => setPage(page + 1)}
                />
              )}
            </>
          )}
          {ctx.canControl && ["open", "opened"].includes(r.state) && (
            <div className="space-y-3 border-t border-hairline p-3">
              <Field label="Request action">
                <Select value={action} onValueChange={setAction}>
                  <SelectTrigger aria-label="Provider request action">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="comment">Comment</SelectItem>
                    <SelectItem value="approve">Approve</SelectItem>
                    {config.kind === "gitea" && (
                      <SelectItem value="request_changes">Request changes</SelectItem>
                    )}
                    <SelectItem value="merge">Merge</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              {action === "merge" ? (
                <Field label="Merge method">
                  <Select value={method} onValueChange={setMethod}>
                    <SelectTrigger aria-label="Provider merge method">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="merge">Merge</SelectItem>
                      <SelectItem value="squash">Squash</SelectItem>
                      {config.kind === "gitea" && <SelectItem value="rebase">Rebase</SelectItem>}
                    </SelectContent>
                  </Select>
                </Field>
              ) : (
                <Field label="Comment">
                  <Textarea
                    aria-label="Provider comment"
                    value={body}
                    onChange={(e) => setBody(e.target.value)}
                    maxLength={60000}
                  />
                </Field>
              )}
              <Button
                size="sm"
                variant="outline"
                disabled={
                  !!ctx.busy ||
                  (["comment", "request_changes"].includes(action) && !body.trim()) ||
                  (action === "merge" && r.draft)
                }
                onClick={() =>
                  ctx.confirm({
                    title: `${action === "merge" ? "Merge" : "Publish review on"} #${number}?`,
                    description: `This updates ${config.project} on ${config.kind === "gitlab" ? "GitLab" : "Gitea"}, for commit ${r.sha.slice(0, 7)}.`,
                    confirmLabel: action === "merge" ? "Merge request" : "Publish review",
                    action: async () => {
                      await ctx.run("Request updated", () =>
                        post(
                          `/git/forge/requests/${number}/action`,
                          { action, method, body, sha: r.sha },
                          { query: { path: ctx.repoPath } },
                        ).then(() => ({ command: "Provider request", ok: true, output: "" })),
                      )
                      setBody("")
                      request.refresh()
                      conversation.refresh()
                    },
                  })
                }
              >
                {action === "merge" ? "Merge request" : "Publish review"}
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  )
}
