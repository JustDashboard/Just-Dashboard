"use client"

import { useEffect, useRef, useState, type RefObject } from "react"
import Link from "next/link"
import { Check, Copy, External, GitHubMark, Plus, Trash } from "@/components/icons"
import { del, errorMessage, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useMediaQuery } from "@/hooks/use-mobile"
import { githubAppStage, useGitHubApp, type GitHubAppStage } from "@/hooks/use-github"
import type { GitHubAppInstallation, GitHubAppManifestStart, GitHubAppStatus } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Field } from "@/components/form"
import { LogoGlyph } from "@/components/logo"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, type Verb } from "@/components/verbs"
import { WireLabel, WireLink, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"

/**
 * The dashboard's own identity on GitHub. Created once through GitHub's
 * manifest flow — the page posts a form to GitHub from the operator's
 * browser, GitHub creates the App and sends the browser back — and installed
 * on whichever accounts own the repositories that deploy here.
 *
 * Once it exists, three manual steps disappear: no webhook and secret per
 * repository, no personal token for private clones and statuses, and the
 * preview address arrives on the pull request itself.
 */
/** Posts the manifest to GitHub the way GitHub requires: a form, from the browser. */
function submitManifest(start: GitHubAppManifestStart) {
  const form = document.createElement("form")
  form.method = "post"
  form.action = start.action
  const field = document.createElement("input")
  field.type = "hidden"
  field.name = "manifest"
  field.value = JSON.stringify(start.manifest)
  form.appendChild(field)
  document.body.appendChild(form)
  form.submit()
}

type CallbackOutcome = { tone: "success" | "warning"; text: string }

/**
 * GitHub sends the browser back to this page with a one-time code and the
 * state the dashboard issued. Both come off the address before anything
 * else happens, so a reload or a shared link cannot replay them, and the
 * exchange is finished under the page's own session — the redirect itself is
 * a cross-site navigation, on which the browser withholds the session cookie.
 */
function takeCallback(): { code: string; state: string } | undefined {
  if (typeof window === "undefined") return undefined
  const query = new URLSearchParams(window.location.search)
  const code = query.get("code")
  const state = query.get("state")
  if (!code || !state) return undefined
  query.delete("code")
  query.delete("state")
  const rest = query.toString()
  window.history.replaceState(null, "", window.location.pathname + (rest ? `?${rest}` : ""))
  return { code, state }
}

/** GitHub serves every account's picture at a fixed address; no API call needed. */
function avatarUrl(account: string) {
  return `https://github.com/${encodeURIComponent(account)}.png?size=80`
}

function hostOf(url: string | undefined) {
  if (!url) return undefined
  try {
    return new URL(url).host
  } catch {
    return undefined
  }
}

/**
 * The picture: the accounts that installed the App, the App, and this
 * server, with the traffic between them drawn as lines. What moves on the
 * lines is the state — nothing before the App exists, a still line while
 * nothing is installed, a pulse once repositories can reach here — so
 * "is this working" is answered before a word is read.
 *
 * The lines are measured from the marks, so the layout can change under them:
 * three columns wide, one column narrow.
 */
function Picture({
  stage,
  data,
  admin,
}: {
  stage: GitHubAppStage
  data: GitHubAppStatus
  admin: boolean
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const container = useRef<HTMLDivElement>(null)
  const appMark = useRef<HTMLDivElement>(null)
  const serverMark = useRef<HTMLDivElement>(null)
  const accountMark = useRef<HTMLDivElement>(null)

  const app = data.app
  const host = hostOf(data.webhookUrl)
  const live = stage === "import"
  const still = !live
  const dashed = stage === "create"
  // Wide, the two directions between the App and the server are two lines a
  // little apart; narrow, they would run down the same column, so one is drawn.
  const spread = wide ? 9 : 0
  const more = admin && data.installUrl

  return (
    <div ref={container} className="relative">
      <AnimatedBeam
        containerRef={container}
        fromRef={appMark}
        toRef={serverMark}
        still={still}
        dashed={dashed}
        startYOffset={-spread}
        endYOffset={-spread}
      />
      {wide && (
        <AnimatedBeam
          containerRef={container}
          fromRef={appMark}
          toRef={serverMark}
          still={still}
          dashed={dashed}
          reverse
          delay={1.4}
          startYOffset={spread}
          endYOffset={spread}
        />
      )}

      <div className="flex flex-col gap-7 lg:grid lg:min-h-40 lg:grid-cols-[minmax(0,1fr)_minmax(3rem,0.5fr)_auto_minmax(13.5rem,0.6fr)_minmax(0,1fr)] lg:items-center lg:gap-0">
        <ul className="flex flex-col gap-5 lg:items-end" aria-label="GitHub App installations">
          {data.installations.map((installation, index) => (
            <AccountNode
              key={installation.id}
              installation={installation}
              containerRef={container}
              appRef={appMark}
              still={still}
              delay={index * 0.35}
            />
          ))}
          {/* The spot where the next account goes: the only account before
              any has installed, a smaller one after the list once some have. */}
          {(stage !== "import" || more) && (
            <li className="min-w-0">
              <AnimatedBeam
                containerRef={container}
                fromRef={accountMark}
                toRef={appMark}
                still
                dashed={stage !== "import"}
              />
              {stage === "import" ? (
                <WireNode
                  nodeRef={accountMark}
                  align="end"
                  mark={
                    <WireLink href={data.installUrl} label="Install on another account">
                      <WirePlaceholder size="sm">
                        <Plus className="size-3.5" />
                      </WirePlaceholder>
                    </WireLink>
                  }
                  title={
                    <span className="text-hint font-normal text-muted-foreground">
                      Another account
                    </span>
                  }
                />
              ) : (
                <WireNode
                  nodeRef={accountMark}
                  align="end"
                  mark={
                    <WireLink
                      href={stage === "install" && more ? data.installUrl : undefined}
                      label="Install on GitHub"
                    >
                      <WirePlaceholder>
                        <Plus className="size-4" />
                      </WirePlaceholder>
                    </WireLink>
                  }
                  eyebrow="Accounts"
                  title={<span className="text-muted-foreground">Nobody yet</span>}
                  hint={
                    stage === "install"
                      ? "Install it on the account that owns the repositories; GitHub asks which ones it may read"
                      : "Installed once the App exists"
                  }
                />
              )}
            </li>
          )}
        </ul>

        <div aria-hidden className="hidden lg:block" />

        <WireNode
          nodeRef={appMark}
          align="center"
          mark={
            app ? (
              <WireMark tone="ink">
                <GitHubMark className="size-8" />
              </WireMark>
            ) : (
              <WirePlaceholder>
                <GitHubMark className="size-6" />
              </WirePlaceholder>
            )
          }
          eyebrow="GitHub App"
          title={
            app ? (
              <a
                href={app.htmlUrl}
                target="_blank"
                rel="noreferrer"
                className="inline-flex max-w-full items-center gap-1 rounded-sm focus-ring hover:underline"
              >
                <span className="truncate">{app.name}</span>
                <External className="size-3 shrink-0 text-muted-foreground" aria-hidden />
              </a>
            ) : (
              <span className="text-muted-foreground">No App yet</span>
            )
          }
          hint={
            app
              ? `owned by ${app.owner || "you"} · created ${relativeTime(app.createdAt)}`
              : "one App in place of a token and a webhook per repository"
          }
        />

        {/* What travels each way, written along the two lines it labels. */}
        <div aria-hidden className="relative hidden self-stretch lg:block">
          <WireLabel lit={live} className="bottom-1/2 mb-4">
            push · pull request · clone
          </WireLabel>
          <WireLabel lit={live} className="top-1/2 mt-4">
            commit status · preview comment
          </WireLabel>
        </div>

        <WireNode
          nodeRef={serverMark}
          align="start"
          mark={
            <WireMark tone="brand" shape="square">
              <LogoGlyph className="h-7" />
            </WireMark>
          }
          eyebrow="This server"
          title={<span className="break-all">{host ?? "Just Dashboard"}</span>}
          hint={
            data.webhookUrl ? (
              <span className="inline-flex max-w-full items-center gap-1">
                <span className="truncate font-mono">/api/v1/hooks/github-app</span>
                <button
                  type="button"
                  aria-label="Copy webhook address"
                  onClick={() => void copyText(data.webhookUrl ?? "", "Webhook address copied")}
                  className="rounded-sm p-0.5 focus-ring transition-colors hover:text-foreground"
                >
                  <Copy className="size-3" />
                </button>
              </span>
            ) : (
              "receives every push"
            )
          }
        />
      </div>
    </div>
  )
}

/** One installed account, and its line to the App. It owns the ref its line is measured from. */
function AccountNode({
  installation,
  containerRef,
  appRef,
  still,
  delay,
}: {
  installation: GitHubAppInstallation
  containerRef: RefObject<HTMLDivElement | null>
  appRef: RefObject<HTMLDivElement | null>
  still: boolean
  delay: number
}) {
  const mark = useRef<HTMLDivElement>(null)
  return (
    <li className="min-w-0">
      <AnimatedBeam
        containerRef={containerRef}
        fromRef={mark}
        toRef={appRef}
        still={still}
        delay={delay}
      />
      <WireNode
        nodeRef={mark}
        align="end"
        mark={
          <Avatar className="size-11 border border-hairline">
            <AvatarImage src={avatarUrl(installation.account)} alt="" />
            <AvatarFallback className="bg-plot-brand text-sm font-semibold text-brand uppercase">
              {installation.account.slice(0, 1)}
            </AvatarFallback>
          </Avatar>
        }
        title={
          <a
            href={installation.htmlUrl}
            target="_blank"
            rel="noreferrer"
            className="rounded-sm focus-ring hover:underline"
          >
            {installation.account}
          </a>
        }
        hint={`${installation.accountType.toLowerCase()} · ${
          installation.repositorySelection === "all" ? "every repository" : "chosen repositories"
        }`}
      />
    </li>
  )
}

/** The three steps: a filled line up to where you are. */
function Rail({ stage }: { stage: GitHubAppStage }) {
  const steps: { key: GitHubAppStage; label: string }[] = [
    { key: "create", label: "Create the App" },
    { key: "install", label: "Install it on an account" },
    { key: "import", label: "Import a repository" },
  ]
  const current = steps.findIndex((step) => step.key === stage)
  return (
    <ol className="flex flex-wrap items-center gap-x-3 gap-y-2" aria-label="GitHub App setup">
      {steps.map((step, index) => {
        const state = index < current ? "done" : index === current ? "current" : "next"
        return (
          <li key={step.key} className="flex items-center gap-3">
            {index > 0 && (
              <span
                aria-hidden
                className={cn(
                  "hidden h-px w-8 sm:block",
                  state === "next" ? "bg-border" : "bg-success",
                )}
              />
            )}
            <span
              aria-current={state === "current" ? "step" : undefined}
              className="flex items-center gap-2"
            >
              <span
                aria-hidden
                className={cn(
                  "numeric flex size-5 items-center justify-center rounded-full text-micro font-semibold",
                  state === "done" && "bg-plot-success text-success",
                  state === "current" && "bg-brand text-brand-foreground",
                  state === "next" && "bg-muted text-muted-foreground",
                )}
              >
                {state === "done" ? <Check className="size-3" /> : index + 1}
              </span>
              <span
                className={cn(
                  "text-xs",
                  state === "current" ? "font-medium" : "text-muted-foreground",
                )}
              >
                {step.label}
              </span>
            </span>
          </li>
        )
      })}
    </ol>
  )
}

export function GitHubAppPanel({
  admin,
  status,
  onChange,
}: {
  admin: boolean
  /** The page polls the App once and shares the reading with its tiles. */
  status: ReturnType<typeof useGitHubApp>
  /** Disconnecting removes the App's own credentials; the list beside it re-reads. */
  onChange?: () => void
}) {
  const [organization, setOrganization] = useState("")
  const [starting, setStarting] = useState(false)
  const [outcome, setOutcome] = useState<CallbackOutcome>()
  const { confirm, dialog } = useConfirm()

  // Runs once per arrival: the first pass takes the code and state off the
  // address, so a re-run (a poll re-render, StrictMode's second mount) finds
  // nothing and does nothing.
  useEffect(() => {
    const pending = takeCallback()
    if (!pending) return
    post<{ slug: string }>("/deploy/github-app/callback", pending)
      .then((app) => {
        setOutcome({
          tone: "success",
          text: `${app.slug} is connected. Install it on the accounts whose repositories deploy here.`,
        })
        status.refresh()
        onChange?.()
      })
      .catch((error) => {
        setOutcome({
          tone: "warning",
          text: `GitHub created the App, but the dashboard could not finish connecting it: ${errorMessage(error)}`,
        })
      })
  }, [status, onChange])

  const create = async () => {
    setStarting(true)
    try {
      const start = await post<GitHubAppManifestStart>("/deploy/github-app/manifest", {
        organization: organization.trim() || undefined,
      })
      submitManifest(start)
    } catch (error) {
      notify.error("Could not start the GitHub App setup", error)
      setStarting(false)
    }
  }

  const disconnect = () =>
    confirm({
      title: "Disconnect the GitHub App",
      confirmLabel: "Disconnect",
      description: (
        <p>
          Deliveries through the App stop, and repositories imported through it can no longer be
          cloned until it is connected again. The App itself stays on GitHub until its owner deletes
          it there.
        </p>
      ),
      action: async () => {
        await del("/deploy/github-app/")
      },
      onDone: () => {
        status.refresh()
        onChange?.()
      },
    })

  const verbs: Verb[] = admin
    ? [
        {
          key: "disconnect",
          label: "Disconnect",
          detail: "Forget the App's key and secrets on this server.",
          icon: Trash,
          danger: true,
          run: disconnect,
        },
      ]
    : []

  const data = status.data
  const stage = githubAppStage(data)
  const installations = data?.installations ?? []

  return (
    <Panel plain>
      <PanelHeader
        title="GitHub App"
        actions={
          <>
            {stage && (
              <Status
                tone={stage === "create" ? "stopped" : stage === "install" ? "warning" : "running"}
                label={
                  stage === "create"
                    ? "Not connected"
                    : stage === "install"
                      ? "Connected, not installed"
                      : `Installed on ${plural(installations.length, "account")}`
                }
              />
            )}
            {data?.configured && verbs.length > 0 && (
              <VerbActions verbs={verbs} menuLabel="Actions for the GitHub App" />
            )}
          </>
        }
      />
      <PanelBody className="space-y-4">
        {outcome && (
          <Notice
            tone={outcome.tone}
            title={outcome.tone === "success" ? "Connected" : "Not connected"}
          >
            {outcome.text}
          </Notice>
        )}
        {status.error && <ErrorState error={status.error} onRetry={status.refresh} />}
        {status.loading && !data && <LoadingRows rows={2} />}
        {data?.error && (
          <Notice tone="warning" title="GitHub is not answering for this App">
            {data.error}
          </Notice>
        )}

        {stage && data && (
          // Framed on purpose, against the page's default of readings on a
          // bare ground: this is a picture of three things and the lines
          // between them, and a picture needs an edge to be read as one.
          <div className="animate-rise overflow-hidden rounded-xl border bg-card">
            <div className="px-6 py-7 lg:px-8 lg:py-10">
              <Picture stage={stage} data={data} admin={admin} />
            </div>

            <div className="flex flex-col gap-4 border-t border-hairline px-6 py-4 lg:flex-row lg:items-center lg:justify-between lg:gap-8">
              <Rail stage={stage} />

              {stage === "create" &&
                (admin ? (
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
                    <Field
                      label="Organisation"
                      htmlFor="github-app-organization"
                      hint="Leave empty to create it on your account."
                      className="sm:w-56"
                    >
                      <Input
                        id="github-app-organization"
                        value={organization}
                        onChange={(event) => setOrganization(event.target.value)}
                        placeholder="acme"
                        autoComplete="off"
                      />
                    </Field>
                    {/* mb matches the field's hint line so the button sits on the input's row. */}
                    <Button
                      pending={starting}
                      onClick={() => void create()}
                      className="sm:mb-6 sm:shrink-0"
                    >
                      <GitHubMark className="size-4" />
                      Create GitHub App
                    </Button>
                  </div>
                ) : (
                  <p className="text-body text-muted-foreground">
                    An administrator creates the App from this page.
                  </p>
                ))}

              {stage === "install" && data.installUrl && (
                <Button asChild className="sm:shrink-0">
                  <a href={data.installUrl} target="_blank" rel="noreferrer">
                    Install on GitHub
                    <External className="size-3.5" aria-hidden />
                  </a>
                </Button>
              )}

              {stage === "import" && (
                <div className="flex flex-wrap items-center gap-2">
                  {admin && data.installUrl && (
                    <Button asChild size="sm" variant="outline">
                      <a href={data.installUrl} target="_blank" rel="noreferrer">
                        <Plus className="size-3.5" />
                        Another account
                      </a>
                    </Button>
                  )}
                  <Button asChild size="sm">
                    <Link href="/deploy/new">Import a repository</Link>
                  </Button>
                </div>
              )}
            </div>
          </div>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}
