"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  Check,
  Copy,
  External,
  GitHubMark,
  Key,
  Logout,
  RefreshClockwise,
  ShieldCheck,
  Wrench,
} from "@/components/icons"
import { errorMessage, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { GitHubAccount, GitHubDeviceStart, GitHubDeviceState } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useGitHubAccount } from "@/hooks/use-github"
import { useConfirm } from "@/components/confirm-dialog"
import { Notice, Spinner } from "@/components/state"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useCopy } from "@/hooks/use-copy"

/**
 * Who this dashboard is, to GitHub, in the repository being worked on.
 *
 * It sits in the workspace header rather than on a settings page because it is
 * the answer to a question asked at the moment of pushing — whose commits are
 * these, and will this push be allowed — and an account you have to go looking
 * for is one nobody checks until something fails.
 *
 * The whole thing is per repository, and the chip says so: the token lives in
 * the home of the host account that owns the checkout, which is the account
 * git runs as. That is not a detail worth hiding, because it is the difference
 * between a push that works and one that asks for a password nobody can type.
 */
export function GitHubAccountControl({
  repoPath,
  status: given,
  compact,
}: {
  /** The repository being worked on, when there is one. */
  repoPath?: string
  /**
   * A status poll the caller already runs. Passing it keeps a page that needs
   * the answer for something else — the committer line, the pull request tab —
   * to one request; leaving it out is the ordinary case, where the control is
   * the only thing asking.
   */
  status?: ReturnType<typeof useGitHubAccount>
  /** For a crowded strip: the avatar, the login, and nothing else. */
  compact?: boolean
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [signingIn, setSigningIn] = useState(false)
  const [fixing, setFixing] = useState(false)
  // The hook is always called — a conditional one would break the rules of
  // hooks — and simply does not run when the caller supplied an answer.
  const own = useGitHubAccount(repoPath, !given)
  const status = given ?? own
  const account = status.data?.account
  const canAdmin = can("system.admin")

  const useForGit = async () => {
    setFixing(true)
    try {
      await post("/git/github/auth/configure", undefined, {
        query: repoPath ? { path: repoPath } : undefined,
      })
      status.refresh()
      notify.success("git is now using this account", {
        description: "Commits are recorded as it, and pushes authenticate with its token.",
      })
    } catch (err) {
      notify.error("Could not set git up", err)
    } finally {
      setFixing(false)
    }
  }

  // gh missing is information, not a failure — the same degradation every
  // optional module gets. It is still said out loud, because the alternative
  // is a git page with no sign-in and no explanation for its absence.
  if (status.data && !status.data.available) {
    // In a crowded strip the absence of a tool nobody asked for is not worth a
    // slot of its own; the git page says it in full.
    if (compact) return null
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="inline-flex items-center gap-1.5 rounded-md border border-dashed px-2 py-1 text-hint text-muted-foreground">
            <GitHubMark className="size-3.5" />
            unavailable
          </span>
        </TooltipTrigger>
        <TooltipContent>
          The GitHub CLI (gh) is not installed on this host, so signing in and pull requests are not
          available.
        </TooltipContent>
      </Tooltip>
    )
  }

  if (!account?.loggedIn) {
    return (
      <>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              size={compact ? "sm" : "sm"}
              variant={compact ? "ghost" : "outline"}
              className={compact ? "h-6 gap-1 px-1.5 text-hint text-muted-foreground" : undefined}
              disabled={!canAdmin || !status.data}
              onClick={() => setSigningIn(true)}
            >
              <GitHubMark className={compact ? "size-3.5" : "size-4"} />
              Sign in
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            {canAdmin
              ? "Sign in to GitHub so pushes and pull requests come from your account"
              : "Signing in to GitHub needs the system.admin capability"}
          </TooltipContent>
        </Tooltip>
        <SignInDialog
          open={signingIn}
          onOpenChange={setSigningIn}
          repoPath={repoPath}
          owner={account?.owner}
          onDone={() => status.refresh()}
        />
      </>
    )
  }

  const initials = (account.login ?? "?").slice(0, 2).toUpperCase()

  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              size="sm"
              variant={compact ? "ghost" : "outline"}
              className={cn(
                "gap-1.5 pr-2 pl-1.5",
                compact && "h-6 gap-1 pr-1.5 pl-1 text-hint font-normal",
              )}
            >
              <Avatar size="sm" className={compact ? "size-4" : "size-5"}>
                {account.avatarUrl && <AvatarImage src={account.avatarUrl} alt="" />}
                <AvatarFallback className="text-micro">{initials}</AvatarFallback>
              </Avatar>
              <span className={compact ? "max-w-[7rem] truncate" : "max-w-[10rem] truncate"}>
                {account.login}
              </span>
              {!account.gitConfigured && <span className="size-1.5 rounded-full bg-warning" />}
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>
          {account.gitConfigured
            ? `Commits and pushes from this page are made as ${account.login}`
            : "Signed in, but git here is not set up to use the account"}
        </TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuLabel className="space-y-1 font-normal">
          <p className="text-body font-medium">{account.name || account.login}</p>
          <p className="text-hint text-muted-foreground">
            {account.login} on {account.host}
            {account.owner ? ` · for the ${account.owner} account` : ""}
          </p>
          {account.committerEmail && (
            <p className="truncate font-mono text-micro text-muted-foreground">
              {account.committerName} &lt;{account.committerEmail}&gt;
            </p>
          )}
        </DropdownMenuLabel>
        {account.scopes && account.scopes.length > 0 && (
          <div className="flex flex-wrap gap-1 px-2 pb-1.5">
            {account.scopes.map((s) => (
              <Tag key={s} mono>
                {s}
              </Tag>
            ))}
          </div>
        )}
        {!account.gitConfigured ? (
          <div className="space-y-1.5 px-2 pb-1.5">
            <p className="text-hint text-muted-foreground">
              {!account.committerEmail
                ? "git here has no name and address, so it cannot record a commit as you yet."
                : "git here is not set up to hand the token to a push yet."}
            </p>
            <Button
              size="sm"
              className="w-full"
              disabled={fixing || !canAdmin}
              onClick={useForGit}
              pending={fixing}
            >
              <Wrench className="size-3.5" />
              Use this account for git
            </Button>
          </div>
        ) : (
          account.remoteProtocol === "ssh" && (
            <p className="px-2 pb-1.5 text-hint text-muted-foreground">
              This repository pushes over SSH, so the push uses this server&apos;s key. Commits are
              recorded as the account, and pull requests are opened as it.
            </p>
          )
        )}
        <DropdownMenuSeparator />
        {account.profileUrl && (
          <DropdownMenuItem asChild>
            <a href={account.profileUrl} target="_blank" rel="noreferrer">
              <External className="size-3.5" />
              View profile on GitHub
            </a>
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onSelect={() => status.refresh()}>
          <RefreshClockwise className="size-3.5" />
          Re-check
        </DropdownMenuItem>
        {canAdmin && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              className="text-destructive"
              onSelect={() =>
                confirm({
                  title: `Sign out of GitHub`,
                  confirmLabel: "Sign out",
                  description: (
                    <p>
                      Removes the stored token for{" "}
                      <span className="font-medium">{account.login}</span>
                      {account.owner ? ` on the ${account.owner} account` : ""}. Pushes from this
                      page will stop being authenticated until somebody signs in again.
                    </p>
                  ),
                  action: async () => {
                    await post("/git/github/auth/logout", undefined, {
                      query: { path: repoPath, host: account.host },
                    })
                    status.refresh()
                    notify.success("Signed out of GitHub")
                  },
                })
              }
            >
              <Logout className="size-3.5" />
              Sign out
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
      {dialog}
    </DropdownMenu>
  )
}

type Stage =
  | { kind: "intro" }
  | { kind: "code"; start: GitHubDeviceStart }
  | { kind: "done"; account?: GitHubAccount }

/**
 * `gh auth login`, as a page.
 *
 * The CLI's own flow is a series of prompts, which a web request has nobody to
 * answer — so the dashboard performs the same OAuth device flow itself and
 * hands gh the finished token. What the operator sees is what gh shows: a
 * one-time code, and the page on github.com to type it into. The token itself
 * never reaches this browser; the server stores it where gh keeps its own.
 *
 * The second tab is for everything the device flow cannot reach — a GitHub
 * Enterprise host, a fine-grained token, a machine account.
 */
function SignInDialog({
  open,
  onOpenChange,
  repoPath,
  owner,
  onDone,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  repoPath?: string
  owner?: string
  onDone: () => void
}) {
  const [stage, setStage] = useState<Stage>({ kind: "intro" })
  const [mode, setMode] = useState<"code" | "token">("code")
  const [token, setToken] = useState("")
  const [host, setHost] = useState("github.com")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const { copy, copied } = useCopy()
  const [remaining, setRemaining] = useState(0)
  // The callback is closed over by the poll below, which must not restart on
  // every render of the parent, so it is kept in a ref synced after render
  // rather than assigned during one.
  const doneRef = useRef(onDone)
  useEffect(() => {
    doneRef.current = onDone
  })

  // Closing resets the flow, so a dialog reopened after a failed attempt
  // starts where it started the first time — a half-finished flow left on
  // screen is how somebody ends up typing an expired code.
  const change = (next: boolean) => {
    if (!next) {
      setStage({ kind: "intro" })
      setError(undefined)
      setToken("")
    }
    onOpenChange(next)
  }

  const start = useCallback(async () => {
    setBusy(true)
    setError(undefined)
    try {
      const res = await post<GitHubDeviceStart>("/git/github/auth/device", undefined, {
        query: repoPath ? { path: repoPath } : undefined,
      })
      setStage({ kind: "code", start: res })
      setRemaining(res.expiresIn)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }, [repoPath])

  // The poll is a chain of timeouts rather than an interval: an interval keeps
  // firing while a slow answer is still in flight, which is exactly what
  // GitHub answers by slowing the whole flow down.
  useEffect(() => {
    if (stage.kind !== "code") return
    let alive = true
    let timer: ReturnType<typeof setTimeout>
    const tick = async () => {
      try {
        const res = await post<GitHubDeviceState>(
          `/git/github/auth/device/${stage.start.id}`,
          undefined,
        )
        if (!alive) return
        if (res.status === "complete") {
          setStage({ kind: "done", account: res.account })
          doneRef.current()
          notify.success(`Signed in to GitHub as ${res.account?.login ?? "your account"}`)
          return
        }
        if (res.status !== "pending") {
          setError(res.message ?? "The sign-in did not complete.")
          setStage({ kind: "intro" })
          return
        }
      } catch (err) {
        if (!alive) return
        setError(errorMessage(err))
        setStage({ kind: "intro" })
        return
      }
      timer = setTimeout(tick, stage.start.interval * 1000)
    }
    timer = setTimeout(tick, stage.start.interval * 1000)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [stage])

  useEffect(() => {
    if (stage.kind !== "code") return
    const timer = setInterval(() => setRemaining((r) => Math.max(0, r - 1)), 1000)
    return () => clearInterval(timer)
  }, [stage])

  const signInWithToken = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const acc = await post<GitHubAccount>(
        "/git/github/auth/token",
        { token: token.trim(), host: host.trim() },
        { query: repoPath ? { path: repoPath } : undefined },
      )
      setStage({ kind: "done", account: acc })
      onDone()
      notify.success(`Signed in to GitHub as ${acc.login ?? "your account"}`)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={change}
      title="Sign in to GitHub"
      description={
        <>
          Commits and pushes made from this page will be yours, and pull requests can be opened from
          here.
          {owner
            ? ` The credential is stored for the ${owner} account, which owns this repository.`
            : ""}
        </>
      }
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={() => change(false)}>
            {stage.kind === "done" ? "Close" : "Cancel"}
          </Button>
        </>
      }
    >
      {error && (
        <Notice title="Sign-in failed" tone="danger">
          {error}
        </Notice>
      )}

      {stage.kind === "done" ? (
        <div className="space-y-3 py-2 text-center">
          <ShieldCheck className="mx-auto size-8 text-success" />
          <div>
            <p className="text-body font-medium">
              Signed in as {stage.account?.login ?? "your account"}
            </p>
            <p className="text-xs text-muted-foreground">
              git here now uses the account for pushes, and records commits as{" "}
              {stage.account?.committerName || stage.account?.login}.
            </p>
          </div>
        </div>
      ) : stage.kind === "code" ? (
        <div className="space-y-3">
          <div className="space-y-2">
            <Label>Your one-time code</Label>
            <div className="flex items-center justify-center rounded-lg border border-hairline bg-surface-sunken py-3">
              <code className="font-mono text-2xl tracking-[0.35em]">{stage.start.userCode}</code>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" size="sm" onClick={() => copy(stage.start.userCode)}>
                {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                {copied ? "Copied" : "Copy code"}
              </Button>
              <Button size="sm" asChild>
                <a href={stage.start.verificationUri} target="_blank" rel="noreferrer">
                  <External className="size-3.5" />
                  Open github.com and enter it
                </a>
              </Button>
            </div>
          </div>
          <div className="flex items-center gap-2 rounded-lg border border-hairline bg-surface-header/60 px-3 py-2 text-xs text-muted-foreground">
            <Spinner className="size-3.5" />
            Waiting for you to authorise the GitHub CLI…
            {remaining > 0 && (
              <span className="numeric ml-auto font-mono">
                {Math.floor(remaining / 60)}:{String(remaining % 60).padStart(2, "0")}
              </span>
            )}
          </div>
          <p className="text-hint text-muted-foreground">
            This is the same flow as <span className="font-mono">gh auth login</span>. The code
            expires if it is not entered, and nothing is stored until you approve it.
          </p>
        </div>
      ) : mode === "code" ? (
        <div className="space-y-3">
          <ol className="space-y-1.5 text-body text-muted-foreground">
            <li>1. We ask GitHub for a one-time code.</li>
            <li>2. You enter it on github.com and approve the GitHub CLI.</li>
            <li>3. The token is stored on this server, for this repository&apos;s account.</li>
          </ol>
          <Button className="w-full" onClick={start} pending={busy}>
            <GitHubMark className="size-4" />
            Get a one-time code
          </Button>
          <button
            className="w-full text-center text-hint text-muted-foreground hover:text-foreground hover:underline"
            onClick={() => setMode("token")}
          >
            Use a token instead — for GitHub Enterprise, or a machine account
          </button>
        </div>
      ) : (
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="gh-token">Personal access token</Label>
            <Input
              id="gh-token"
              type="password"
              autoComplete="off"
              spellCheck={false}
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="ghp_…"
              className="font-mono"
            />
            <p className="text-hint text-muted-foreground">
              Needs the <span className="font-mono">repo</span> scope, and{" "}
              <span className="font-mono">workflow</span> to push changes under{" "}
              <span className="font-mono">.github/workflows</span>.
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="gh-host">Host</Label>
            <Input
              id="gh-host"
              value={host}
              onChange={(e) => setHost(e.target.value)}
              placeholder="github.com"
              className="font-mono"
            />
          </div>
          <div className="flex gap-2">
            <Button
              className="flex-1"
              disabled={busy || !token.trim()}
              onClick={signInWithToken}
              pending={busy}
            >
              <Key className="size-4" />
              Sign in with token
            </Button>
            <Button variant="ghost" onClick={() => setMode("code")}>
              Back
            </Button>
          </div>
        </div>
      )}
    </Modal>
  )
}
