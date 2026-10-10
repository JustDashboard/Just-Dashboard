"use client"

import { useCallback, useMemo, useRef, useState } from "react"
import { CheckCircle, Download, GitTag } from "@/components/icons"
import { post } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { Hint, Term } from "@/components/docker/explain"
import { foldPull, pullShare, type PullFrame, type PullLayer } from "@/components/docker/images"
import { Disclosure } from "@/components/form"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { cn } from "@/lib/utils"

/**
 * A pull over the progress socket, held by the page so the image's row can
 * say it is being pulled while the dialog is open.
 *
 * The socket is enabled only while the pull is in flight. The server closes
 * it once the pull ends, and `useSocket` reconnects whatever is enabled — so
 * a socket left enabled after "done" opened a fresh pull a second later, and
 * another after that, for as long as the dialog stayed mounted, closed or not.
 */
export function usePull() {
  const [ref, setRef] = useState<string>()
  const [frames, setFrames] = useState<PullFrame[]>([])
  const [error, setError] = useState<string>()
  const [done, setDone] = useState(false)
  const settle = useRef<(ok: boolean) => void>(undefined)

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "progress") {
      const frame = envelope.data as PullFrame
      setFrames((prev) => [...prev.slice(-400), frame])
    } else if (envelope.type === "done") {
      setDone(true)
      settle.current?.(true)
    } else if (envelope.type === "error") {
      setError(envelope.error ?? "The pull failed")
      setDone(true)
      settle.current?.(false)
    }
  }, [])

  const query = useMemo(() => ({ ref: ref ?? "" }), [ref])
  useSocket("/docker/images/pull", { onMessage, enabled: Boolean(ref) && !done, query })

  const start = (image: string) => {
    setFrames([])
    setError(undefined)
    setDone(false)
    setRef(image)
    return new Promise<boolean>((resolve) => {
      settle.current = resolve
    })
  }
  const reset = () => {
    setRef(undefined)
    setFrames([])
    setError(undefined)
    setDone(false)
  }
  const active = Boolean(ref) && !done
  return { ref: active ? ref : undefined, frames, error, done, active, start, reset }
}

export type Pull = ReturnType<typeof usePull>

/**
 * Pulling an image, drawn as the layers it moves rather than the two hundred
 * lines Docker streams about them: a row per layer with its bytes in the
 * colour of what is happening to them — received in the network's, written
 * to disk in the disk's — and the raw transcript a fold away for when a
 * registry says something worth reading.
 */
