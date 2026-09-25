"use client"

import { useEffect, useRef, useState, type RefObject } from "react"
import Link from "next/link"
import { CheckCircle, Copy, External, GitHubMark, Plus, Trash, Warning } from "@/components/icons"
import { del, errorMessage, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { calendarDate, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useMediaQuery } from "@/hooks/use-mobile"
import { githubAppStage, useGitHubApp, type GitHubAppStage } from "@/hooks/use-github"
import type {
  DeploymentCredential,
  GitHubAppInstallation,
  GitHubAppManifestStart,
  GitHubAppStatus,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { Field, FormFact } from "@/components/form"
import { LogoGlyph } from "@/components/logo"
import { ProductLogo } from "@/components/product-logo"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ForgeFace } from "@/components/git/marks"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, type Verb } from "@/components/verbs"
import { Segment } from "@/components/deploy/run-pipeline"
import { StepMark } from "@/components/deploy/vocabulary"
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
 *
 * Its state is said here and nowhere else on the page: the header's `Status`,
 * and the setup path under the picture. The Credentials readings used to
 * carry it as a third statement of the same fact.
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

function hostOf(url: string | undefined) {
  if (!url) return undefined
  try {
    return new URL(url).host
  } catch {
    return undefined
  }
}

/**
 * How many projects read their source through a credential. `usedBy` counts
 * environments, so a project deploying two of them through one token is one
 * project; the ids say which, where the server sent them.
 */
export function projectsUsing(credential: DeploymentCredential) {
  return credential.usedByProjectIds?.length ?? credential.usedBy
}

/** The credential an installation mints its clone tokens through. */
function credentialOf(
  installation: GitHubAppInstallation,
  credentials: DeploymentCredential[] | undefined,
) {
  return credentials?.find((credential) => credential.id === installation.credentialId)
}

/**
 * A GitHub account, drawn as its own picture on a square — the face
 * `/deploy/new` draws the same account with (`ForgeFace`), because a filled
 * circle holding a letter is the pill §4 deleted. Until the picture arrives,
 * and wherever it cannot (a network that does not reach GitHub, the mocked
 * API), the account's initials stand in the hue its name is given everywhere
 * else, so `acme` keeps one colour on the Git tab, in this picture, on the
 * credential it mints and in the repository chooser.
 */
export function AccountFace({ account, className }: { account: string; className?: string }) {
  return (
    <span className={cn("relative flex shrink-0 overflow-hidden", className)}>
      <ForgeFace
        login={account}
        provider="github"
        size="sm"
        className="size-full rounded-[inherit]"
      />
    </span>
  )
}

/**
 * The picture: the accounts that installed the App, the App, and this
 * server, with the traffic between them drawn as lines. What moves on a line
 * is the state (§2) — dotted before the thing exists, still while it is not
 * installed or while nothing deploys through it, a pulse once a project's
 * source is read through it — so "is this working" is answered before a word
 * is read, per account rather than for the App as a whole.
 *
 * The lines are measured from the marks, so the layout can change under them:
 * three columns wide, one column narrow. Wide, the three sit as one
 * composition in the middle of the frame rather than at its far edges.
 */
