import type { ProcessInspect, ProcessRow } from "@/lib/types"

/** A process's own page. A PID alone is reused; the page checks the start time it opened on. */
export function processHref(pid: number, tab?: string) {
  return `/processes/${pid}${tab && tab !== "overview" ? `?tab=${encodeURIComponent(tab)}` : ""}`
}

/** What the kernel does with a signal nobody handles (signal(7)). */
type Disposition = "end" | "core" | "pause" | "resume" | "nothing"

export type SignalSpec = {
  signal: string
  /** The verb's word. */
  label: string
  /** What the page says while it is in flight. */
  progressive: string
  disposition: Disposition
  /** Neither caught nor ignored nor blocked by anything: SIGKILL and SIGSTOP. */
  forced?: boolean
  /** What programs that handle it usually do with it. */
  usual: string
  /** Asked before it is sent: everything here but Resume changes what the process is doing. */
  confirm: boolean
  danger?: boolean
}

/**
 * Every signal the route accepts, in the order they are reached for: ask it
 * to stop, make it stop, freeze and thaw it, then the ones a program defines
 * for itself.
 */
export const SIGNALS: SignalSpec[] = [
  {
    signal: "SIGTERM",
    label: "Terminate",
    progressive: "Terminating",
    disposition: "end",
    usual: "Most programs finish what they are doing and exit.",
    confirm: true,
  },
  {
    signal: "SIGKILL",
    label: "Kill",
    progressive: "Killing",
    disposition: "end",
    forced: true,
    usual: "The kernel ends it at once; nothing it had not written is kept.",
    confirm: true,
    danger: true,
  },
  {
    signal: "SIGSTOP",
    label: "Pause",
    progressive: "Pausing",
    disposition: "pause",
    forced: true,
    usual: "It stops being scheduled until it is resumed; whatever waits on it waits too.",
    confirm: true,
  },
  {
    signal: "SIGCONT",
    label: "Resume",
    progressive: "Resuming",
    disposition: "resume",
    usual: "A paused process carries on where it stopped.",
    confirm: false,
  },
  {
    signal: "SIGHUP",
    label: "Hang up",
    progressive: "Reloading",
    disposition: "end",
    usual: "nginx, sshd and most daemons reload their configuration on it.",
    confirm: true,
  },
  {
    signal: "SIGINT",
    label: "Interrupt",
    progressive: "Signalling",
    disposition: "end",
    usual: "What Ctrl+C sends: a program stops the way it would at a terminal.",
    confirm: true,
  },
  {
    signal: "SIGQUIT",
    label: "Quit",
    progressive: "Signalling",
    disposition: "core",
    usual:
      "A Go or Java program prints every goroutine's or thread's stack; nginx shuts down gracefully.",
    confirm: true,
  },
  {
    signal: "SIGUSR1",
    label: "User signal 1",
    progressive: "Signalling",
    disposition: "end",
    usual: "The program's own: nginx reopens its log files, many workers dump their state.",
    confirm: true,
  },
  {
    signal: "SIGUSR2",
    label: "User signal 2",
    progressive: "Signalling",
    disposition: "end",
    usual: "The program's own: nginx starts a new binary, some servers reload.",
    confirm: true,
  },
  {
    signal: "SIGWINCH",
    label: "Window change",
    progressive: "Signalling",
    disposition: "nothing",
    usual: "nginx and Apache stop their workers gracefully; a terminal program redraws.",
    confirm: true,
  },
  {
    signal: "SIGTTIN",
    label: "Add a worker",
    progressive: "Signalling",
    disposition: "pause",
    usual: "gunicorn starts one more worker.",
    confirm: true,
  },
  {
    signal: "SIGTTOU",
    label: "Remove a worker",
    progressive: "Signalling",
    disposition: "pause",
    usual: "gunicorn stops one of its workers.",
    confirm: true,
  },
]

export function signalSpec(signal: string): SignalSpec | undefined {
  return SIGNALS.find((spec) => spec.signal === signal)
}

export type SignalEffect = {
  /** handled, ignored, blocked, or what the kernel does: ends, pauses… */
  word: string
  tone: "default" | "success" | "warning" | "danger" | "muted"
  /** One sentence for this process, in its own name. */
  sentence: string
}

const DEFAULT_WORDS: Record<Disposition, { word: string; sentence: string }> = {
  end: { word: "ends it", sentence: "does not handle it, so the kernel ends it" },
  core: {
    word: "ends it",
    sentence: "does not handle it, so the kernel ends it and may write a core dump",
  },
  pause: { word: "pauses it", sentence: "does not handle it, so the kernel pauses it" },
  resume: { word: "resumes it", sentence: "carries on if it was paused" },
  nothing: { word: "nothing", sentence: "does not handle it, and by default nothing happens" },
}

/**
 * What a signal will do to *this* process, read from the masks the kernel
 * keeps for it: a program that catches SIGHUP reloads, one that does not is
 * ended by it, and that is the difference between a reload and an outage.
 * Without the masks (another account's process) the kernel's default is all
 * that can be said, and it says so.
 */
