"use client"

import {
  Building,
  Link as LinkGlyph,
  Location,
  Plus,
  RefreshClockwise,
  Users,
} from "@/components/icons"
import { plural } from "@/lib/format"
import type { GitHubConnection, GitHubInstallation, GitHubProfile } from "@/lib/github-types"
import { cn } from "@/lib/utils"
import { ForgeFace } from "@/components/git/marks"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { blogHref, blogWords } from "@/components/github/home/model"

/**
 * The connected account, as the head of a GitHub profile's left column: its
 * face, its name over its login, its bio, the people around it and where it
 * is, each fact only when GitHub has it.
 *
 * On a phone the face and the name share a row and the facts run on one
 * wrapped line under the bio, so the account is one compact block over the
 * calendar rather than a screen of its own.
 */
export function ProfileColumn({
  connection,
  profile,
  className,
}: {
  connection: GitHubConnection
  profile?: GitHubProfile
  className?: string
}) {
  const name = profile?.name ?? connection.name
  const blog = blogHref(profile?.blog)
  const facts: { key: string; icon: typeof Users; node: React.ReactNode }[] = []
  if (profile?.company)
    facts.push({ key: "company", icon: Building, node: <span>{profile.company}</span> })
  if (profile?.location)
    facts.push({ key: "location", icon: Location, node: <span>{profile.location}</span> })
  if (blog)
    facts.push({
      key: "blog",
      icon: LinkGlyph,
      node: (
        <a
          href={blog}
          target="_blank"
          rel="noopener noreferrer"
          className="truncate rounded-sm text-foreground focus-ring hover:underline"
        >
          {blogWords(blog)}
        </a>
      ),
    })

  return (
    <aside
      aria-label={`@${connection.login} on GitHub`}
      data-slot="github-profile"
      className={cn("flex min-w-0 flex-col gap-4 lg:gap-5", className)}
    >
      <div className="flex min-w-0 items-center gap-4 lg:flex-col lg:items-start">
        <a
          href={profile?.htmlUrl ?? connection.htmlUrl}
          target="_blank"
          rel="noopener noreferrer"
          aria-label={`@${connection.login} on GitHub`}
          className="shrink-0 rounded-full focus-ring"
        >
          <ForgeFace
            login={connection.login}
            provider="github"
            account
            className="size-16 border border-hairline sm:size-20 lg:size-28"
          />
        </a>
        <div className="min-w-0 space-y-0.5">
          {name && (
            <p className="truncate text-lg leading-6 font-semibold tracking-tight">{name}</p>
          )}
          <p
            className={cn(
              "truncate font-mono text-muted-foreground",
              name ? "text-body" : "text-lg leading-6 text-foreground",
            )}
          >
            {connection.login}
          </p>
        </div>
      </div>

      {/* On a phone this is one block of one height whatever GitHub says —
          the facts on one line, the bio in at most two under them — so the
          calendar below does not move when the profile lands (§11). On a desk
          the column stands beside the main one and runs to its own length. */}
      <div className="flex min-w-0 flex-col gap-2 max-lg:h-17 lg:gap-5">
        {profile ? (
          <>
            {profile.bio && (
              <p className="text-body leading-5 text-pretty max-lg:order-last max-lg:line-clamp-2">
                {profile.bio}
              </p>
            )}
            <div className="flex min-w-0 gap-x-4 gap-y-2 text-body leading-5 text-muted-foreground max-lg:h-5 max-lg:overflow-hidden lg:flex-col">
              <p className="numeric flex shrink-0 items-center gap-2">
                <Users aria-hidden className="size-4 shrink-0" />
                <span>
                  <span className="font-medium text-foreground">{profile.followers}</span>{" "}
                  {profile.followers === 1 ? "follower" : "followers"} ·{" "}
                  <span className="font-medium text-foreground">{profile.following}</span> following
                </span>
              </p>
              {/* A phone's one line holds the people and the first fact; the
                  rest, cut to a letter each, said nothing there. */}
              {facts.map(({ key, icon: Glyph, node }, index) => (
                <p
                  key={key}
                  className={cn(
                    "flex max-w-full min-w-0 items-center gap-2",
                    index > 0 && "max-lg:hidden",
                  )}
                >
                  <Glyph aria-hidden className="size-4 shrink-0" />
                  <span className="min-w-0 truncate">{node}</span>
                </p>
              ))}
            </div>
          </>
        ) : (
          <div className="space-y-2" aria-hidden>
            <Skeleton className="h-5 w-40" />
            <Skeleton className="h-5 w-28" />
          </div>
        )}
      </div>
    </aside>
  )
}

/**
 * What the App can reach for this account: each installation as the face of
 * the account it is on, its login, and all or selected repositories, with
 * where that is changed on GitHub. Installing on another account and reading
 * the access again close it. It stands under the profile on a desk and at
 * the foot of the page on a phone, where it is the least asked-for thing.
 */
export function AccessList({
  connection,
  installUrl,
  refreshing,
  onRefresh,
  className,
}: {
  connection: GitHubConnection
  installUrl?: string
  refreshing: boolean
  onRefresh: () => void
  className?: string
}) {
  const count = connection.installations.length
  return (
    <section
      role="group"
      aria-label="What the App can reach"
      data-slot="github-access"
      className={cn("min-w-0 space-y-2 border-t border-hairline pt-4", className)}
    >
      <h2 className="flex items-baseline justify-between gap-2 text-body font-semibold">
        Reaches
        <span className="numeric text-hint font-normal text-muted-foreground">
          {plural(count, "account")}
        </span>
      </h2>
      {count === 0 ? (
        <p className="text-body text-muted-foreground">Not installed on any account yet</p>
      ) : (
        <ul className="min-w-0 space-y-1">
          {connection.installations.map((installation) => (
            <AccessEntry key={installation.id} installation={installation} />
          ))}
        </ul>
      )}
      <div className="flex flex-wrap items-center gap-1 pt-1">
        {installUrl && (
          <Button size="xs" variant="ghost" asChild>
            <a href={installUrl} target="_blank" rel="noreferrer">
              <Plus className="size-3.5" />
              Install
            </a>
          </Button>
        )}
        <Button size="xs" variant="ghost" onClick={onRefresh} pending={refreshing}>
          <RefreshClockwise className="size-3.5" />
          Refresh access
        </Button>
      </div>
    </section>
  )
}

function AccessEntry({ installation }: { installation: GitHubInstallation }) {
  const all = installation.repositorySelection === "all"
  return (
    <li data-slot="github-installation" className="flex h-8 min-w-0 items-center gap-2.5 text-body">
      <ForgeFace login={installation.account.login} provider="github" size="md" />
      <span className="min-w-0 truncate font-mono">{installation.account.login}</span>
      <span className="shrink-0 text-hint text-muted-foreground">
        {installation.suspended ? (
          <span className="text-warning">Suspended</span>
        ) : all ? (
          "All"
        ) : (
          "Selected"
        )}
      </span>
      <a
        href={installation.settingsUrl}
        target="_blank"
        rel="noreferrer"
        aria-label={`Change what ${installation.account.login} grants`}
        className="ml-auto shrink-0 rounded-sm text-hint text-muted-foreground underline-offset-2 focus-ring hover:text-foreground hover:underline"
      >
        Change
      </a>
    </li>
  )
}
