"use client"

import { useEffect, useState } from "react"
import { External, Trash } from "@/components/icons"
import { del, get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"
import type { GitHubAppManifestStart, GitHubAppStatus } from "@/lib/types"
import { Field, FormFact, FormFacts } from "@/components/form"
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

function readCallbackOutcome(): CallbackOutcome | undefined {
  if (typeof window === "undefined") return undefined
  const query = new URLSearchParams(window.location.search)
  const result = query.get("github-app")
  if (!result) return undefined
  if (result === "connected") {
    return {
      tone: "success",
      text: "The GitHub App is connected. Install it on the accounts whose repositories deploy here.",
    }
  }
  return {
    tone: "warning",
    text: `GitHub did not finish creating the App: ${query.get("reason") ?? "unknown reason"}`,
  }
}

export function GitHubAppCard({ admin }: { admin: boolean }) {
  const status = useGitHubApp()
  const [organization, setOrganization] = useState("")
  const [starting, setStarting] = useState(false)
  // GitHub sends the browser back with the result in the query string: read
  // once when the card first renders, then the address is cleaned so a
  // reload does not repeat it.
  const [outcome] = useState(readCallbackOutcome)
  const { confirm, dialog } = useConfirm()

  useEffect(() => {
    if (!outcome || typeof window === "undefined") return
    const query = new URLSearchParams(window.location.search)
    query.delete("github-app")
    query.delete("reason")
    const rest = query.toString()
    window.history.replaceState(null, "", window.location.pathname + (rest ? `?${rest}` : ""))
  }, [outcome])

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
      onDone: () => status.refresh(),
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
            <p className="text-sm text-muted-foreground">
              One App, installed on your GitHub account or organisation, replaces a webhook secret
              per repository, a personal token for private clones, and reading a run log to find a
              preview address: the App delivers every push, clones with its own short-lived tokens,
              and writes each preview&apos;s state on the pull request.
            </p>
            {admin ? (
              <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
                <Field
                  label="Organisation"
                  htmlFor="github-app-organization"
                  hint="Leave empty to create the App on your personal account."
                >
                  <Input
                    id="github-app-organization"
                    value={organization}
                    onChange={(event) => setOrganization(event.target.value)}
                    placeholder="acme"
                  />
                </Field>
                <Button pending={starting} onClick={() => void create()}>
                  Create GitHub App
                </Button>
              </div>
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
            <div className="flex flex-wrap items-center justify-between gap-2">
              <p className="eyebrow">Installations</p>
              {status.data.installUrl && (
                <Button asChild size="xs" variant="outline">
                  <a href={status.data.installUrl} target="_blank" rel="noreferrer">
                    Install on another account
                    <External className="size-3" aria-hidden />
                  </a>
                </Button>
              )}
            </div>
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
                    title={
                      <span className="inline-flex min-w-0 items-center gap-2">
                        <span className="truncate">{installation.account}</span>
                        <Tag>{installation.accountType}</Tag>
                      </span>
                    }
                    subtitle={
                      installation.repositorySelection === "all"
                        ? "Every repository on the account"
                        : "Selected repositories"
                    }
                    trailing={
                      <Button asChild size="xs" variant="ghost">
                        <a href={installation.htmlUrl} target="_blank" rel="noreferrer">
                          Configure on GitHub
                        </a>
                      </Button>
                    }
                  />
                ))}
              </RowList>
            )}
          </>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}
