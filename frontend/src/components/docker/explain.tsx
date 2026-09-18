"use client"

import { Question } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Label } from "@/components/ui/label"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"

/**
 * The teaching layer.
 *
 * Docker's vocabulary is the actual barrier to using it, not its commands. An
 * operator who has never met it does not need a tooltip saying "Bind mount" —
 * they need to be told that a volume is storage Docker manages for you, a bind
 * mount is a folder on this server handed to the container, and choosing wrong
 * is how people lose data. Every panel in this feature therefore says what
 * things are in the place the choice is made, not in documentation somewhere
 * else.
 *
 * The rule these components enforce is that the explanation is *quiet*: a
 * short line under the field for the thing you need to know to answer, and a
 * hover card for the paragraph behind it. A form that shouts every caveat at
 * once is as unusable as one that explains nothing, and this is the shape that
 * lets an expert skim past what a newcomer stops to read.
 */

/** One short line under a control: what to type, or what happens if you do not. */
export function Hint({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p className={cn("text-hint leading-relaxed text-muted-foreground", className)} {...props} />
  )
}

/**
 * A term with its definition one hover away.
 *
 * Rendered as a dotted underline rather than a question-mark icon so it can sit
 * inside a sentence — "put it on a [network]" reads as prose, where an icon
 * after every noun reads as clutter.
 */
export function Term({ name, children }: { name: string; children?: React.ReactNode }) {
  const entry = GLOSSARY[name]
  return (
    <HoverCard openDelay={150}>
      <HoverCardTrigger asChild>
        <button
          type="button"
          className="cursor-help underline decoration-dotted underline-offset-4 hover:text-foreground"
        >
          {children ?? name}
        </button>
      </HoverCardTrigger>
      <HoverCardContent className="w-80 text-xs leading-relaxed">
        <p className="mb-1 text-body font-medium">{entry?.title ?? name}</p>
        <p className="text-muted-foreground">{entry?.body ?? "No description available."}</p>
      </HoverCardContent>
    </HoverCard>
  )
}

