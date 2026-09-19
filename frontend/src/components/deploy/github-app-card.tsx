"use client"

import { useEffect, useState } from "react"
import { External, Trash } from "@/components/icons"
import { del, errorMessage, get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"
import type { GitHubAppManifestStart, GitHubAppStatus } from "@/lib/types"
import { Field, FormFact, FormFacts, FormSection } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, type Verb } from "@/components/verbs"

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
export function useGitHubApp() {
  return usePoll((signal) => get<GitHubAppStatus>("/deploy/github-app/", undefined, signal), 30000)
}

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

  return (
    <Panel plain>
      <PanelHeader
        title="GitHub App"
        actions={
          status.data?.configured && verbs.length > 0 ? (
            <VerbActions verbs={verbs} menuLabel="Actions for the GitHub App" />
          ) : undefined
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
        {status.loading && !status.data && <LoadingRows rows={2} />}
        {status.data && !status.data.configured && (
          <>
            <p className="max-w-prose text-body leading-relaxed text-muted-foreground">
              One App on your account or organisation delivers every push, clones private
              repositories with its own short-lived tokens and writes each preview&apos;s state on
              the pull request — no webhook secret or personal token per repository.
            </p>
            {admin ? (
              <Field
                label="Organisation"
                htmlFor="github-app-organization"
                hint="Leave empty to create the App on your personal account."
              >
                {/* The button sits on the input's own line, so the two read as
                    one control: pressed against the field's hint instead, it
                    hung a line lower than the box it belonged to. */}
                <div className="flex flex-col gap-2 sm:flex-row">
                  <Input
                    id="github-app-organization"
                    value={organization}
                    onChange={(event) => setOrganization(event.target.value)}
                    placeholder="acme"
                    autoComplete="off"
                    className="sm:max-w-xs"
                  />
                  <Button pending={starting} onClick={() => void create()} className="sm:shrink-0">
                    Create GitHub App
                  </Button>
                </div>
              </Field>
            ) : (
              <EmptyNote>An administrator connects the App from this page.</EmptyNote>
            )}
          </>
        )}
        {status.data?.configured && status.data.app && (
          <>
            {status.data.error && (
              <Notice tone="warning" title="GitHub is not answering for this App">
                {status.data.error}
              </Notice>
            )}
            <FormFacts>
              <FormFact label="App">
                <a
                  href={status.data.app.htmlUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 rounded-sm underline underline-offset-4 focus-ring"
                >
                  {status.data.app.name}
                  <External className="size-3" aria-hidden />
                </a>
              </FormFact>
              <FormFact label="Owner">{status.data.app.owner || "—"}</FormFact>
              <FormFact label="Created">{relativeTime(status.data.app.createdAt)}</FormFact>
              {status.data.webhookUrl && (
                <FormFact label="Webhook" mono>
                  {status.data.webhookUrl}
                </FormFact>
              )}
            </FormFacts>
            <FormSection
              title="Installations"
              actions={
                status.data.installUrl && (
                  <Button asChild size="xs" variant="outline">
                    <a href={status.data.installUrl} target="_blank" rel="noreferrer">
                      Install on another account
                      <External className="size-3" aria-hidden />
                    </a>
                  </Button>
                )
              }
            >
              {status.data.installations.length === 0 ? (
                <EmptyNote>
                  Not installed anywhere yet. Install it on the account whose repositories deploy
                  here; the repositories then appear in the import list.
                </EmptyNote>
              ) : (
                <RowList aria-label="GitHub App installations">
                  {status.data.installations.map((installation) => (
                    <Row
                      key={installation.id}
                      title={installation.account}
                      subtitle={
                        installation.repositorySelection === "all"
                          ? "Every repository on the account"
                          : "Selected repositories"
                      }
                      trailing={
                        <>
                          <Tag>{installation.accountType}</Tag>
                          <Button asChild size="xs" variant="ghost">
                            <a href={installation.htmlUrl} target="_blank" rel="noreferrer">
                              Configure on GitHub
                            </a>
                          </Button>
                        </>
                      }
                    />
                  ))}
                </RowList>
              )}
            </FormSection>
          </>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}
