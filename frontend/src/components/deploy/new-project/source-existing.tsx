"use client"

import { useState } from "react"
import { Warning } from "@/components/icons"
import { Field } from "@/components/form"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ErrorState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { useSessionState } from "@/lib/view-state"
import type { DeploymentDraftSource } from "@/lib/types"
import { deploymentName } from "@/components/deploy/vocabulary"
import { validateSource, type WizardErrors } from "@/components/deploy/deployment-defaults"
import {
  inspectAndPrepare,
  previewImport,
  repositoryName,
  type ConfigureFlow,
  type ImportPreview,
} from "@/components/deploy/new-project/draft"

type ExistingMode = "existing_container" | "existing_stack" | "existing_checkout"

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/**
 * Bring a workload the dashboard did not create under management, without
 * touching it. Inspection previews what would and would not carry over;
 * anything it cannot adopt automatically has to be acknowledged before the
 * same button proceeds, and adoption ends in a plan, never a run.
 */
export function SourceExisting({ onInspected }: { onInspected: (flow: ConfigureFlow) => void }) {
  const [mode, setMode] = useSessionState<ExistingMode>(
    "deploy.new.existing.mode",
    "existing_container",
  )
  const [resourceId, setResourceId] = useSessionState("deploy.new.existing.resource", "")
  const [localPath, setLocalPath] = useSessionState("deploy.new.existing.path", "")
  const [subdirectory, setSubdirectory] = useSessionState("deploy.new.existing.subdirectory", "")
  const [managedInPlace, setManagedInPlace] = useSessionState("deploy.new.existing.managed", false)
  const [includeSubmodules, setIncludeSubmodules] = useSessionState(
    "deploy.new.existing.submodules",
    false,
  )
  const [includeLfs, setIncludeLfs] = useSessionState("deploy.new.existing.lfs", false)
  const [preview, setPreview] = useSessionState<ImportPreview | undefined>(
    "deploy.new.existing.preview",
    undefined,
  )
  const [acknowledged, setAcknowledged] = useSessionState("deploy.new.existing.acknowledged", false)
  const [errors, setErrors] = useState<WizardErrors>({})
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<Error>()

  const buildSource = (): DeploymentDraftSource =>
    mode === "existing_checkout"
      ? {
          kind: "import",
          mode,
          localPath: localPath.trim(),
          subdirectory: subdirectory.trim() || undefined,
          managedInPlace,
          includeSubmodules,
          includeLfs,
        }
      : { kind: "import", mode, resourceId: resourceId.trim() }

  const inspect = async () => {
    const source = buildSource()
    const validation = validateSource(source)
    if (Object.keys(validation).length) {
      setErrors(validation)
      return
    }
    setErrors({})
    setBusy(true)
    setFailure(undefined)
    try {
      const result = await previewImport(source)
      setPreview(result)
      if ((result.unsupported.length > 0 || result.warnings.length > 0) && !acknowledged) return
      const label =
        result.name || resourceId.trim() || repositoryName(localPath) || "Existing workload"
      onInspected(
        await inspectAndPrepare(deploymentName(label), "imported", source, {
          sourceLabel: label,
          importPreview: result,
        }),
      )
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="w-full max-w-3xl min-w-0 space-y-4">
      {failure && <ErrorState error={failure} />}
      <Panel plain>
        <PanelHeader title="Existing workload" />
        <PanelBody className="space-y-4">
          <Field label="What kind of resource" htmlFor="existing-mode">
            <Select
              value={mode}
              onValueChange={(value) => {
                setMode(value as ExistingMode)
                setPreview(undefined)
                setAcknowledged(false)
              }}
            >
              <SelectTrigger id="existing-mode" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="existing_container">Container</SelectItem>
                <SelectItem value="existing_stack">Compose stack</SelectItem>
                <SelectItem value="existing_checkout">Git checkout</SelectItem>
              </SelectContent>
            </Select>
          </Field>

          {mode !== "existing_checkout" ? (
            <Field
              label={
                mode === "existing_container" ? "Container name or id" : "Compose project name"
              }
              htmlFor="resource-id"
              hint="Inspection is read-only. Nothing is stopped, renamed, or adopted."
              error={errors.resource}
            >
              <Input
                id="resource-id"
                value={resourceId}
                onChange={(event) => setResourceId(event.target.value)}
                className="font-mono"
                aria-invalid={Boolean(errors.resource)}
              />
            </Field>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field
                label="Path on this server"
                htmlFor="existing-local-path"
                className="sm:col-span-2"
                error={errors.localPath}
                hint="Must be inside a configured deployment root."
              >
                <Input
                  id="existing-local-path"
                  value={localPath}
                  onChange={(event) => setLocalPath(event.target.value)}
                  placeholder="/srv/app"
                  className="font-mono"
                />
              </Field>
              <Field
                label="Subdirectory"
                htmlFor="existing-subdirectory"
                hint="Optional relative application root."
              >
                <Input
                  id="existing-subdirectory"
                  value={subdirectory}
                  onChange={(event) => setSubdirectory(event.target.value)}
                  className="font-mono"
                />
              </Field>
              <Label className="flex min-h-9 items-center gap-2 text-xs">
                <Switch checked={managedInPlace} onCheckedChange={setManagedInPlace} />
                Allow managed changes in this checkout
              </Label>
              <div className="flex flex-wrap items-end gap-x-4 sm:col-span-2">
                <Label className="flex min-h-9 items-center gap-2 text-xs">
                  <Checkbox
                    checked={includeSubmodules}
                    onCheckedChange={(checked) => setIncludeSubmodules(checked === true)}
                  />
                  Include Git submodules
                </Label>
                <Label className="flex min-h-9 items-center gap-2 text-xs">
                  <Checkbox
                    checked={includeLfs}
                    onCheckedChange={(checked) => setIncludeLfs(checked === true)}
                  />
                  Include Git LFS objects
                </Label>
              </div>
            </div>
          )}

          {preview && (preview.unsupported.length > 0 || preview.warnings.length > 0) && (
            <Notice tone="warning" icon={Warning} title={`Review ${preview.name}`}>
              <ul className="list-disc space-y-1 pl-4">
                {[...preview.unsupported, ...preview.warnings].map((item) => (
                  <li key={item}>{item}</li>
                ))}
              </ul>
              <Label className="mt-3 flex min-h-11 items-center gap-2 text-xs text-foreground">
                <Checkbox
                  checked={acknowledged}
                  onCheckedChange={(checked) => setAcknowledged(checked === true)}
                />
                I understand which settings cannot be adopted automatically.
              </Label>
            </Notice>
          )}
        </PanelBody>
        <PanelFooter className="justify-end">
          <Button className="h-11 sm:h-9" pending={busy} onClick={() => void inspect()}>
            Inspect
          </Button>
        </PanelFooter>
      </Panel>
    </div>
  )
}
