"use client"

import { useState } from "react"
import { BranchPlus, GitBranch, ShieldCheck, Trash } from "@/components/icons"
import { Field } from "@/components/form"
import { DimActions, IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { Spinner } from "@/components/loading"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { branchProblem } from "@/components/github/repo/model"
import {
  AbilityButton,
  Blocked,
  ListBody,
  ShowMore,
  useActReason,
  usePages,
  useRepo,
} from "@/components/github/repo/shared"
import { usePoll } from "@/hooks/use-poll"
import { del, errorMessage, get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { GitHubBranch, GitHubBranchPage, GitHubBranchProtection } from "@/lib/github-types"

/** The default first, then the rest as GitHub lists them. */
function ordered(branches: GitHubBranch[]) {
  return [...branches].sort((a, b) => Number(b.default) - Number(a.default))
}

/**
 * The repository's branches: the default and the protected ones marked, a
 * branch's protection and rules in a popover off its row, **New branch**
 * and a delete per row where the repository allows pushing. The default
 * branch is never offered for deletion; GitHub refuses it too.
 */
export function RepoBranches() {
  const { repo, path, confirm } = useRepo()
  const pages = usePages<GitHubBranch, GitHubBranchPage>(path("/branches"), {}, (p) => p.branches)
  const [creating, setCreating] = useState(false)
  const pushReason = useActReason("push", { destructive: true })
  const branches = ordered(pages.items)

  const remove = (branch: GitHubBranch) =>
    confirm({
      title: `Delete ${branch.name}`,
      confirmLabel: "Delete",
      description: (
        <p>
          Deletes the branch on GitHub. Its commits stay reachable from any branch or pull request
          that holds them; an open pull request from it is closed.
        </p>
      ),
      action: async () => {
        await del(path("/branches"), { query: { name: branch.name } })
        pages.reload()
      },
    })

  return (
    <Panel plain aria-label="Branches">
      <PanelHeader
        title="Branches"
        count={
          pages.items.length > 0 ? plural(pages.items.length, "branch", "branches") : undefined
        }
        actions={
          <AbilityButton
            ability="push"
            size="sm"
            variant="outline"
            onClick={() => setCreating(true)}
          >
            <BranchPlus className="size-3.5" />
            New branch
          </AbilityButton>
        }
      />
      <PanelBody flush>
        <ListBody pages={pages} empty="This repository has no branches yet.">
          <RowList data-slot="github-branches">
            {branches.map((branch) => {
              const reason = branch.default
                ? "The default branch cannot be deleted. Choose another default in Settings first."
                : pushReason
              return (
                <Row
                  key={branch.name}
                  data-branch={branch.name}
                  leading={<GitBranch aria-hidden className="size-4 text-muted-foreground" />}
                  title={<span className="font-mono">{branch.name}</span>}
                  trailing={
                    <>
                      {branch.default && <Tag className="text-brand">default</Tag>}
                      {branch.protected && <Protection branch={branch.name} />}
                      <span className="font-mono text-micro text-muted-foreground max-sm:hidden">
                        {branch.sha.slice(0, 7)}
                      </span>
                      <DimActions>
                        {reason ? (
                          <Blocked reason={reason}>
                            <IconAction label={`Delete ${branch.name}`} disabled>
                              <Trash />
                            </IconAction>
                          </Blocked>
                        ) : (
                          <IconAction
                            label={`Delete ${branch.name}`}
                            onClick={() => remove(branch)}
                          >
                            <Trash />
                          </IconAction>
                        )}
                      </DimActions>
                    </>
                  }
                />
              )
            })}
          </RowList>
        </ListBody>
        <ShowMore pages={pages} />
      </PanelBody>
      {creating && (
        <NewBranch
          branches={branches}
          defaultBranch={repo.defaultBranch}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false)
            pages.reload()
          }}
        />
      )}
    </Panel>
  )
}

/**
 * A protected branch's rules, read when its tag is pressed: classic
 * protection (which GitHub states only to admins; otherwise it says why)
 * and the rulesets that apply to it.
 */
function Protection({ branch }: { branch: string }) {
  const [open, setOpen] = useState(false)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={`Protection of ${branch}`}
          className="inline-flex items-center gap-1 rounded-sm text-micro font-medium text-success focus-ring hover:underline"
        >
          <ShieldCheck aria-hidden className="size-3.5" />
          <span className="caps">protected</span>
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80">
        {open && <ProtectionBody branch={branch} />}
      </PopoverContent>
    </Popover>
  )
}

