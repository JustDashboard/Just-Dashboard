"use client"

import { useState } from "react"
import { ArrowRight, LockClosed, RefreshClockwise, Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { useGitHubAccount } from "@/hooks/use-github"
import { GitHubAccountControl } from "@/components/git/github-account"
import { CredentialSelect } from "@/components/deploy/credentials-page"
import type { DeploymentDraftSource, GitHubAppRepository, GitHubRepoSummary } from "@/lib/types"
import { Field, FormNote, OptionList, OptionRow } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { SearchInput } from "@/components/page"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import Link from "next/link"
import { deploymentName } from "@/components/deploy/vocabulary"
import {
  inspectAndPrepare,
  repositoryName,
  type ConfigureFlow,
  type SourceTabKey,
} from "@/components/deploy/new-project/draft"

const SHORTCUTS: { tab: SourceTabKey; title: string; hint: string }[] = [
  { tab: "template", title: "Template", hint: "Reviewed apps with ready-to-use defaults" },
  { tab: "database", title: "Database", hint: "Postgres, MySQL, Redis, MongoDB & more" },
  { tab: "image", title: "Docker image", hint: "Run an image from any registry" },
  { tab: "compose", title: "Compose stack", hint: "An application and its services, together" },
  {
    tab: "existing",
    title: "Existing workload",
    hint: "Bring a running service into your projects",
  },
]

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/**
 * Import a repository the signed-in GitHub credential can reach, or paste any
 * Git URL. Importing inspects immediately with the default branch — the
 * branch itself is changed on Configure, once a plan exists to re-detect.
 */
/** Only a clone URL is prefilled from a link: anything else stays out of the field. */
function cloneURL(value?: string) {
  const trimmed = value?.trim() ?? ""
  return /^(https?:\/\/|ssh:\/\/|git@)[^\s]+$/.test(trimmed) ? trimmed : ""
}

/** A repository the picker can import, from either GitHub identity. */
type PickableRepo = {
  nameWithOwner: string
  name: string
  description?: string
  language?: string
  private: boolean
  cloneUrl: string
  defaultBranch: string
  credentialId?: number
  viaApp: boolean
}

export function SourceGit({
  onInspected,
  onSwitchTab,
  initialUrl,
  initialRef,
}: {
  onInspected: (flow: ConfigureFlow) => void
  onSwitchTab: (tab: SourceTabKey) => void
  initialUrl?: string
  initialRef?: string
}) {
  const status = useGitHubAccount()
  const signedIn = Boolean(status.data?.available && status.data.account?.loggedIn)
  const repos = usePoll(
    (signal) => get<GitHubRepoSummary[]>("/git/github/repos", undefined, signal),
    0,
    [signedIn],
    { enabled: signedIn },
  )
  // Repositories the dashboard's GitHub App is installed on: cloned with the
  // installation's own token, so nothing has to be signed in or pasted.
  const appRepos = usePoll(
    (signal) => get<GitHubAppRepository[]>("/deploy/github-app/repositories", undefined, signal),
    0,
  )
  const appConfigured = (appRepos.data?.length ?? 0) > 0
  const [filter, setFilter] = useState("")
  const [owner, setOwner] = useState("all")
  const [manualUrl, setManualUrl] = useState(() => cloneURL(initialUrl))
  const [manualRef, setManualRef] = useState(() => initialRef?.trim() || "main")
  const [credentialId, setCredentialId] = useState<number>()
  // Apply to whichever import fires — the picked-repo rows and the pasted
  // URL both become the same `DeploymentDraftSource` the fields already are.
  const [includeSubmodules, setIncludeSubmodules] = useState(false)
  const [includeLfs, setIncludeLfs] = useState(false)
  const [busy, setBusy] = useState("")
  const [failure, setFailure] = useState<Error>()

  const doImport = async (
    source: DeploymentDraftSource,
    name: string,
    sourceLabel: string,
    githubRepo: string | undefined,
    key: string,
  ) => {
    setBusy(key)
    setFailure(undefined)
    try {
      onInspected(await inspectAndPrepare(name, "web", source, { sourceLabel, githubRepo }))
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBusy("")
    }
  }

  const needle = filter.trim().toLowerCase()
  // One list: what the App grants first, then what the signed-in account can
  // reach and the App cannot. A repository both can reach imports through the
  // App, whose token never expires under the operator's feet.
  const appNames = new Set((appRepos.data ?? []).map((repo) => repo.nameWithOwner))
  const pickable: PickableRepo[] = [
    ...(appRepos.data ?? []).map((repo) => ({
      nameWithOwner: repo.nameWithOwner,
      name: repo.name,
      description: repo.description,
      language: repo.language,
      private: repo.private,
      cloneUrl: repo.cloneUrl,
      defaultBranch: repo.defaultBranch,
      credentialId: repo.credentialId,
      viaApp: true,
    })),
    ...(repos.data ?? [])
      .filter((repo) => !appNames.has(repo.nameWithOwner))
      .map((repo) => ({
        nameWithOwner: repo.nameWithOwner,
        name: repo.name,
        description: repo.description,
        language: repo.language,
        private: repo.private,
        cloneUrl: repo.cloneUrl,
        defaultBranch: repo.defaultBranch || "main",
        viaApp: false,
      })),
  ]
  const owners = [...new Set(pickable.map((repo) => repo.nameWithOwner.split("/")[0]))]
  const visible = pickable.filter(
    (repo) =>
      (owner === "all" || repo.nameWithOwner.split("/")[0] === owner) &&
      (!needle ||
        repo.nameWithOwner.toLowerCase().includes(needle) ||
        (repo.description ?? "").toLowerCase().includes(needle)),
  )
  const listing = (signedIn && repos.loading) || (appRepos.loading && !appRepos.data)

  return (
    <div className="grid min-w-0 items-start gap-6 lg:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
      <div className="min-w-0 space-y-4">
        {failure && <ErrorState error={failure} />}
        <Panel plain>
          <PanelHeader
            title="Import Git repository"
            actions={<GitHubAccountControl status={status} compact />}
          />
          <PanelBody className="space-y-4">
            {status.error && <ErrorState error={status.error} />}
            {repos.error && <ErrorState error={repos.error} />}
            {appRepos.error && <ErrorState error={appRepos.error} />}
            {!signedIn && !appConfigured && status.data && (
              <Notice
                tone="warning"
                icon={Warning}
                title={
                  status.data.available
                    ? "Not signed in to GitHub on this server"
                    : "The GitHub CLI is not installed on this host"
                }
              >
                {status.data.available ? (
                  <>
                    Sign in once on the{" "}
                    <Link href="/git" className="underline underline-offset-2">
                      Git page
                    </Link>{" "}
                    and every private repository becomes selectable here, or connect the{" "}
                    <Link href="/deploy/credentials" className="underline underline-offset-2">
                      GitHub App
                    </Link>{" "}
                    and every repository it is installed on appears without signing in.
                  </>
                ) : (
                  <>
                    Connect the{" "}
                    <Link href="/deploy/credentials" className="underline underline-offset-2">
                      GitHub App
                    </Link>{" "}
                    to browse repositories here. A public clone URL works without it.
                  </>
                )}
              </Notice>
            )}
            {(signedIn || appConfigured) && (
              <>
                <div className="flex flex-wrap items-center gap-2">
                  <Select value={owner} onValueChange={setOwner}>
                    <SelectTrigger aria-label="Repository owner" className="w-40">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="all">All repositories</SelectItem>
                      {owners.map((name) => (
                        <SelectItem key={name} value={name}>
                          {name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <IconAction
                    label="Refresh repositories"
                    onClick={() => {
                      repos.refresh()
                      appRepos.refresh()
                    }}
                  >
                    <RefreshClockwise />
                  </IconAction>
                </div>
                <SearchInput
                  value={filter}
                  onChange={(event) => setFilter(event.target.value)}
                  placeholder="Filter repositories"
                  aria-label="Filter repositories"
                  containerClassName="sm:w-full"
                />
                {listing && <LoadingRows rows={5} />}
                {!listing && visible.length === 0 && (
                  <EmptyNote>
                    {pickable.length === 0
                      ? "This account has no repositories the credential can list."
                      : "No repository matches that filter."}
                  </EmptyNote>
                )}
                <RowList aria-label="Repositories" className="max-h-[32rem] overflow-y-auto">
                  {visible.map((repo) => (
                    <Row
                      key={repo.nameWithOwner}
                      leading={
                        repo.private ? (
                          <LockClosed
                            className="size-3.5 text-muted-foreground"
                            aria-label="Private repository"
                          />
                        ) : undefined
                      }
                      title={repo.nameWithOwner}
                      subtitle={repo.description}
                      trailing={
                        <>
                          {repo.viaApp && <Tag>App</Tag>}
                          {repo.language && <Tag>{repo.language}</Tag>}
                          <Button
                            size="xs"
                            variant="outline"
                            aria-label={`Import ${repo.nameWithOwner}`}
                            pending={busy === repo.nameWithOwner}
                            onClick={() =>
                              void doImport(
                                {
                                  kind: "git",
                                  mode: "git_url",
                                  url: repo.cloneUrl,
                                  ref: repo.defaultBranch || "main",
                                  credentialId: repo.credentialId,
                                  includeSubmodules,
                                  includeLfs,
                                },
                                deploymentName(repo.name),
                                repo.nameWithOwner,
                                repo.nameWithOwner,
                                repo.nameWithOwner,
                              )
                            }
                          >
                            Import
                          </Button>
                        </>
                      }
                    />
                  ))}
                </RowList>
              </>
            )}

            {/* Below the repository rows (spec §5.4), not above them: this is
                the fallback for a repository the signed-in account cannot
                list, not the primary way in. */}
            <div className="space-y-3 border-t border-hairline pt-5">
              <p className="eyebrow">Or paste a Git URL</p>
              <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
                <Field label="Clone URL" htmlFor="manual-url">
                  <Input
                    id="manual-url"
                    value={manualUrl}
                    onChange={(event) => setManualUrl(event.target.value)}
                    placeholder="https://github.com/owner/repository.git"
                    className="font-mono"
                  />
                </Field>
                <div className="flex items-end">
                  <Button
                    variant="outline"
                    pending={busy === "manual"}
                    disabled={!manualUrl.trim()}
                    onClick={() =>
                      void doImport(
                        {
                          kind: "git",
                          mode: "git_url",
                          url: manualUrl.trim(),
                          ref: manualRef.trim() || "main",
                          credentialId,
                          includeSubmodules,
                          includeLfs,
                        },
                        deploymentName(repositoryName(manualUrl)),
                        manualUrl.trim(),
                        undefined,
                        "manual",
                      )
                    }
                  >
                    Import
                  </Button>
                </div>
              </div>
              <details>
                <summary className="cursor-pointer rounded-sm py-2 text-xs text-muted-foreground focus-ring">
                  Branch & authentication
                </summary>
                <div className="grid gap-3 pt-2 sm:grid-cols-2">
                  <Field label="Branch or tag" htmlFor="manual-ref">
                    <Input
                      id="manual-ref"
                      value={manualRef}
                      onChange={(event) => setManualRef(event.target.value)}
                    />
                  </Field>
                  <Field label="Credential" htmlFor="manual-credential">
                    <CredentialSelect
                      id="manual-credential"
                      kind="git"
                      value={credentialId}
                      onChange={setCredentialId}
                    />
                  </Field>
                </div>
                <OptionList className="pt-1">
                  <OptionRow
                    title="Include submodules"
                    hint="Fetch the repositories declared in .gitmodules along with this one."
                    checked={includeSubmodules}
                    onCheckedChange={setIncludeSubmodules}
                  />
                  <OptionRow
                    title="Include Git LFS files"
                    hint="Download Git LFS objects instead of leaving their pointer files."
                    checked={includeLfs}
                    onCheckedChange={setIncludeLfs}
                  />
                </OptionList>
              </details>
            </div>
          </PanelBody>
        </Panel>
      </div>
      <Panel plain>
        <PanelHeader title="Start with something ready" />
        <PanelBody flush>
          <RowList aria-label="Other ways to start">
            {SHORTCUTS.map((shortcut) => (
              <Row
                key={shortcut.tab}
                onClick={() => onSwitchTab(shortcut.tab)}
                title={shortcut.title}
                subtitle={shortcut.hint}
                trailing={<ArrowRight className="size-3.5 text-muted-foreground" />}
              />
            ))}
          </RowList>
        </PanelBody>
      </Panel>
      <FormNote className="lg:col-span-2">
        New commits to the selected branch deploy automatically after your first deployment.
      </FormNote>
    </div>
  )
}
