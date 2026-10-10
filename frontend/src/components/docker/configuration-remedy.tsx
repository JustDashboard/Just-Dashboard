"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type {
  ContainerDetail,
  ContainerSpec,
  CreateResult,
  DockerFinding,
  SpecPreview,
} from "@/lib/types"
import {
  changedSpecFields,
  composeRemedy,
  DOCKER_REMEDIES,
  dockerRemedyKind,
  prepareDockerRemedy,
  type DockerRemedyKind,
} from "@/lib/docker-remedies"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Field } from "@/components/form"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Button } from "@/components/ui/button"
import type { ConfirmFn } from "./shared"

export function ConfigurationRemedy({
  detail,
  findings,
  confirm,
  onChanged,
}: {
  detail: ContainerDetail
  findings: DockerFinding[]
  confirm: ConfirmFn
  onChanged: () => void
}) {
  const { can } = useAuth()
  if (detail.composeStack)
    return (
      <Notice title="Compose owns this configuration">
        Edit the owning service, validate and save the file, then bring the stack up. This keeps the
        remedy through future deployments.{" "}
        {findings
          .filter(
            (finding) =>
              dockerRemedyKind(finding) || finding.id.startsWith("container.nomemorylimit."),
          )
          .map((finding) => (
            <div key={finding.id} className="mt-3 space-y-2">
              <p className="font-medium">{finding.title}</p>
              <pre className="font-mono text-hint whitespace-pre-wrap">
                {composeRemedy(finding.id.split(".")[1])}
              </pre>
              <Link
                className="underline"
                href={`/docker/stacks/${encodeURIComponent(detail.composeStack!)}?tab=compose&remedy=${encodeURIComponent(finding.id.split(".")[1])}`}
              >
                Edit the owning Compose service
              </Link>
            </div>
          ))}
        <Link
          className="underline"
          href={`/docker/stacks/${encodeURIComponent(detail.composeStack)}?tab=compose`}
        >
          Open Compose file
        </Link>
      </Notice>
    )
  if (!can("system.admin"))
    return (
      <Notice title="Administrator required">
        The replacement specification includes environment credentials and host mounts. You can
        review the evidence under Inspect; an administrator can edit and apply this configuration.
      </Notice>
    )
  return (
    <StandaloneRemedy
      key={detail.id}
      detail={detail}
      findings={findings}
      confirm={confirm}
      onChanged={onChanged}
    />
  )
}

function StandaloneRemedy({
  detail,
  findings,
  confirm,
  onChanged,
}: {
  detail: ContainerDetail
  findings: DockerFinding[]
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
  const [values, setValues] = useState<Partial<Record<DockerRemedyKind, string>>>({})
  const [error, setError] = useState("")
  const [preview, setPreview] = useState<{
    spec: ContainerSpec
    content: string
    rendered: SpecPreview
  }>()
  const [busy, setBusy] = useState(false)
  const content = draft ?? (source.data ? JSON.stringify(source.data, null, 2) : "")
  const kinds = [
    ...new Set(findings.map(dockerRemedyKind).filter((kind): kind is DockerRemedyKind => !!kind)),
  ]
  const edit = (next: string) => {
    setDraft(next)
    setPreview(undefined)
    setError("")
  }
  const prepare = (kind: DockerRemedyKind) => {
    try {
      edit(JSON.stringify(prepareDockerRemedy(JSON.parse(content), kind, values[kind]), null, 2))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
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
    /* The preview reports invalid JSON next to the editor. */
  }
  return (
    <div className="space-y-6">
      <Panel plain>
        <PanelHeader title="Review configuration remedies" />
        <PanelBody className="space-y-4">
          <p className="text-body text-muted-foreground">
            Prepare a local change, review the full replacement specification and confirm before it
            runs. The specification contains credentials; keep it private. It represents the
            dashboard&apos;s supported container settings; compare Inspect for additional Engine
            options before replacing.
          </p>
          {kinds.map((kind) => (
            <div key={kind} className="space-y-2 border-t border-hairline pt-3">
              <p className="text-body font-medium">{DOCKER_REMEDIES[kind].label}</p>
              <p className="text-body text-muted-foreground">{DOCKER_REMEDIES[kind].advice}</p>
              {(kind === "latest" || kind === "nohealthcheck") && (
                <Field
                  htmlFor={`remedy-${kind}`}
                  label={kind === "latest" ? "Version or digest" : "Readiness command"}
                >
                  <Input
                    id={`remedy-${kind}`}
                    value={values[kind] ?? ""}
                    onChange={(e) =>
                      setValues((current) => ({ ...current, [kind]: e.target.value }))
                    }
                  />
                </Field>
              )}
              <Button size="xs" variant="outline" onClick={() => prepare(kind)}>
                Prepare change
              </Button>
            </div>
          ))}
        </PanelBody>
      </Panel>
      <Field
        label="Replacement specification"
        htmlFor="remedy-spec"
        error={error}
        hint="Nothing runs until you review and confirm. Name is retained; replacement starts the service."
      >
        <Textarea
          id="remedy-spec"
          className="min-h-80 font-mono text-hint"
          value={content}
          onChange={(e) => edit(e.target.value)}
          spellCheck={false}
        />
      </Field>
      <p className="text-body text-muted-foreground">
        Changed fields: {changed.join(", ") || "none"}
      </p>
      <Button variant="outline" pending={busy} disabled={!changed.length || busy} onClick={review}>
        Preview replacement
      </Button>
      {preview && (
        <div className="space-y-3">
          <p className="text-body font-medium">Reviewed Compose equivalent</p>
          <Well className="max-h-80 overflow-auto font-mono text-hint break-all whitespace-pre-wrap">
            {preview.rendered.compose}
          </Well>
          <p className="text-body text-muted-foreground">
            Docker validates the submitted settings when creating the replacement. Preview shows the
            settings; it cannot prove the application will work.
          </p>
          <Button
            variant="outline"
            disabled={!can("destructive") || !!source.data.autoRemove}
            onClick={apply}
          >
            Replace with reviewed configuration
          </Button>
          {source.data.autoRemove && (
            <p className="text-hint text-muted-foreground">
              This auto-remove container cannot be replaced in place. Create a replacement under a
              new name so the original remains recoverable.
            </p>
          )}
          {!can("destructive") && (
            <p className="text-hint text-muted-foreground">
              Destructive capability is required to apply.
            </p>
          )}
        </div>
      )}
    </div>
  )
}