function ProtectionBody({ branch }: { branch: string }) {
  const { path } = useRepo()
  const read = usePoll(
    (signal) => get<GitHubBranchProtection>(path("/branches/protection"), { name: branch }, signal),
    0,
    [branch],
  )
  const data = read.data
  if (!data) {
    return read.error ? (
      <p className="text-body text-destructive">{errorMessage(read.error)}</p>
    ) : (
      <div className="flex items-center gap-2 text-body text-muted-foreground">
        <Spinner /> Reading the rules for {branch}…
      </div>
    )
  }
  const p = data.protection
  const lines = p
    ? [
        p.requiredReviews !== undefined &&
          `${plural(p.requiredReviews, "approving review")} before merging`,
        p.requireCodeOwnerReviews && "Code owners must review",
        p.dismissStaleReviews && "New pushes dismiss approvals",
        p.requiredChecks.length > 0 && `Checks must pass: ${p.requiredChecks.join(", ")}`,
        p.strictChecks && "The branch must be up to date before merging",
        p.conversationResolution && "Conversations must be resolved",
        p.linearHistory && "History stays linear",
        p.enforceAdmins && "Administrators are held to it too",
        p.allowForcePushes && "Force pushes allowed",
        p.allowDeletions && "Deletion allowed",
      ].filter(Boolean)
    : []
  return (
    <div className="space-y-3 text-body" data-slot="github-protection">
      <p className="font-medium">
        <span className="font-mono">{data.branch}</span>
      </p>
      <section className="space-y-1">
        <h3 className="text-hint font-medium text-muted-foreground">Protection</h3>
        {!data.protectionReadable ? (
          <p className="text-muted-foreground">{data.protectionMessage}</p>
        ) : lines.length === 0 ? (
          <p className="text-muted-foreground">No classic protection rules.</p>
        ) : (
          <ul className="list-disc space-y-0.5 pl-4">
            {lines.map((line) => (
              <li key={String(line)}>{line}</li>
            ))}
          </ul>
        )}
      </section>
      <section className="space-y-1">
        <h3 className="text-hint font-medium text-muted-foreground">Rulesets</h3>
        {!data.rulesReadable ? (
          <p className="text-muted-foreground">{data.rulesMessage}</p>
        ) : data.rules.length === 0 ? (
          <p className="text-muted-foreground">No ruleset applies.</p>
        ) : (
          <ul className="space-y-0.5">
            {data.rules.map((rule, index) => (
              <li key={`${rule.type}-${index}`}>
                {rule.summary}
                {rule.rulesetName && (
                  <span className="text-muted-foreground"> · {rule.rulesetName}</span>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

function NewBranch({
  branches,
  defaultBranch,
  onClose,
  onCreated,
}: {
  branches: GitHubBranch[]
  defaultBranch: string
  onClose: () => void
  onCreated: () => void
}) {
  const { path } = useRepo()
  const [name, setName] = useState("")
  const [from, setFrom] = useState(defaultBranch)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState<string>()
  const problem = name ? branchProblem(name) : undefined
  const taken = branches.some((b) => b.name === name.trim())
  const error = problem ?? (taken ? "A branch with this name exists." : undefined)
  const names = branches.some((b) => b.name === defaultBranch)
    ? branches.map((b) => b.name)
    : [defaultBranch, ...branches.map((b) => b.name)]

  const create = async () => {
    if (!name.trim() || error || busy) return
    setBusy(true)
    setRefused(undefined)
    try {
      await post<GitHubBranch>(path("/branches"), { name: name.trim(), from })
      notify.success(`Created ${name.trim()}`)
      onCreated()
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
      title="New branch"
      size="sm"
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={create} disabled={!name.trim() || Boolean(error)} pending={busy}>
            Create
          </Button>
        </>
      }
    >
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          void create()
        }}
      >
        <Field label="Name" htmlFor="branch-name" error={error ?? refused}>
          <Input
            id="branch-name"
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label="From" htmlFor="branch-from">
          <Select value={from} onValueChange={setFrom}>
            <SelectTrigger id="branch-from" className="w-full font-mono">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {names.map((branch) => (
                <SelectItem key={branch} value={branch} className="font-mono">
                  {branch}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </form>
    </Modal>
  )
}