export function signalEffect(
  spec: SignalSpec,
  name: string,
  signals: ProcessInspect["signals"] | undefined,
): SignalEffect {
  if (spec.forced) {
    return {
      word: spec.disposition === "pause" ? "always pauses it" : "always ends it",
      tone: spec.disposition === "pause" ? "warning" : "danger",
      sentence: `No program can catch or ignore ${spec.signal}: ${name} ${
        spec.disposition === "pause" ? "is paused" : "is ended"
      } whatever it is doing.`,
    }
  }
  if (spec.disposition === "resume") {
    return {
      word: "resumes it",
      tone: "success",
      sentence: `${name} ${DEFAULT_WORDS.resume.sentence}.`,
    }
  }
  if (!signals?.ready) {
    const fallback = DEFAULT_WORDS[spec.disposition]
    return {
      word: `${fallback.word} unless handled`,
      tone: "muted",
      sentence: `Which signals ${name} handles is not readable as this account. Unless it handles ${spec.signal}, the kernel's default applies: ${fallback.word}.`,
    }
  }
  if (signals.ignored.includes(spec.signal)) {
    return {
      word: "ignored",
      tone: "muted",
      sentence: `${name} ignores ${spec.signal}: sending it changes nothing.`,
    }
  }
  if (signals.caught.includes(spec.signal)) {
    const blocked = signals.blocked.includes(spec.signal)
    return {
      word: blocked ? "handled, held" : "handled",
      tone: "success",
      sentence: `${name} has a handler for ${spec.signal} and does what it was written to. ${spec.usual}${
        blocked ? " It is holding the signal for now, and handles it when it lets it through." : ""
      }`,
    }
  }
  const fallback = DEFAULT_WORDS[spec.disposition]
  return {
    word: fallback.word,
    tone:
      spec.disposition === "nothing"
        ? "muted"
        : spec.disposition === "pause"
          ? "warning"
          : "danger",
    sentence: `${name} ${fallback.sentence}.`,
  }
}

/** The parts of resident memory, for one strip, in the order a reader asks about them. */
export function memoryParts(memory: ProcessInspect["memory"]) {
  return [
    { key: "anonymous", label: "Its own", count: memory.anonymous },
    { key: "file", label: "Files it maps", count: memory.fileBacked },
    { key: "shared", label: "Shared memory", count: memory.sharedMemory },
    { key: "swap", label: "Swapped out", count: memory.swap },
  ]
}

/**
 * The out-of-memory killer's view of the process in words. The score runs
 * from 0 to 1000 (and past it with a bias), and the highest goes first.
 */
export function oomWords(
  score: number,
  adj: number,
): { word: string; tone: "default" | "success" | "warning" } {
  if (adj <= -1000) return { word: "never ended for memory", tone: "success" }
  if (adj < 0 && score === 0) return { word: "protected", tone: "success" }
  if (score >= 500) return { word: "among the first to go", tone: "warning" }
  if (score >= 200) return { word: "a likely choice", tone: "default" }
  return { word: "an unlikely choice", tone: "default" }
}

/** The presets an operator reaches for, as the bias each sets. */
export const OOM_PRESETS = [
  { adj: -1000, label: "Never end it" },
  { adj: -500, label: "Protect it" },
  { adj: 0, label: "Normal" },
  { adj: 500, label: "End it early" },
  { adj: 1000, label: "End it first" },
]

/** A capability in words: `CAP_NET_BIND_SERVICE` → "net bind service". */
export function capabilityWord(cap: string) {
  return cap.replace(/^CAP_/, "").toLowerCase().replaceAll("_", " ")
}

/** The namespaces a process has of its own, in words: "network, mounts". */
const NAMESPACE_WORDS: Record<string, string> = {
  pid: "processes",
  net: "network",
  mnt: "mounts",
  user: "users",
  uts: "hostname",
  ipc: "shared memory",
  cgroup: "control groups",
  time: "clock",
}

export function namespaceWords(namespaces: ProcessInspect["namespaces"]) {
  return namespaces.filter((ns) => ns.own).map((ns) => NAMESPACE_WORDS[ns.kind] ?? ns.kind)
}

/** A user as `www-data (33)`, or the number where the host has no name for it. */
export function idWords(id: { id: number; name?: string }) {
  return id.name ? `${id.name} (${id.id})` : String(id.id)
}

/**
 * Why a process wants a restart that it does not know it wants: its binary
 * or a library it loaded was replaced by an upgrade after it started.
 */
export function staleCode(inspect: ProcessInspect | undefined) {
  if (!inspect) return null
  const libraries = inspect.libraries.filter((library) => library.deleted)
  if (!inspect.exeDeleted && libraries.length === 0) return null
  return { binary: Boolean(inspect.exeDeleted), libraries }
}

/** The pressures on a process worth a notice, worst first. */
export function processWarnings(row: ProcessRow, inspect: ProcessInspect | undefined) {
  const out: { key: string; tone: "warning" | "danger"; title: string; body: string }[] = []
  // A zombie holds nothing and a kernel thread has no descriptors of its own:
  // whatever a read says about either, there is nothing here to act on.
  if (row.state === "zombie" || row.manager === "kernel") return out
  if (row.fdReady !== false && row.openFilesLimit && row.fileDescriptors !== undefined) {
    const share = row.fileDescriptors / row.openFilesLimit
    if (share >= 0.9) {
      out.push({
        key: "fds",
        tone: "danger",
        title: "Almost out of file descriptors",
        body: `${row.fileDescriptors} of ${row.openFilesLimit} are open. At the limit it cannot accept a connection or open a file, and most programs fail with “too many open files”.`,
      })
    }
  }
  if (inspect?.files.deleted) {
    out.push({
      key: "deleted",
      tone: "warning",
      title: `Holding ${inspect.files.deleted === 1 ? "a deleted file" : `${inspect.files.deleted} deleted files`} open`,
      body: "Their space comes back to the disk only when it lets go of them: a log rotated away under a running daemon is the usual one. Reopening its logs or restarting it frees it.",
    })
  }
  if (inspect?.signals.pending.length) {
    out.push({
      key: "pending",
      tone: "warning",
      title: "A signal is waiting",
      body: `${inspect.signals.pending.join(", ")} has been sent and not handled yet${
        inspect.signals.pending.some((s) => inspect.signals.blocked.includes(s))
          ? ": the process is holding it back"
          : ""
      }.`,
    })
  }
  return out
}
