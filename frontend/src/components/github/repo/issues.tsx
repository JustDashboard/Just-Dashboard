"use client"

import { useState } from "react"
import { CheckCircle, Pencil, Plus } from "@/components/icons"
import { IssueMark } from "@/components/git/glyphs"
import { ChipStrip, FilterChip } from "@/components/chips"
import { ChoiceList, ChoiceListSkeleton, ChoiceRow } from "@/components/choice-row"
import { Field } from "@/components/form"
import { CommentCount } from "@/components/git/pull-state"
import { Markdown } from "@/components/markdown"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { BackToList } from "@/components/github/repo/pulls"
import {
  AbilityButton,
  Blocked,
  LabelTag,
  OnGitHub,
  ShowMore,
  useActReason,
  useOpenParam,
  usePages,
  useRepo,
} from "@/components/github/repo/shared"
import { usePoll } from "@/hooks/use-poll"
import { errorMessage, get, patch, post } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  GitHubIssue,
  GitHubIssueCreate,
  GitHubIssuePage,
  GitHubIssueUpdate,
  GitHubLabel,
} from "@/lib/github-types"

type IssueFilter = "open" | "closed" | "all"
const FILTERS: { key: IssueFilter; label: string }[] = [
  { key: "open", label: "Open" },
  { key: "closed", label: "Closed" },
  { key: "all", label: "All" },
]
const ANY_LABEL = "\u0000any"

/** The repository's labels, read once for the filter and the forms. */
function useLabels() {
  const { repo, path } = useRepo()
  return usePoll((signal) => get<GitHubLabel[]>(path("/labels"), undefined, signal), 0, [
    repo.fullName,
  ])
}

/**
 * The repository's issues, by state and by label, each a card that opens
 * the issue in place of the list (`&issue=<n>`). Pull requests, which
 * GitHub also calls issues, are left out by the server.
 */
export function RepoIssues() {
  const { repo } = useRepo()
  const [open, setOpen] = useOpenParam("issue")
  const number = open && /^\d+$/.test(open) ? Number(open) : null
  if (!repo.settings.hasIssues) {
    return (
      <EmptyNote>
        Issues are switched off on {repo.fullName}. They can be switched on in Settings.
      </EmptyNote>
    )
  }
  if (number !== null) return <IssueDetail number={number} onBack={() => setOpen(null)} />
  return <IssueList onOpen={(n) => setOpen(String(n))} />
}

