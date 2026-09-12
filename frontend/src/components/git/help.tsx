"use client"

import { BookOpen, Question } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"

/**
 * The teaching layer for git, in the shape docker/explain.tsx established: the
 * vocabulary is the actual barrier, not the buttons, so the explanation lives
 * where the choice is made. Two forms — a term with its meaning one hover away,
 * and a "how it works" card that lays out the four-step flow — because the
 * person this is for has heard "commit" and "push" and does not know which
 * order they go in or what each one actually does to their files.
 *
 * The rule is the same one that keeps the docker forms usable: the explanation
 * is quiet. An expert skims past it; a newcomer stops to read it.
 */

export const GIT_GLOSSARY: Record<string, { title: string; body: string }> = {
  repository: {
    title: "Repository",
    body: "A folder whose history git tracks. Every version you have ever saved is kept, so you can see what changed, when, and go back to any of them.",
  },
  branch: {
    title: "Branch",
    body: "A separate line of work with its own history. You make one to try something without disturbing the main version, and merge it back — or throw it away — when you are done. The current branch is the one your changes are being saved onto.",
  },
  commit: {
    title: "Commit",
    body: "One saved point in history, with a message describing it. Committing records the changes you have staged; it is local until you push. Think of it as a labelled snapshot you can always come back to.",
  },
  stage: {
    title: "Staging",
    body: "Choosing which changes go into the next commit. You can stage some files and leave others for later, which is how one messy afternoon becomes two tidy commits. Nothing is saved until you commit what you have staged.",
  },
  changes: {
    title: "Changes (the working tree)",
    body: "The edits you have made since the last commit that are not yet saved. Files here are modified, new, or deleted. Stage the ones you want, then commit them.",
  },
  push: {
    title: "Push",
    body: "Send your committed changes to the shared copy on the server (the remote, usually called origin), so others — and your other machines — can see them. Nothing you have only committed locally is safe from a lost laptop until it is pushed.",
  },
  pull: {
    title: "Pull",
    body: "Bring the latest committed changes from the shared copy into yours. This dashboard only pulls when it can do so cleanly, so it never leaves you with a half-finished merge to untangle from a web page.",
  },
  fetch: {
    title: "Fetch",
    body: 'Ask the server what has changed without touching your files. It updates the "you are N behind" count so you know a pull is waiting, and it can never lose anything.',
  },
  stash: {
    title: "Stash",
    body: "Set your uncommitted changes aside so you have a clean slate — to switch branches, say — then bring them back later with Pop. Nothing is lost; it is just parked.",
  },
  remote: {
    title: "Remote (origin)",
    body: 'The shared copy of the repository, usually on a server like GitHub. "origin" is the default name for it. Push sends to it, pull brings from it.',
  },
  aheadBehind: {
    title: "Ahead and behind",
    body: "How your branch compares to the shared copy. Ahead means you have commits it does not — push them. Behind means it has commits you do not — pull them.",
  },
  discard: {
    title: "Discard",
    body: "Throw away the uncommitted edits to a file and put it back the way it was at the last commit. There is no undo — the current contents are gone — which is why it always asks first.",
  },
  head: {
    title: "HEAD",
    body: 'Shorthand for the commit you are currently on — normally the tip of the current branch. "Reset to HEAD" means "put everything back to the last commit".',
  },
}

/** A term with its plain-language meaning one hover away, for use inside a sentence. */
export function GitTerm({ name, children }: { name: string; children?: React.ReactNode }) {
  const entry = GIT_GLOSSARY[name]
  return (
    <HoverCard openDelay={150}>
      <HoverCardTrigger asChild>
        <button
          type="button"
          className="cursor-help underline decoration-dotted underline-offset-4 hover:text-foreground"
        >
          {children ?? entry?.title ?? name}
        </button>
      </HoverCardTrigger>
      <HoverCardContent className="w-80 text-xs leading-relaxed">
        <p className="mb-1 text-body font-medium">{entry?.title ?? name}</p>
        <p className="text-muted-foreground">{entry?.body ?? "No description available."}</p>
      </HoverCardContent>
    </HoverCard>
  )
}

/** The same definition behind a small icon, for a label with no sentence to hold it. */
export function GitExplain({ name, className }: { name: string; className?: string }) {
  const entry = GIT_GLOSSARY[name]
  return (
    <HoverCard openDelay={150}>
      <HoverCardTrigger asChild>
        <button
          type="button"
          aria-label={`What is ${entry?.title ?? name}?`}
          className={cn("text-muted-foreground transition-colors hover:text-foreground", className)}
        >
          <Question className="size-3.5" />
        </button>
      </HoverCardTrigger>
      <HoverCardContent className="w-80 text-xs leading-relaxed">
        <p className="mb-1 text-body font-medium">{entry?.title ?? name}</p>
        <p className="text-muted-foreground">{entry?.body ?? "No description available."}</p>
      </HoverCardContent>
    </HoverCard>
  )
}

const STEPS = [
  { n: 1, title: "Stage", body: "Tick the changed files you want to save together." },
  { n: 2, title: "Describe", body: "Write a short message saying what you changed." },
  { n: 3, title: "Commit", body: "Save them as one point in this repository's history." },
  {
    n: 4,
    title: "Push",
    body: "Send your commits to the shared copy so they are safe and shared.",
  },
]

/** The "new to git?" button: the four-step flow plus the words behind it. */
export function GitHelp() {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm">
          <BookOpen className="size-4" />
          New to git?
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-96 space-y-3">
        <div>
          <p className="text-body font-medium">Saving your work, step by step</p>
          <p className="text-xs text-muted-foreground">
            The same four moves every time. You never have to touch the command line.
          </p>
        </div>
        <ol className="space-y-2">
          {STEPS.map((s) => (
            <li key={s.n} className="flex gap-2.5">
              <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-plot-primary text-hint font-semibold text-primary">
                {s.n}
              </span>
              <div className="min-w-0">
                <p className="text-body leading-tight font-medium">{s.title}</p>
                <p className="text-hint leading-snug text-muted-foreground">{s.body}</p>
              </div>
            </li>
          ))}
        </ol>
        <div className="space-y-1.5 border-t border-hairline pt-2.5 text-xs">
          <p className="text-hint font-medium text-muted-foreground uppercase">
            Words you will see
          </p>
          {(["branch", "stash", "fetch", "pull", "aheadBehind"] as const).map((k) => (
            <p key={k} className="leading-snug">
              <span className="font-medium">{GIT_GLOSSARY[k].title}</span>{" "}
              <span className="text-muted-foreground">— {GIT_GLOSSARY[k].body}</span>
            </p>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}
