"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { ContainerDetail, ContainerSpec, CreateResult, SpecPreview } from "@/lib/types"
import { changedSpecFields } from "@/lib/container-configuration"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Textarea } from "@/components/ui/textarea"
import { Button } from "@/components/ui/button"
import { BorderBeam } from "@/components/ui/border-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import type { ConfirmFn } from "./shared"

export function ContainerConfiguration(props: {
  detail: ContainerDetail
  confirm: ConfirmFn
  onChanged: () => void
}) {
  const { can } = useAuth()
  if (props.detail.composeStack)
    return (
      <Button variant="outline" asChild>
        <Link href={`/docker/stacks/${encodeURIComponent(props.detail.composeStack)}?tab=compose`}>
          Open Compose file
        </Link>
      </Button>
    )
  if (!can("system.admin"))
    return (
      <Notice title="Administrator required">
        An administrator can edit the replacement specification. Inspect shows the current
        configuration.
      </Notice>
    )
  return <StandaloneConfiguration {...props} />
}

function StandaloneConfiguration({
  detail,
  confirm,
  onChanged,
}: {
  detail: ContainerDetail
  confirm: ConfirmFn
  onChanged: () => void
}) {
  const { can } = useAuth()
  const router = useRouter()
  const source = usePoll(
    (signal) => get<ContainerSpec>(`/docker/containers/${detail.id}/spec`, undefined, signal),
    0,
    [detail.id],
  )
  const [draft, setDraft] = useState<string>()
  const [error, setError] = useState("")
  const [preview, setPreview] = useState<{
    spec: ContainerSpec
    content: string
    rendered: SpecPreview
  }>()
  const [busy, setBusy] = useState(false)
  const [replacing, setReplacing] = useState(false)
  const content = draft ?? (source.data ? JSON.stringify(source.data, null, 2) : "")
  const edit = (next: string) => {
    setDraft(next)
    setPreview(undefined)
    setError("")
  }
  const review = async () => {
    setBusy(true)
    setError("")
    try {
      const spec = JSON.parse(content) as ContainerSpec
      if (
        !spec ||
        Array.isArray(spec) ||
        typeof spec !== "object" ||
        !spec.image?.trim() ||
        !spec.limits
      )
        throw new Error("A specification with an image and limits is required")
      if (spec.name !== source.data?.name)
        throw new Error("Keep the original container name; use Rename separately")
      const rendered = await post<SpecPreview>("/docker/containers/preview", spec)
      setPreview({ spec, content, rendered })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const apply = () => {
    if (!preview || preview.content !== content || !source.data || source.error) return
    const spec = preview.spec
    confirm({
      title: `Replace ${detail.name}`,
      confirmLabel: "Replace container",
      description: `Changed fields: ${changedSpecFields(source.data, spec).join(", ")}. This starts a replacement and interrupts the service. Mounts and volumes in the reviewed specification remain; the old logs and data stored only in its writable layer are permanently lost. Move required data to a volume first. A running replacement does not prove application readiness.`,
      action: async () => {
        setReplacing(true)
        try {
          const result = await post<CreateResult>(`/docker/containers/${detail.id}/recreate`, {
            spec,
          })
          if (result.warnings?.length)
            notify.warning("Replacement started with caveats", {
              description: result.warnings.join("; "),
            })
          else notify.success("Replacement started; check its health and logs")
          onChanged()
          router.push(`/docker/containers/${encodeURIComponent(result.id)}`)
          return "reported"
        } finally {
          setReplacing(false)
        }
      },
    })
  }
  if (source.loading && !source.data) return <LoadingRows />
  if (source.error) return <ErrorState error={source.error} onRetry={source.refresh} />
  if (!source.data) return null
  let changed: string[] = []
  try {
    changed = changedSpecFields(source.data, JSON.parse(content))
  } catch {
    /* The editor reports invalid JSON when the draft is reviewed. */
  }
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-4">
      {/* One frame around the draft: it is a working region, and its head is
          where the draft says what it changes. */}
      <section className="relative min-w-0">
        {replacing && (
          <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
            <BorderBeam size={120} duration={4} />
          </span>
        )}
        <Panel>
          <PanelHeader
            title={<label htmlFor="remedy-spec">Replacement specification</label>}
            actions={
              replacing ? (
                <TextShimmer className="text-hint font-medium">Replacing…</TextShimmer>
              ) : changed.length > 0 ? (
                <span className="flex min-w-0 flex-wrap items-center justify-end gap-x-2 gap-y-1">
                  <span className="text-hint text-muted-foreground">Edited</span>
                  {changed.map((field) => (
                    <Tag key={field} mono style={{ color: "var(--git-modified)" }}>
                      {field}
                    </Tag>
                  ))}
                </span>
              ) : (
                <span className="text-hint text-muted-foreground">As it runs now</span>
              )
            }
          />
          <PanelBody className="space-y-3">
            <p className="text-hint text-muted-foreground">
              Edit the specification here; nothing runs until you review and confirm. It contains
              credentials, so keep it private. It holds the settings the dashboard can carry across
              a replacement; compare Inspect for anything else the Engine keeps. The name is
              retained, and the replacement starts the service.
            </p>
            <Textarea
              id="remedy-spec"
              className="min-h-80 font-mono text-hint"
              value={content}
              onChange={(event) => edit(event.target.value)}
              spellCheck={false}
              aria-invalid={error ? true : undefined}
            />
            {error && (
              <p role="alert" className="text-hint text-destructive">
                {error}
              </p>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant={preview ? "outline" : "default"}
                pending={busy}
                disabled={!changed.length || busy}
                onClick={review}
              >
                Preview replacement
              </Button>
              {!changed.length && (
                <span className="text-hint text-muted-foreground">
                  Change a field to review a replacement.
                </span>
              )}
            </div>
          </PanelBody>
        </Panel>
      </section>

      {preview && (
        <Panel plain className="animate-rise">
          <PanelHeader title="Reviewed Compose equivalent" />
          <PanelBody className="space-y-3">
            <Well className="max-h-80 overflow-auto text-xs leading-relaxed break-all whitespace-pre-wrap">
              <code>{preview.rendered.compose}</code>
            </Well>
            <p className="text-hint text-muted-foreground">
              Docker validates the submitted settings when creating the replacement. Preview shows
              the settings; it cannot prove the application will work.
            </p>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                disabled={!can("destructive") || !!source.data.autoRemove || replacing}
                onClick={apply}
              >
                Replace with reviewed configuration
              </Button>
              {source.data.autoRemove && (
                <span className="text-hint text-muted-foreground">
                  This auto-remove container cannot be replaced in place. Create a replacement under
                  a new name so the original remains recoverable.
                </span>
              )}
              {!can("destructive") && (
                <span className="text-hint text-muted-foreground">
                  Destructive capability is required to apply.
                </span>
              )}
            </div>
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}