function IssueList({ onOpen }: { onOpen: (number: number) => void }) {
  const { path } = useRepo()
  const [filter, setFilter] = useState<IssueFilter>("open")
  const [label, setLabel] = useState(ANY_LABEL)
  const [creating, setCreating] = useState(false)
  const labels = useLabels()
  const pages = usePages<GitHubIssue, GitHubIssuePage>(
    path("/issues"),
    { state: filter, labels: label === ANY_LABEL ? undefined : label },
    (p) => p.issues,
  )
  return (
    <Panel plain aria-label="Issues">
      <PanelHeader
        title="Issues"
        actions={
          <AbilityButton
            ability="createIssues"
            size="sm"
            variant="outline"
            onClick={() => setCreating(true)}
          >
            <Plus className="size-3.5" />
            New issue
          </AbilityButton>
        }
      />
      <PanelBody flush className="space-y-3">
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
          <ChipStrip aria-label="Which issues">
            {FILTERS.map((f) => (
              <FilterChip key={f.key} selected={filter === f.key} onClick={() => setFilter(f.key)}>
                {f.label}
              </FilterChip>
            ))}
          </ChipStrip>
          {(labels.data?.length ?? 0) > 0 && (
            <Select value={label} onValueChange={setLabel}>
              <SelectTrigger aria-label="Label" size="sm" className="w-44">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ANY_LABEL}>Any label</SelectItem>
                {labels.data?.map((l) => (
                  <SelectItem key={l.name} value={l.name}>
                    {l.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>
        {pages.error && pages.items.length === 0 ? (
          <ErrorState error={pages.error} onRetry={pages.reload} retryable />
        ) : pages.loading && pages.items.length === 0 ? (
          <ChoiceListSkeleton rows={4} />
        ) : pages.items.length === 0 ? (
          <EmptyNote>
            {filter === "all"
              ? "No issues yet."
              : `No ${filter} issues${label === ANY_LABEL ? "" : ` labelled ${label}`}.`}
          </EmptyNote>
        ) : (
          <ChoiceList data-slot="github-issues">
            {pages.items.map((issue) => (
              <ChoiceRow
                key={issue.number}
                leading={<IssueState issue={issue} />}
                verb={`Open issue #${issue.number}`}
                title={
                  <>
                    <span className="numeric text-muted-foreground">#{issue.number}</span>{" "}
                    {issue.title}
                  </>
                }
                description={
                  <>
                    {issue.author} ·{" "}
                    <span className="numeric">{relativeTime(issue.updatedAt)}</span>
                  </>
                }
                trailing={
                  <span className="flex items-center gap-1.5">
                    <span className="flex items-center gap-1.5 max-sm:hidden">
                      {issue.labels.slice(0, 2).map((l) => (
                        <LabelTag key={l.name} label={l} />
                      ))}
                    </span>
                    <CommentCount count={issue.comments} />
                  </span>
                }
                onSelect={() => onOpen(issue.number)}
              />
            ))}
          </ChoiceList>
        )}
        <ShowMore pages={pages} />
      </PanelBody>
      {creating && (
        <IssueForm
          labels={labels.data ?? []}
          onClose={() => setCreating(false)}
          onSaved={(issue) => {
            setCreating(false)
            onOpen(issue.number)
          }}
        />
      )}
    </Panel>
  )
}

/** An issue's state as GitHub marks it: green open, violet done, grey not planned. */
function IssueState({ issue }: { issue: Pick<GitHubIssue, "state" | "stateReason"> }) {
  if (issue.state === "open") {
    return (
      <IssueMark
        role="img"
        aria-label="open"
        className="size-4 shrink-0"
        style={{ color: "var(--pull-open)" }}
      />
    )
  }
  if (issue.stateReason === "not_planned") {
    return (
      <IssueMark
        role="img"
        aria-label="not planned"
        className="size-4 shrink-0 text-muted-foreground"
      />
    )
  }
  return (
    <CheckCircle
      role="img"
      aria-label="completed"
      className="size-4 shrink-0"
      style={{ color: "var(--pull-merged)" }}
    />
  )
}

function IssueDetail({ number, onBack }: { number: number; onBack: () => void }) {
  const { repo, path, refresh } = useRepo()
  const read = usePoll(
    (signal) => get<GitHubIssue>(path(`/issues/${number}`), undefined, signal),
    0,
    [repo.fullName, number],
  )
  const labels = useLabels()
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const stateReason = useActReason("issues")
  const issue = read.data
  if (!issue) {
    return (
      <div className="space-y-4">
        <BackToList label="Issues" onBack={onBack} />
        {read.error ? (
          <ErrorState error={read.error} onRetry={read.refresh} retryable />
        ) : (
          <LoadingRows />
        )}
      </div>
    )
  }
  const change = async (update: GitHubIssueUpdate, done: string) => {
    setBusy(true)
    try {
      await patch<GitHubIssue>(path(`/issues/${issue.number}`), update)
      notify.success(done)
      read.refresh()
      refresh()
    } catch (err) {
      notify.error("GitHub did not take that", err)
    } finally {
      setBusy(false)
    }
  }
  const stateControl =
    issue.state === "open" ? (
      stateReason ? (
        <Blocked reason={stateReason}>
          <Button size="sm" variant="outline" disabled>
            Close
          </Button>
        </Blocked>
      ) : (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="sm" variant="outline" pending={busy}>
              Close
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem
              onSelect={() =>
                change({ state: "closed", stateReason: "completed" }, `Closed #${issue.number}`)
              }
            >
              As completed
            </DropdownMenuItem>
            <DropdownMenuItem
              onSelect={() =>
                change({ state: "closed", stateReason: "not_planned" }, `Closed #${issue.number}`)
              }
            >
              As not planned
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )
    ) : (
      <AbilityButton
        ability="issues"
        size="sm"
        variant="outline"
        pending={busy}
        onClick={() => change({ state: "open" }, `Reopened #${issue.number}`)}
      >
        Reopen
      </AbilityButton>
    )
  return (
    <div className="flex min-w-0 flex-col gap-6 pb-6" data-slot="github-issue">
      <BackToList label="Issues" onBack={onBack} />
      <div className="flex min-w-0 flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <h2 className="text-base font-semibold">
            <span className="numeric text-muted-foreground">#{issue.number}</span> {issue.title}
          </h2>
          <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-hint text-muted-foreground">
            <span className="inline-flex items-center gap-1">
              <IssueState issue={issue} />
              {issue.state === "open"
                ? "open"
                : issue.stateReason === "not_planned"
                  ? "closed as not planned"
                  : "closed as completed"}
            </span>
            <span>opened by {issue.author}</span>
            <span className="numeric" title={timestamp(issue.updatedAt)}>
              updated {relativeTime(issue.updatedAt)}
            </span>
          </p>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <AbilityButton
            ability="createIssues"
            size="sm"
            variant="outline"
            onClick={() => setEditing(true)}
          >
            <Pencil className="size-3.5" />
            Edit
          </AbilityButton>
          {stateControl}
          <OnGitHub href={issue.htmlUrl} />
        </div>
      </div>
      {issue.labels.length > 0 && (
        <div className="flex flex-wrap gap-1.5" aria-label="Labels">
          {issue.labels.map((l) => (
            <LabelTag key={l.name} label={l} />
          ))}
        </div>
      )}
      <div className="min-w-0">
        {issue.body?.trim() ? (
          <Markdown source={issue.body} repoUrl={repo.htmlUrl} />
        ) : (
          <p className="text-body text-muted-foreground">No description.</p>
        )}
      </div>
      {editing && (
        <IssueForm
          issue={issue}
          labels={labels.data ?? []}
          onClose={() => setEditing(false)}
          onSaved={() => {
            setEditing(false)
            read.refresh()
          }}
        />
      )}
    </div>
  )
}

/**
 * Opening or editing an issue: its title, its body, and its labels where
 * the repository lets this account label (`issues`); without that, GitHub
 * would drop them, so the field is not offered.
 */
function IssueForm({
  issue,
  labels,
  onClose,
  onSaved,
}: {
  issue?: GitHubIssue
  labels: GitHubLabel[]
  onClose: () => void
  onSaved: (issue: GitHubIssue) => void
}) {
  const { repo, path } = useRepo()
  const [title, setTitle] = useState(issue?.title ?? "")
  const [body, setBody] = useState(issue?.body ?? "")
  const [chosen, setChosen] = useState<string[]>(issue?.labels.map((l) => l.name) ?? [])
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState<string>()
  const canLabel = repo.can.issues && labels.length > 0
  const save = async () => {
    if (!title.trim() || busy) return
    setBusy(true)
    setRefused(undefined)
    try {
      let saved: GitHubIssue
      if (issue) {
        const update: GitHubIssueUpdate = {}
        if (title.trim() !== issue.title) update.title = title.trim()
        if (body !== (issue.body ?? "")) update.body = body
        const before = issue.labels.map((l) => l.name).sort()
        if (canLabel && JSON.stringify([...chosen].sort()) !== JSON.stringify(before)) {
          update.labels = chosen
        }
        saved = await patch<GitHubIssue>(path(`/issues/${issue.number}`), update)
      } else {
        const create: GitHubIssueCreate = { title: title.trim() }
        if (body.trim()) create.body = body
        if (canLabel && chosen.length > 0) create.labels = chosen
        saved = await post<GitHubIssue>(path("/issues"), create)
      }
      notify.success(issue ? `Saved #${saved.number}` : `Opened #${saved.number}`)
      onSaved(saved)
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && !busy && onClose()}
      title={issue ? `Edit #${issue.number}` : "New issue"}
      size="lg"
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={!title.trim()} pending={busy}>
            {issue ? "Save" : "Open"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label="Title" htmlFor="issue-title">
          <Input id="issue-title" value={title} onChange={(e) => setTitle(e.target.value)} />
        </Field>
        <Field label="Description" htmlFor="issue-body">
          <Textarea
            id="issue-body"
            rows={8}
            value={body}
            onChange={(e) => setBody(e.target.value)}
          />
        </Field>
        {canLabel && (
          <fieldset className="space-y-1.5">
            <legend className="pb-1 text-body font-medium">Labels</legend>
            <div className="grid max-h-48 gap-1 overflow-y-auto sm:grid-cols-2">
              {labels.map((l) => {
                const id = `issue-label-${l.name}`
                return (
                  <label key={l.name} htmlFor={id} className="flex min-w-0 items-center gap-2 py-1">
                    <Checkbox
                      id={id}
                      checked={chosen.includes(l.name)}
                      onCheckedChange={(on) =>
                        setChosen((held) =>
                          on ? [...held, l.name] : held.filter((name) => name !== l.name),
                        )
                      }
                    />
                    <LabelTag label={l} />
                  </label>
                )
              })}
            </div>
          </fieldset>
        )}
        {refused && (
          <p role="alert" className="text-body text-destructive">
            {refused}
          </p>
        )}
      </div>
    </Modal>
  )
}