/** The same definition behind an icon, for a panel header where there is no sentence to put it in. */
export function ExplainIcon({ name, className }: { name: string; className?: string }) {
  const entry = GLOSSARY[name]
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

/**
 * A labelled control with its explanation attached.
 *
 * `hint` is the line that answers "what do I put here"; `term` adds the hover
 * definition to the label for the field whose *name* is the unfamiliar part.
 */
export function Field({
  label,
  hint,
  term,
  htmlFor,
  required,
  className,
  children,
}: {
  label: React.ReactNode
  hint?: React.ReactNode
  term?: string
  htmlFor?: string
  required?: boolean
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className={cn("min-w-0 space-y-1.5", className)}>
      <Label htmlFor={htmlFor} className="flex items-center gap-1.5 text-xs">
        {label}
        {required && <span className="text-destructive">*</span>}
        {term && <ExplainIcon name={term} />}
      </Label>
      {children}
      {hint && <Hint>{hint}</Hint>}
    </div>
  )
}

/**
 * The definitions, in one place.
 *
 * Written for somebody who has never run a container and phrased around the
 * decision rather than the mechanism — "a volume is where data goes if you
 * want to keep it" is more useful at the moment of choosing than an accurate
 * description of the storage driver. Each one is two or three sentences: long
 * enough to be an answer, short enough to be read in a hover card.
 */
export const GLOSSARY: Record<string, { title: string; body: string }> = {
  /*
    The first four are the ones a newcomer needs before any of the others make
    sense, and they were the four this file did not have. Somebody arriving at
    /docker for the first time met "Runtime health", "Attention" and "Compose
    stacks" as three headings with no way to find out what any of them meant —
    the exact experience this teaching layer exists to prevent, happening on the
    section's own front page.
  */
  docker: {
    title: "Docker",
    body: "A way of running applications in their own sealed boxes. Each box — a container — carries the application and everything it needs, so it runs the same way here as it did on the machine it was built on, and it cannot disturb anything else on this server. Almost everything self-hosted is published as a Docker image, which is why this page exists.",
  },
  runtimeHealth: {
    title: "Runtime health",
    body: "What Docker itself says about what is running right now: how many containers are up, how many are passing their own health check, how many are failing it. It clears itself — start a stopped container and this goes green again with nothing else to do.",
  },
  attention: {
    title: "Attention",
    body: "Everything that is not a running-or-not fact: a container with no memory limit, a database published to the whole internet, an image pinned to a tag that moves under you. None of it stops a service today and none of it goes away on its own — this is the list that needs a decision rather than a restart.",
  },
  containerState: {
    title: "Running, stopped, paused",
    body: '"Running" means the program inside is alive — not necessarily that it works, which is what a health check is for. "Exited" means it finished or crashed, and the exit code says which. "Paused" means it is frozen in place, still holding its memory. "Restarting" means it keeps stopping and Docker keeps starting it again, which is usually a crash loop rather than a recovery.',
  },
  cpuShare: {
    title: "How CPU is counted",
    body: "Docker counts one core as 100%, so a container using two cores fully reads as 200% and that is normal rather than an emergency. What matters is the figure against this server's total — eight cores is 800% — and against the container's own limit, if it was given one.",
  },
  memoryUse: {
    title: "How memory is counted",
    body: 'The first figure is what the container is using now. A second figure after a slash is the ceiling somebody set for it; without one there is no ceiling, and Docker unhelpfully reports the whole machine\'s RAM in its place — which is why this dashboard says "no limit" instead of dividing by it.',
  },
  exitCode: {
    title: "Exit code",
    body: "The number the program returned when it stopped. 0 means it finished on purpose. 137 almost always means it was killed for using too much memory; 139 is a crash; 1 and 2 are the program's own way of saying it refused to start, and its logs will say why.",
  },
  healthCheck: {
    title: "Health check",
    body: 'A command Docker runs inside the container every so often to ask whether it is actually working. Without one, "running" only means the process has not exited — a wedged application answering nothing still counts as up. Most good images ship one; the rest need one adding to the compose file.',
  },
  image: {
    title: "Image",
    body: "A packaged, read-only copy of an application and everything it needs to run. Containers are made from images. `nginx:alpine` means the image called nginx, the version tagged alpine — the part after the colon is the tag, and it decides which version you get.",
  },
  container: {
    title: "Container",
    body: "One running copy of an image. It has its own filesystem, network address and processes, but shares the server's kernel. Anything it writes inside itself is lost when it is replaced, which is what volumes are for.",
  },
  tag: {
    title: "Tag",
    body: "The version part of an image name, after the colon. `latest` is not a version — it means whatever the publisher last pushed, so two servers running `latest` can be running different software. Pinning a real version is what makes an update something you choose.",
  },
  volume: {
    title: "Volume",
    body: "Storage Docker manages for you, kept outside the container so it survives being recreated. This is where a database's data belongs. Give it a name — an unnamed one gets a random hash you will not recognise in six months.",
  },
  bind: {
    title: "Folder on this server",
    body: "A directory on the server handed straight to the container, so both see the same files. Right for configuration you want to edit yourself, and for anything already on disk. Riskier than a volume: the container writes to your filesystem with whatever permissions it has.",
  },
  tmpfs: {
    title: "Temporary memory",
    body: "A filesystem that lives in RAM and vanishes when the container stops. Useful for scratch files and caches, and for anything you specifically do not want written to disk.",
  },
  port: {
    title: "Published port",
    body: "Makes a port inside the container reachable from outside it. Written host:container — `8080:80` means the server's port 8080 reaches the container's port 80. Leave the host side empty to let containers on the same network reach it while nothing outside can. Keep the address on 127.0.0.1 unless it genuinely has to be reachable from other machines: a published port is wired with NAT rules the firewall never sees.",
  },
  hostIp: {
    title: "Which addresses can reach it",
    body: "127.0.0.1 means only this server — the right answer for a database, and for anything you will put behind the reverse proxy. Left empty, Docker publishes on every interface, and it does so with NAT rules that are consulted before the firewall's, so it is reachable even when the firewall looks like it says no.",
  },
  network: {
    title: "Network",
    body: "A private network containers can be put on so they can reach each other by name. Two containers on the same network can talk; two on different networks cannot, which is the cause of most \"it can't connect to the database\" problems. On a network, a container's name is its hostname.",
  },
  restart: {
    title: "Restart policy",
    body: 'What Docker does when the container stops. "Unless stopped" is what a service wants: it comes back after a crash and after a reboot, but stays down if you deliberately stopped it. "No" means it will not come back when this server restarts.',
  },
  env: {
    title: "Environment variables",
    body: "Settings passed to the program inside the container, and the usual way to configure one. Most images document the ones they expect — a database image will refuse to start without a password variable, for instance.",
  },
  memoryLimit: {
    title: "Memory limit",
    body: "The most memory this container may use. Without one, a container with a leak takes the whole server down and the kernel picks something at random to kill. With one, the kernel kills this container instead, which is usually the outcome you wanted.",
  },
  cpuLimit: {
    title: "CPU limit",
    body: "How many cores this container may use, as a number — 1.5 means one and a half. Without a limit it can use all of them, which is fine for the only thing on a server and not for one of ten.",
  },
  health: {
    title: "Health check",
    body: 'A command Docker runs inside the container to ask whether it is actually working. Without one, "running" only means the process has not exited — a wedged application that answers nothing still counts as up.',
  },
  privileged: {
    title: "Privileged",
    body: "Removes almost every restriction separating the container from the server. Anything that gets into a privileged container has the machine. A handful of tools genuinely need it; most images asking for it need one or two specific capabilities instead.",
  },
  compose: {
    title: "Compose",
    body: "A file describing several containers that belong together, plus the networks and volumes they share. Running it brings the whole set up in the right order. It is a plain file on disk, so it can be committed to git, backed up, and used from a terminal exactly as it is here.",
  },
  stack: {
    title: "Stack",
    body: "One compose file and the containers it created — an application rather than a process. Acting on a stack acts on all of its services at once.",
  },
  image_layer: {
    title: "Layers",
    body: "An image is built as a stack of layers, one per instruction in its Dockerfile. Layers are shared between images, which is why ten images can total less than the sum of their sizes, and why deleting one often reclaims less than it claims to be.",
  },
  dangling: {
    title: "Dangling image",
    body: "A layer left behind when an image was rebuilt or re-pulled and the tag moved to the new copy. Nothing references it and it is always safe to remove.",
  },
  buildCache: {
    title: "Build cache",
    body: 'What BuildKit keeps from every `docker build` so the next one can skip the steps that have not changed. It lives outside the image store, which is why deleting images never shrinks it and why it is the usual answer to "where did my disk go" on a server that builds. Emptying it costs nothing but a slower next build.',
  },
  writableLayer: {
    title: "The container's own filesystem",
    body: "Anything a container writes that is not in a volume goes here. It is not backed up, it is invisible to the file manager, and it is destroyed the moment the container is recreated — which includes every image update.",
  },
  networkMode: {
    title: "Network mode",
    body: '"bridge" is the normal one: the container gets its own address and reaches the outside through the server. A named network means it was put on one deliberately, so it can reach other containers there by name. "host" means it has no network of its own at all — it uses the server\'s, so every port it opens is open on the server directly, with no publishing step and nothing for the firewall\'s Docker rules to filter.',
  },
  hostNetwork: {
    title: "The host network",
    body: "Gives the container the server's own network instead of one of its own. Every port it listens on is immediately open on the server, published or not, and it can reach anything bound to 127.0.0.1 — including databases that are on loopback precisely so nothing else can reach them. Occasionally necessary, for discovery protocols and VPN software; almost never what you want otherwise.",
  },
  movingTag: {
    title: "Moving tag",
    body: 'A tag whose meaning changes — `latest`, `stable`, `3`, or no tag at all. It means whatever the publisher last pushed under that name, so two servers running "the same" tag can be running different software, and there is no earlier version to roll back to. Pinning a real version, or a digest, makes an update something you choose.',
  },
  containerUser: {
    title: "User",
    body: "Which account the program inside the container runs as. Leave it empty to use whatever the image says — `1000:1000` is the common override. `root` is the default for most images and means the process has full rights inside the container, which matters most where the container can reach the server's files through a folder mount or the host network.",
  },

  /*
    The entries below replaced prose that used to be printed in the create
    form itself — a section heading with three lines of explanation under it,
    six times over, plus a hint under most fields. An expert scrolled past all
    of it on every visit and a newcomer read it once. Behind a `?` it costs
    nothing at rest and says more than the line it replaced had room for.
  */
  containerName: {
    title: "Name",
    body: "How you will refer to it here, and the hostname other containers on the same network use to reach it — `postgres:5432` works between containers precisely because one of them is named postgres. Docker generates a random one if you leave it empty, which is fine for something disposable and a nuisance for anything else.",
  },
  command: {
    title: "Command",
    body: "Overrides what the image runs when it starts. Every image already has one, so leave this empty unless the documentation told you otherwise — an image given the wrong command usually exits immediately with a message you will only find in its logs.",
  },
  workingDir: {
    title: "Working directory",
    body: "The directory the program starts in inside the container. Images set their own, and it is almost always the right one. Changing it matters only for an image that expects to be run from somewhere specific and was not told so.",
  },
  initProcess: {
    title: "Init process",
    body: "Puts a tiny supervisor at PID 1 inside the container to clean up after processes that exit. Most programs were never written to be PID 1, and without this their abandoned child processes accumulate as zombies until the container is restarted. It costs nothing and is almost always right.",
  },
  readOnlyRootfs: {
    title: "Read-only filesystem",
    body: "The container cannot write anywhere except the storage you attached to it. A good default for anything that does not need to write — it means a compromised process cannot modify the application it is running, and it makes the container's own filesystem stop being somewhere data can be accidentally left.",
  },
  pullPolicy: {
    title: "Pull a fresh image first",
    body: "Checks the registry for a newer copy of this tag before creating the container. Off, Docker uses whatever copy is already on this server — which for a moving tag like `latest` may be months old. On, creating takes as long as the download.",
  },
  templateBinding: {
    title: "Why these bind to this server only",
    body: "Every starting point here publishes its ports on 127.0.0.1, so only this server can reach them. That is the right default even for something meant to be public: put it behind the reverse proxy, which reaches it on loopback like anything else here and brings TLS, logging and access rules with it. A port published on every interface bypasses the firewall, which is not a thing to opt into by accident.",
  },
  pastedCommand: {
    title: "Nothing runs yet",
    body: "The command is read, not executed. It becomes a form you can check and change, and only the Create button at the bottom actually does anything. Flags the visual editor does not understand are listed for you rather than dropped silently — a `--gpus all` quietly ignored produces a container that starts and then has no GPU.",
  },
  equivalentCommand: {
    title: "The equivalent command",
    body: "Exactly what creating this container will do, rendered by the server from the same spec the Create button sends — so it cannot drift from what actually happens. Copy it if you would rather run it in a shell yourself, or keep it for a ticket.",
  },
  composeExport: {
    title: "The same container, as a file",
    body: "A container created here exists only in Docker's own memory: there is no file anywhere describing it, so it cannot be committed to git, backed up, or recreated on another machine. The compose version can. Paste it into a new stack to keep it.",
  },
  containerStorage: {
    title: "Storage",
    body: "Anything a container writes outside the paths you attach storage to lives in the container's own filesystem, and that is destroyed every time the container is replaced — which includes every image update. A volume is the fix: Docker keeps it outside the container, so the data survives.",
  },
}