function Picture({
  stage,
  data,
  admin,
  credentials,
}: {
  stage: GitHubAppStage
  data: GitHubAppStatus
  admin: boolean
  credentials?: DeploymentCredential[]
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const container = useRef<HTMLDivElement>(null)
  const appMark = useRef<HTMLDivElement>(null)
  const serverMark = useRef<HTMLDivElement>(null)
  const accountMark = useRef<HTMLDivElement>(null)

  const app = data.app
  const host = hostOf(data.webhookUrl)
  const carries = (installation: GitHubAppInstallation) =>
    (credentialOf(installation, credentials)?.usedBy ?? 0) > 0
  const live = stage === "import" && data.installations.some(carries)
  const dashed = stage === "create"
  // Wide, the two directions between the App and the server are two lines a
  // little apart; narrow, they would run down the same column, so one is drawn.
  const spread = wide ? 9 : 0
  const more = admin && data.installUrl

  return (
    <div ref={container} className="relative mx-auto max-w-5xl">
      <AnimatedBeam
        containerRef={container}
        fromRef={appMark}
        toRef={serverMark}
        still={!live}
        dashed={dashed}
        startYOffset={-spread}
        endYOffset={-spread}
      />
      {wide && (
        <AnimatedBeam
          containerRef={container}
          fromRef={appMark}
          toRef={serverMark}
          still={!live}
          dashed={dashed}
          reverse
          delay={1.4}
          startYOffset={spread}
          endYOffset={spread}
        />
      )}

      <div className="flex flex-col gap-6 lg:grid lg:min-h-40 lg:grid-cols-[minmax(0,1fr)_minmax(3rem,0.5fr)_auto_minmax(13.5rem,0.6fr)_minmax(0,1fr)] lg:items-center lg:gap-0">
        <ul
          className="flex flex-col gap-4 lg:items-end lg:gap-5"
          aria-label="GitHub App installations"
        >
          {data.installations.map((installation, index) => (
            <AccountNode
              key={installation.id}
              installation={installation}
              credential={credentialOf(installation, credentials)}
              containerRef={container}
              appRef={appMark}
              wide={wide}
              delay={index * 0.35}
            />
          ))}
          {/* The spot where the next account goes: the only account before
              any has installed, a smaller one after the list once some have.
              That smaller one is drawn wide only: in one column the line from
              an account to the App would run through it, reading as a chain,
              and the strip's button and the menu already carry the verb. */}
          {(stage !== "import" || (more && wide)) && (
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
              ? // A creation date is a date, not an age: "53d 23h ago" asked
                // the reader to do the subtraction back to the day it was.
                `owned by ${app.owner || "you"} · created ${calendarDate(app.createdAt)}`
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

/**
 * One installed account, and its line to the App. It owns the ref its line is
 * measured from. The line pulses only while a project's source is read through
 * this installation's credential: an account that installed the App and
 * deploys nothing through it is connected, and still.
 */
function AccountNode({
  installation,
  credential,
  containerRef,
  appRef,
  wide,
  delay,
}: {
  installation: GitHubAppInstallation
  credential?: DeploymentCredential
  containerRef: RefObject<HTMLDivElement | null>
  appRef: RefObject<HTMLDivElement | null>
  wide: boolean
  delay: number
}) {
  const mark = useRef<HTMLDivElement>(null)
  const projects = credential ? projectsUsing(credential) : 0
  return (
    <li className="min-w-0">
      <AnimatedBeam
        containerRef={containerRef}
        fromRef={mark}
        toRef={appRef}
        still={projects === 0}
        delay={delay}
      />
      <WireNode
        nodeRef={mark}
        align="end"
        mark={
          // Three accounts at 44px were two hundred pixels of a phone before
          // the App; the picture is the same at 36.
          <AccountFace
            account={installation.account}
            className={cn(
              "border border-hairline",
              wide ? "size-11 rounded-xl" : "size-9 rounded-lg",
            )}
          />
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
        hint={
          // Two lines on purpose: what the installation is, then what deploys
          // through it — one string wrapped wherever the column ended.
          <>
            <span className="block">
              {installation.accountType.toLowerCase()} ·{" "}
              {installation.repositorySelection === "all"
                ? "every repository"
                : "chosen repositories"}
            </span>
            {credential && (
              <span className="block">
                {projects > 0
                  ? `${plural(projects, "project")} deploy through it`
                  : "nothing deployed yet"}
              </span>
            )}
          </>
        }
      />
    </li>
  )
}

type PathState = "passed" | "warning" | "current" | "pending"

const PATH_WORD: Record<PathState, string> = {
  passed: "Done",
  warning: "Waiting on you",
  current: "Next",
  pending: "Not yet",
}

/**
 * The three steps from nothing to deploying, drawn the way the release path
 * and the dashboard's own restarts draw a run of stages: a segment of rule per
 * step, coloured by where the App is, its name under it. The step that waits
 * on GitHub's install screen is amber — the header says "Connected, not
 * installed" in the same colour — and the one that is simply next is the
 * brand's location mark.
 *
 * It replaced numbered filled circles, which is the pill §4 deleted and §16
 * names for exactly this shape. The last step is done once a project's
 * source reads through the App, not when the App merely could be used.
 */
function SetupPath({ stage, deploying }: { stage: GitHubAppStage; deploying: boolean }) {
  const steps = [
    { key: "create", label: "Create the App" },
    { key: "install", label: "Install it on an account" },
    { key: "import", label: deploying ? "Repositories deploy through it" : "Import a repository" },
  ]
  const at = steps.findIndex((step) => step.key === stage)
  return (
    <ol
      aria-label="GitHub App setup"
      className="grid min-w-0 flex-1 gap-x-3 lg:max-w-xl"
      style={{ gridTemplateColumns: "repeat(3, minmax(0, 1fr))" }}
    >
      {steps.map((step, index) => {
        const state: PathState =
          index < at || (index === 2 && deploying)
            ? "passed"
            : index > at
              ? "pending"
              : stage === "install"
                ? "warning"
                : "current"
        return (
          <li
            key={step.key}
            aria-current={state === "current" || state === "warning" ? "step" : undefined}
            className="flex min-w-0 flex-col gap-2"
          >
            {state === "current" ? (
              <span className="relative block h-1.5 w-full rounded-full bg-brand" />
            ) : (
              <Segment state={state} />
            )}
            {/* Wrapped rather than truncated: three steps across a phone are
                a hundred pixels each, and "Install it on an a…" is not one. */}
            <span className="flex min-w-0 items-start gap-1.5">
              {state === "current" ? (
                <span
                  aria-hidden
                  className="mt-0.5 flex size-3.5 shrink-0 items-center justify-center"
                >
                  <span className="size-1.5 rounded-full bg-brand" />
                </span>
              ) : (
                <StepMark state={state} className="mt-0.5" />
              )}
              <span
                className={cn(
                  "min-w-0 text-xs leading-snug font-medium",
                  state === "pending" && "text-muted-foreground",
                  state === "warning" && "text-warning",
                )}
              >
                {step.label}
              </span>
              <span className="sr-only">{PATH_WORD[state]}</span>
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
  credentials,
  onChange,
}: {
  admin: boolean
  /** The page polls the App once and shares the reading with its credential marks. */
  status: ReturnType<typeof useGitHubApp>
  /**
   * The page's saved credentials: an installation's own credential says how
   * many projects deploy through that account, which is what its line draws.
   */
  credentials?: DeploymentCredential[]
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
      subject: status.data?.app && {
        mark: <ProductLogo id="github" size="sm" />,
        name: status.data.app.name,
        facts: (
          <FormFact label="Installed on">
            {plural(status.data.installations.length, "account")}
          </FormFact>
        ),
      },
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

  const data = status.data
  const stage = githubAppStage(data)
  const installations = data?.installations ?? []
  const deploying = installations.some(
    (installation) => (credentialOf(installation, credentials)?.usedBy ?? 0) > 0,
  )

  // Declared once, and drawn by the header's menu (§13). The copy button
  // beside the server's address stays, because that is where the address is
  // read.
  const verbs: Verb[] = []
  if (data?.app) {
    const htmlUrl = data.app.htmlUrl
    verbs.push({
      key: "open",
      label: "Open on GitHub",
      icon: External,
      run: () => window.open(htmlUrl, "_blank", "noreferrer"),
    })
  }
  if (admin && data?.installUrl && stage !== "create") {
    const installUrl = data.installUrl
    verbs.push({
      key: "install",
      label: "Install on another account",
      icon: Plus,
      run: () => window.open(installUrl, "_blank", "noreferrer"),
    })
  }
  if (data?.webhookUrl) {
    const webhookUrl = data.webhookUrl
    verbs.push({
      key: "copy",
      label: "Copy webhook address",
      icon: Copy,
      run: () => void copyText(webhookUrl, "Webhook address copied"),
    })
  }
  if (admin) {
    verbs.push({
      key: "disconnect",
      label: "Disconnect",
      icon: Trash,
      danger: true,
      run: disconnect,
    })
  }

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
            icon={outcome.tone === "success" ? CheckCircle : Warning}
            title={outcome.tone === "success" ? "Connected" : "Not connected"}
          >
            {outcome.text}
          </Notice>
        )}
        {status.error && <ErrorState error={status.error} onRetry={status.refresh} />}
        {status.loading && !data && <LoadingRows rows={2} />}
        {data?.error && (
          <Notice tone="warning" icon={Warning} title="GitHub is not answering for this App">
            {data.error}
          </Notice>
        )}

        {stage && data && (
          // Framed on purpose, against the page's default of readings on a
          // bare ground: this is a picture of three things and the lines
          // between them, and a picture needs an edge to be read as one.
          <div className="animate-rise overflow-hidden rounded-xl border bg-card">
            <div className="px-5 py-6 sm:px-6 sm:py-7 lg:px-8 lg:py-10">
              <Picture stage={stage} data={data} admin={admin} credentials={credentials} />
            </div>

            <div className="border-t border-hairline px-5 py-4 sm:px-6">
              {/* The picture's own measure, so the path and its command sit
                  under the drawing rather than at the frame's far edges. */}
              <div className="mx-auto flex w-full max-w-5xl flex-col gap-5 lg:flex-row lg:items-center lg:justify-between lg:gap-10">
                <SetupPath stage={stage} deploying={deploying} />

                {stage === "create" &&
                  (admin ? (
                    <Field
                      label="Organisation"
                      htmlFor="github-app-organization"
                      hint="Leave empty to create it on your account."
                      className="lg:shrink-0"
                    >
                      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                        {/* The slug is an account's path on GitHub, so the field
                            says so: the prefix and the name read as one address. */}
                        <InputGroup className="sm:w-64">
                          <InputGroupAddon className="gap-0 border-r-0 pr-0">
                            {/* At the field's own size, so the two read as one path. */}
                            <InputGroupText className="text-base sm:text-body">
                              github.com/
                            </InputGroupText>
                          </InputGroupAddon>
                          <InputGroupInput
                            id="github-app-organization"
                            value={organization}
                            onChange={(event) => setOrganization(event.target.value)}
                            placeholder="acme"
                            autoComplete="off"
                            spellCheck={false}
                            className="pl-0"
                          />
                        </InputGroup>
                        <Button
                          pending={starting}
                          onClick={() => void create()}
                          className="max-sm:h-11 max-sm:w-full sm:shrink-0"
                        >
                          <GitHubMark className="size-4" />
                          Create GitHub App
                        </Button>
                      </div>
                    </Field>
                  ) : (
                    <p className="text-body text-muted-foreground">
                      An administrator creates the App from this page.
                    </p>
                  ))}

                {stage === "install" && data.installUrl && (
                  <Button asChild className="max-sm:h-11 sm:shrink-0">
                    <a href={data.installUrl} target="_blank" rel="noreferrer">
                      Install on GitHub
                      <External className="size-3.5" aria-hidden />
                    </a>
                  </Button>
                )}

                {/* Installing and importing are an administrator's. */}
                {stage === "import" && admin && (
                  <div className="flex flex-wrap items-center gap-2 lg:shrink-0">
                    {data.installUrl && (
                      <Button asChild size="sm" variant="outline">
                        <a href={data.installUrl} target="_blank" rel="noreferrer">
                          <Plus className="size-3.5" />
                          Another account
                        </a>
                      </Button>
                    )}
                    {/* The command face while the last step is still next; once
                        a project deploys through the App nothing here waits on
                        the reader, and the page's one command is its own. */}
                    <Button asChild size="sm" variant={deploying ? "outline" : "default"}>
                      <Link href="/deploy/new">Import a repository</Link>
                    </Button>
                  </div>
                )}
              </div>
            </div>
          </div>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}
