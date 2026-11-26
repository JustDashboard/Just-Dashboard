"use client"

import {
  ArrowUpRight,
  BookOpen,
  CheckCircle,
  Download,
  Eye,
  FolderOpen,
  Link as LinkGlyph,
  LockClosed,
  Servers,
  ShieldCheck,
  Slash,
  Star,
} from "@/components/icons"
import { IssueMark, SourceFork } from "@/components/git/glyphs"
import { LanguageBar, LanguageList } from "@/components/git/languages"
import { BranchChip, ForgeFace, ShortSha } from "@/components/git/marks"
import { WireFlow, type WireFlowNode } from "@/components/wire/wire-flow"
import { WireMark } from "@/components/wire/wire"
import { Markdown } from "@/components/markdown"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  abilityList,
  grantWords,
  installationWords,
  roleWord,
} from "@/components/github/repo/model"
import { absoluteReadme, languageShares } from "@/components/github/repo/readme-model"
import { LabelTag, useRepo } from "@/components/github/repo/shared"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { compactCount, plural, relativeTime, timestamp } from "@/lib/format"
import { localGitHref } from "@/lib/github"
import type { GitHubCommitPage, GitHubLanguages, GitHubReadme } from "@/lib/github-types"
import type { GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * The repository as GitHub opens it: the code's front page in the main
 * column and what the repository is in a narrow one beside it.
 *
 * First, across both, the picture (D2) of the repository on GitHub wired to
 * its checkouts on this server, or one sentence and **Clone** when there are
 * none. Then the main column: the README, headed by the last commit on the
 * default branch, drawn as GitHub draws it, and what this page lets you do
 * here and why not the rest. The side column is **About** — the description, the
 * homepage, the topics, the counts by their glyphs — and the languages as
 * GitHub's bar in Linguist's colours, with each language's own logo.
 *
 * On a phone the side column comes first, as GitHub's own app puts it: what
 * a repository is, before a README that may run for screens.
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
    <div
      data-slot="github-repo-overview"
      className="grid min-w-0 gap-8 pb-6 lg:grid-cols-[minmax(0,1fr)_15rem] xl:gap-x-10"
    >
      {/* The picture runs across both columns: its three stations need the
          width, and beside the rail a half-width one broke every path. */}
      <div className="min-w-0 lg:col-span-2">
        <CheckoutPicture checkouts={checkouts} loading={checkoutsLoading} onClone={onClone} />
      </div>
      <aside
        aria-label="About this repository"
        className="flex min-w-0 flex-col gap-6 lg:col-start-2 lg:row-start-2"
      >
        <About />
        <Languages />
      </aside>
      <div className="flex min-w-0 flex-col gap-8 lg:col-start-1 lg:row-start-2">
        <Readme />
        <Abilities />
      </div>
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

/** The README pane's least height while it is read, so the page under it holds still (§11). */
const README_WAITING = "min-h-64"

/**
 * The README, as GitHub shows it under a repository's files: a pane headed
 * by the last commit on the default branch — who, what, its short sha and
 * when — and the file's name, then the Markdown drawn on this product's
 * ladder. Its pictures and links beside it are made whole first
 * (`absoluteReadme`), so a screenshot in the repository is drawn and a link
 * to `docs/` goes to GitHub. A repository with none says so in one line.
 */
function Readme() {
  const { repo, path } = useRepo()
  const readme = usePoll((signal) => get<GitHubReadme>(path("/readme"), undefined, signal), 0, [
    repo.fullName,
    repo.defaultBranch,
  ])
  const commits = usePoll(
    (signal) =>
      get<GitHubCommitPage>(path("/commits"), { sha: repo.defaultBranch, page: 1 }, signal),
    0,
    [repo.fullName, repo.defaultBranch],
  )
  const last = commits.data?.commits[0]
  const data = readme.data
  return (
    <section
      aria-label="README"
      data-slot="github-readme"
      className="min-w-0 overflow-hidden rounded-xl border border-hairline"
    >
      <div className="flex h-12 min-w-0 items-center gap-2.5 border-b border-hairline bg-surface-header px-3.5 text-hint">
        {last ? (
          <>
            <ForgeFace login={last.author.login} provider="github" size="md" />
            <span className="shrink-0 font-medium text-foreground">
              {last.author.login ?? last.author.name}
            </span>
            <a
              href={last.htmlUrl}
              target="_blank"
              rel="noopener noreferrer"
              title={last.message}
              className="min-w-0 truncate rounded-sm text-muted-foreground focus-ring hover:text-foreground hover:underline"
            >
              {last.message.split("\n")[0]}
            </a>
            <span className="ml-auto flex shrink-0 items-center gap-2 text-muted-foreground">
              {last.verified && (
                <ShieldCheck aria-label="Verified" className="size-3.5 text-success" />
              )}
              <ShortSha sha={last.sha} className="max-sm:hidden" />
              {last.author.date && (
                <time dateTime={last.author.date} title={timestamp(last.author.date)}>
                  {relativeTime(last.author.date)}
                </time>
              )}
            </span>
          </>
        ) : commits.loading && !commits.error ? (
          <Skeleton className="h-4 w-2/3" />
        ) : (
          <span className="text-muted-foreground">No commits yet</span>
        )}
      </div>
      <div className="flex h-10 min-w-0 items-center gap-2 px-3.5 text-body font-medium">
        <BookOpen aria-hidden className="size-4 text-muted-foreground" />
        <span className="min-w-0 truncate">{data?.found ? (data.path ?? "README") : "README"}</span>
        {data?.found && data.htmlUrl && (
          <a
            href={data.htmlUrl}
            target="_blank"
            rel="noopener noreferrer"
            aria-label="The README on GitHub"
            className="ml-auto flex shrink-0 items-center gap-1 rounded-sm text-hint font-normal text-muted-foreground focus-ring hover:text-foreground"
          >
            <ArrowUpRight aria-hidden className="size-3.5" />
            GitHub
          </a>
        )}
      </div>
      <div className={cn("min-w-0 px-5 pt-2 pb-6", !data && README_WAITING)}>
        {data?.found && data.content ? (
          <>
            <Markdown
              source={absoluteReadme(data.content, repo.fullName, repo.defaultBranch, data.path)}
              repoUrl={repo.htmlUrl}
            />
            {data.truncated && (
              <p className="pt-4 text-hint text-muted-foreground">
                The rest is on GitHub: the README is longer than the page reads.
              </p>
            )}
          </>
        ) : data?.found && data.truncated ? (
          <p className="py-4 text-body text-muted-foreground">
            The README is too long to read here; it is on GitHub.
          </p>
        ) : data?.found ? (
          <p className="py-4 text-body text-muted-foreground">
            {data.path ?? "The README"} is empty.
          </p>
        ) : data ? (
          <p className="py-4 text-body text-muted-foreground">{repo.fullName} has no README yet.</p>
        ) : readme.error ? (
          <p className="py-4 text-body text-muted-foreground">
            GitHub did not hand over the README.{" "}
            <button
              type="button"
              onClick={readme.refresh}
              className="rounded-sm font-medium text-foreground focus-ring hover:underline"
            >
              Try again
            </button>
          </p>
        ) : (
          <div aria-hidden className="space-y-3 pt-2">
            <Skeleton className="h-7 w-1/3" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-5/6" />
            <Skeleton className="h-24 w-full rounded-lg" />
          </div>
        )}
      </div>
    </section>
  )
}

/**
 * What the repository is, in GitHub's About: the description in the body's
 * size, the homepage, the topics as GitHub's chips, and the counts, each by
 * its own glyph, then the facts that change how it is used (a template, a
 * fork, its default branch).
 */
function About() {
  const { repo } = useRepo()
  const topics = repo.topics ?? []
  return (
    <section aria-label="About" className="flex min-w-0 flex-col gap-3">
      <h2 className="text-title font-semibold">About</h2>
      <p className={cn("text-body text-pretty", !repo.description && "text-muted-foreground")}>
        {repo.description || "No description."}
      </p>
      {repo.homepage && (
        <a
          href={repo.homepage}
          target="_blank"
          rel="noopener noreferrer"
          className="flex min-w-0 items-center gap-2 rounded-sm text-body font-medium text-brand focus-ring hover:underline"
        >
          <LinkGlyph aria-hidden className="size-4 shrink-0" />
          <span className="truncate">{repo.homepage.replace(/^https?:\/\//, "")}</span>
        </a>
      )}
      {topics.length > 0 && (
        <div className="flex flex-wrap gap-1.5" aria-label="Topics">
          {topics.map((topic) => (
            <LabelTag key={topic} label={{ name: topic, color: "" }} />
          ))}
        </div>
      )}
      <ul className="numeric space-y-2 pt-1 text-body text-muted-foreground">
        <AboutFact icon={Star}>
          <strong className="font-medium text-foreground">{compactCount(repo.stars)}</strong>{" "}
          {repo.stars === 1 ? "star" : "stars"}
        </AboutFact>
        <AboutFact icon={SourceFork}>
          <strong className="font-medium text-foreground">{compactCount(repo.forks)}</strong>{" "}
          {repo.forks === 1 ? "fork" : "forks"}
        </AboutFact>
        <AboutFact icon={IssueMark}>
          <strong className="font-medium text-foreground">{compactCount(repo.openIssues)}</strong>{" "}
          open {repo.openIssues === 1 ? "issue" : "issues"}
        </AboutFact>
        <AboutFact icon={repo.visibility === "public" ? Eye : LockClosed}>
          {repo.visibility === "public"
            ? "Public"
            : repo.visibility === "internal"
              ? "Internal"
              : "Private"}
          {" · "}
          <span className="font-mono">{repo.defaultBranch}</span>
        </AboutFact>
        {repo.isTemplate && (
          <AboutFact icon={CheckCircle}>A template for new repositories</AboutFact>
        )}
        {repo.parent && (
          <AboutFact icon={SourceFork}>
            Forked from{" "}
            <a
              href={repo.parent.htmlUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="rounded-sm font-mono text-foreground focus-ring hover:underline"
            >
              {repo.parent.fullName}
            </a>
          </AboutFact>
        )}
      </ul>
    </section>
  )
}

function AboutFact({
  icon: Glyph,
  children,
}: {
  icon: React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>
  children: React.ReactNode
}) {
  return (
    <li className="flex min-w-0 items-center gap-2">
      <Glyph aria-hidden className="size-4 shrink-0" />
      <span className="min-w-0 truncate">{children}</span>
    </li>
  )
}

/**
 * What it is written in: GitHub's bar in Linguist's colours, the one strip a
 * forge draws for a whole, and under it each language by its own logo and
 * share. Nothing is drawn while GitHub has measured nothing; a read GitHub
 * refused says so in one line, with Try again, rather than waiting on.
 */
function Languages() {
  const { repo, path } = useRepo()
  const read = usePoll((signal) => get<GitHubLanguages>(path("/languages"), undefined, signal), 0, [
    repo.fullName,
  ])
  const shares = languageShares(read.data)
  if (read.data && shares.length === 0) return null
  return (
    <section
      aria-label="Languages"
      data-slot="github-languages"
      className="flex min-w-0 flex-col gap-3 border-t border-hairline pt-5"
    >
      <h2 className="text-title font-semibold">Languages</h2>
      {read.data ? (
        <>
          <LanguageBar languages={shares} className="h-2" />
          <LanguageList languages={shares} max={6} className="flex-wrap gap-y-2" />
        </>
      ) : read.error ? (
        <p className="text-body text-muted-foreground" data-slot="github-languages-failed">
          GitHub did not hand over the languages.{" "}
          <button
            type="button"
            onClick={read.refresh}
            className="rounded-sm font-medium text-foreground focus-ring hover:underline"
          >
            Try again
          </button>
        </p>
      ) : (
        <div aria-hidden className="space-y-3">
          <Skeleton className="h-2 w-full rounded-full" />
          <Skeleton className="h-4 w-3/4" />
        </div>
      )}
    </section>
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
