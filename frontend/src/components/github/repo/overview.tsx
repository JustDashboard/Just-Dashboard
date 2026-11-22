"use client"

import { CheckCircle, Download, FolderOpen, Servers, Slash } from "@/components/icons"
import { BranchChip, ForgeFace } from "@/components/git/marks"
import { WireFlow, type WireFlowNode } from "@/components/wire/wire-flow"
import { WireMark } from "@/components/wire/wire"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import {
  abilityList,
  grantWords,
  installationWords,
  roleWord,
} from "@/components/github/repo/model"
import { LabelTag, useRepo } from "@/components/github/repo/shared"
import { plural } from "@/lib/format"
import { localGitHref } from "@/lib/github"
import type { GitRepo } from "@/lib/types"

/**
 * What the repository is and where it is: first the picture (D2) of the
 * repository on GitHub wired to its checkouts on this server, or one
 * sentence and **Clone** when there are none; then what GitHub says about
 * it; then what this page lets you do with it and why not the rest.
 */
export function RepoOverview({
  checkouts,
  checkoutsLoading,
  onClone,
}: {
  checkouts: GitRepo[]
  checkoutsLoading: boolean
  onClone: () => void
}) {
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <CheckoutPicture checkouts={checkouts} loading={checkoutsLoading} onClone={onClone} />
      <About />
      <Abilities />
    </div>
  )
}

function CheckoutPicture({
  checkouts,
  loading,
  onClone,
}: {
  checkouts: GitRepo[]
  loading: boolean
  onClone: () => void
}) {
  const { repo, controlReason } = useRepo()
  const nodes: WireFlowNode[] = checkouts.map((checkout) => ({
    id: checkout.path,
    mark: (size) => (
      <WireMark tone="logo" size={size === "lg" ? "md" : size}>
        <ProductGlyph id="git" />
      </WireMark>
    ),
    eyebrow: checkout.worktree ? "Worktree" : "Checkout",
    title: <span className="font-mono">{checkout.path}</span>,
    hint: (
      <span className="inline-flex items-center gap-1.5">
        <BranchChip branch={checkout.branch} detached={checkout.detached} />
        {checkout.dirty && <span>{plural(checkout.changes, "change")}</span>}
      </span>
    ),
    name: checkout.path,
    href: localGitHref(checkout.path),
    label: `Open ${checkout.path} in Local Git`,
    line: { state: checkout.behind > 0 || checkout.ahead > 0 ? "warning" : "up" },
  }))
  return (
    <Panel plain aria-label="On this server">
      <PanelHeader
        title="On this server"
        actions={
          checkouts.length > 0 ? (
            <span className="numeric text-hint text-muted-foreground">
              {plural(checkouts.length, "checkout")}
            </span>
          ) : undefined
        }
      />
      <PanelBody flush>
        {!loading && checkouts.length === 0 ? (
          <div
            data-slot="github-no-checkout"
            className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-3 py-2"
          >
            <WireMark tone="neutral" size="md">
              <FolderOpen aria-hidden />
            </WireMark>
            <p className="min-w-0 flex-1 text-body text-muted-foreground">
              {repo.fullName} has no checkout on this server yet.
            </p>
            {!controlReason && (
              <Button size="sm" onClick={onClone}>
                <Download className="size-3.5" />
                Clone
              </Button>
            )}
          </div>
        ) : (
          <WireFlow
            size="sm"
            phone="marks"
            loading={loading}
            start={{
              title: "On GitHub",
              nodes: [
                {
                  id: "github",
                  mark: (size) => (
                    <WireMark tone="logo" size={size === "lg" ? "md" : size}>
                      <ForgeFace
                        login={repo.owner.login}
                        provider="github"
                        className="size-full rounded-[inherit]"
                      />
                    </WireMark>
                  ),
                  eyebrow: "GitHub",
                  title: <span className="font-mono">{repo.fullName}</span>,
                  hint: repo.visibility,
                  name: repo.fullName,
                  line: { state: "up" },
                },
              ],
              loadingRows: 1,
            }}
            hub={{
              mark: (
                <WireMark tone="brand" size="lg">
                  <Servers aria-hidden />
                </WireMark>
              ),
              eyebrow: "Local Git",
              title: "This server",
              loadingLines: { title: "This server", hint: 0 },
            }}
            end={{ title: "Checkouts", nodes, loadingRows: 1 }}
          />
        )}
      </PanelBody>
    </Panel>
  )
}