export function PullDialog({
  open,
  initial,
  pull,
  onClose,
  onDone,
}: {
  open: boolean
  initial: string
  pull: Pull
  onClose: () => void
  onDone: () => void
}) {
  const [ref, setRef] = useState(initial)
  // Seeded once per opening rather than fighting the input on every render.
  const [seeded, setSeeded] = useState(initial)
  if (seeded !== initial) {
    setSeeded(initial)
    setRef(initial)
  }

  const start = async () => {
    const target = ref.trim()
    const ok = await pull.start(target)
    if (ok) {
      notify.success(`${target} pulled`)
      onDone()
    } else {
      notify.error(`Could not pull ${target}`)
    }
  }

  const close = () => {
    pull.reset()
    onClose()
  }

  const { layers, summary } = foldPull(pull.frames)
  const { moved, total } = pullShare(layers)
  const started = pull.active || pull.frames.length > 0 || pull.error

  return (
    <Modal
      open={open}
      onOpenChange={(next) => {
        // Never close mid-pull: the socket drives it, and closing would
        // abandon a download with no way back to its output.
        if (pull.active || next) return
        close()
      }}
      size="lg"
      title="Pull an image"
      description="Downloads it to this server. Containers already running an older copy keep running it until they are recreated."
      footer={
        <>
          <Button variant="outline" onClick={close} disabled={pull.active}>
            {pull.done ? "Done" : "Close"}
          </Button>
          <Button onClick={start} disabled={!ref.trim() || pull.active} pending={pull.active}>
            <Download className="size-4" />
            Pull
          </Button>
        </>
      }
    >
      <div className="space-y-2">
        <Input
          value={ref}
          spellCheck={false}
          placeholder="nginx:alpine"
          aria-label="Image reference"
          className="font-mono"
          disabled={pull.active}
          onChange={(e) => setRef(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && ref.trim() && !pull.active && start()}
        />
        <Hint>
          Leave the version off and you get <span className="font-mono">latest</span>, which is{" "}
          <Term name="tag">whatever the publisher last pushed</Term>.
        </Hint>
      </div>
      {started && (
        <section aria-label="Pull progress" className="space-y-3">
          <div className="flex min-w-0 items-baseline justify-between gap-3 border-b border-hairline pb-2">
            <p className="min-w-0 truncate text-body">
              {pull.error ? (
                <span className="text-destructive">{pull.error}</span>
              ) : pull.active ? (
                <TextShimmer>{summary ?? "Asking the registry…"}</TextShimmer>
              ) : (
                <span className="inline-flex items-center gap-1.5">
                  <CheckCircle className="size-3.5 text-success" />
                  {summary ?? "Pulled"}
                </span>
              )}
            </p>
            {total > 0 && (
              <span className="numeric shrink-0 text-hint text-muted-foreground">
                <span className="font-medium text-foreground">{bytes(moved)}</span> of{" "}
                {bytes(total)}
              </span>
            )}
          </div>
          {layers.length > 0 && (
            <ul className="max-h-56 space-y-1.5 overflow-y-auto pr-1">
              {layers.map((layer) => (
                <LayerProgress key={layer.id} layer={layer} />
              ))}
            </ul>
          )}
          <Disclosure quiet summary="Docker's output">
            {/* `whitespace-pre-wrap`: the lines are joined with newlines, and
                a plain block collapsed them into one run-on paragraph. */}
            <Well className="max-h-48 whitespace-pre-wrap">
              {pull.frames.length === 0
                ? "Starting…"
                : pull.frames
                    .map((f) => [f.id, f.status, f.progress].filter(Boolean).join(" "))
                    .join("\n")}
            </Well>
          </Disclosure>
        </section>
      )}
    </Modal>
  )
}

const PHASE_WORD: Record<PullLayer["phase"], string> = {
  waiting: "waiting",
  downloading: "downloading",
  extracting: "extracting",
  done: "pulled",
  cached: "already here",
}

/** One layer: its id, a bar of its bytes, and Docker's word for where it is. */
function LayerProgress({ layer }: { layer: PullLayer }) {
  const share =
    layer.phase === "done" || layer.phase === "cached"
      ? 1
      : layer.total && layer.current !== undefined
        ? Math.min(layer.current / layer.total, 1)
        : 0
  // Received bytes in the network's colour, written ones in the disk's (§10).
  const color = layer.phase === "extracting" ? "var(--chart-4)" : "var(--chart-2)"
  return (
    <li className="grid min-w-0 grid-cols-[6.5rem_minmax(0,1fr)_7rem] items-center gap-3 text-hint">
      <span className="truncate font-mono text-muted-foreground">{layer.id}</span>
      <span className="h-1.5 overflow-hidden rounded-full bg-meter-track">
        <span
          className={cn(
            "block h-full transition-[width] duration-500 ease-out motion-reduce:transition-none",
            layer.phase === "done" && "bg-success",
            layer.phase === "cached" && "bg-muted-foreground/40",
          )}
          style={{
            width: `${share * 100}%`,
            background: layer.phase === "done" || layer.phase === "cached" ? undefined : color,
          }}
        />
      </span>
      <span
        className={cn(
          "numeric truncate text-right",
          layer.phase === "done" ? "text-success" : "text-muted-foreground",
        )}
      >
        {(layer.phase === "downloading" || layer.phase === "extracting") && layer.amounts
          ? layer.amounts
          : PHASE_WORD[layer.phase]}
      </span>
    </li>
  )
}

/**
 * A second name for an image. The route was there and nothing called it; the
 * use it has on this page is keeping a copy: tag the image a container runs
 * before pulling its tag again, and the old copy keeps a name instead of
 * turning into an untagged layer the next sweep removes.
 */
export function TagDialog({
  image,
  onClose,
  onTagged,
}: {
  image: { id: string; name: string } | null
  onClose: () => void
  onTagged: () => void
}) {
  const [tag, setTag] = useState("")
  const [busy, setBusy] = useState(false)
  const [seeded, setSeeded] = useState<string | null>(null)
  const suggestion = image ? suggestTag(image.name) : ""
  if (image && seeded !== image.id) {
    setSeeded(image.id)
    setTag(suggestion)
  }

  const submit = async () => {
    if (!image || !tag.trim()) return
    setBusy(true)
    try {
      await post(`/docker/images/${encodeURIComponent(image.id)}/tag`, { tag: tag.trim() })
      notify.success(`Tagged as ${tag.trim()}`)
      onTagged()
      onClose()
    } catch (err) {
      notify.error("Could not tag the image", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={image !== null}
      onOpenChange={(next) => !next && !busy && onClose()}
      title={`Tag ${image?.name ?? "image"}`}
      description="Adds a second name to the same image."
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!tag.trim()} pending={busy}>
            <GitTag className="size-4" />
            Tag
          </Button>
        </>
      }
    >
      <div className="space-y-2">
        <Input
          value={tag}
          spellCheck={false}
          aria-label="New tag"
          className="font-mono"
          onChange={(e) => setTag(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        <Hint>
          The same layers under another name, so nothing is copied. Tag what a container runs before
          pulling its tag again and the old copy keeps a name to go back to.
        </Hint>
      </div>
    </Modal>
  )
}

/** `nginx:1.27-alpine` → `nginx:1.27-alpine-previous`; an untagged layer gets a name to start from. */
function suggestTag(name: string) {
  if (/^[0-9a-f]{12}$/.test(name)) return "kept:" + name
  return name.includes(":") ? `${name}-previous` : `${name}:previous`
}