function About() {
  const { repo } = useRepo()
  const topics = repo.topics ?? []
  return (
    <Panel plain aria-label="About">
      <PanelHeader title="About" />
      <PanelBody className="space-y-3">
        <p className="text-body">
          {repo.description || <span className="text-muted-foreground">No description.</span>}
        </p>
        <dl className="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-6 gap-y-2 text-body">
          {repo.homepage && (
            <>
              <dt className="text-muted-foreground">Homepage</dt>
              <dd className="min-w-0 truncate">
                <a
                  href={repo.homepage}
                  target="_blank"
                  rel="noreferrer"
                  className="font-mono text-brand hover:underline"
                >
                  {repo.homepage}
                </a>
              </dd>
            </>
          )}
          {repo.parent && (
            <>
              <dt className="text-muted-foreground">Forked from</dt>
              <dd className="min-w-0 truncate">
                <a
                  href={repo.parent.htmlUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="font-mono text-brand hover:underline"
                >
                  {repo.parent.fullName}
                </a>
              </dd>
            </>
          )}
          <dt className="text-muted-foreground">Counts</dt>
          <dd className="numeric">
            {[
              plural(repo.stars, "star"),
              plural(repo.forks, "fork"),
              plural(repo.openIssues, "open issue"),
            ].join(" · ")}
          </dd>
          {repo.isTemplate && (
            <>
              <dt className="text-muted-foreground">Template</dt>
              <dd>New repositories can be made from it.</dd>
            </>
          )}
        </dl>
        {topics.length > 0 && (
          <div className="flex flex-wrap gap-1.5" aria-label="Topics">
            {topics.map((topic) => (
              <LabelTag key={topic} label={{ name: topic, color: "" }} />
            ))}
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * What the page lets the reader do here, and why not the rest: their role,
 * what the App was installed on and granted, and each act held back with the
 * reason. The same reasons are on the controls, as tooltips; here they are
 * all in one place, on a touch screen too.
 */
function Abilities() {
  const { repo } = useRepo()
  const list = abilityList(repo.can)
  return (
    <Panel plain aria-label="What you can do here">
      <PanelHeader title="What you can do here" />
      <PanelBody className="space-y-4">
        <p className="text-body">
          You have <span className="font-medium">{roleWord(repo.role)}</span> on {repo.fullName}.{" "}
          {installationWords(repo.installation)} The App may{" "}
          {grantWords(repo.installation.permissions)}.
        </p>
        <ul className="min-w-0 divide-y divide-hairline" data-slot="github-abilities">
          {list.map((item) => (
            <li key={item.key} className="flex min-w-0 items-start gap-2.5 py-2 text-body">
              {item.allowed ? (
                <CheckCircle aria-label="allowed" className="mt-0.5 size-4 shrink-0 text-success" />
              ) : (
                <Slash
                  aria-label="not allowed"
                  className="mt-0.5 size-4 shrink-0 text-muted-foreground"
                />
              )}
              <span className="min-w-0">
                <span className={item.allowed ? "" : "text-muted-foreground"}>{item.words}</span>
                {item.reason && (
                  <span className="block text-hint text-muted-foreground">{item.reason}</span>
                )}
              </span>
            </li>
          ))}
        </ul>
        {repo.installation.settingsUrl && (
          <a
            href={repo.installation.settingsUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-block text-body font-medium text-brand hover:underline"
          >
            Change what the App reaches on GitHub
          </a>
        )}
      </PanelBody>
    </Panel>
  )
}
