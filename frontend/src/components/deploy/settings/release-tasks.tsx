"use client"

import { Plus, Trash } from "@/components/icons"
import type { DeploymentConfiguration, DeploymentVariable } from "@/lib/types"
import { Group } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"

export type ReleaseTask = NonNullable<DeploymentConfiguration["build"]["releaseTasks"]>[number]

/**
 * Named, timed gates that run after the artifact is recorded and before
 * activation — a migration, a cache warm, a smoke script. Each task's `env`
 * is drawn only from variables already scoped to Release task, so a task can
 * never reach a value nobody granted it.
 */
export function ReleaseTasks({
  tasks,
  variables,
  disabled,
  onChange,
  rowError,
}: {
  tasks: ReleaseTask[]
  variables: DeploymentVariable[]
  disabled?: boolean
  onChange: (tasks: ReleaseTask[]) => void
  /** The message a save refused for this task, when it named the task but no sub-field. */
  rowError?: (index: number) => string | undefined
}) {
  const releaseVariables = variables.filter((variable) => variable.scopes.includes("release_task"))
  const update = (index: number, patch: Partial<ReleaseTask>) =>
    onChange(tasks.map((task, i) => (i === index ? { ...task, ...patch } : task)))

  return (
    <div className="space-y-3">
      {tasks.length === 0 ? (
        <p className="text-body text-muted-foreground">No release tasks configured.</p>
      ) : (
        <div className="space-y-3">
          {tasks.map((task, index) => (
            <Group key={index} className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_9rem_auto]">
                <Input
                  aria-label={`Release task ${index + 1} name`}
                  value={task.name}
                  onChange={(event) => update(index, { name: event.target.value })}
                  placeholder="Database migration"
                  readOnly={disabled}
                />
                <Input
                  aria-label={`Release task ${index + 1} timeout seconds`}
                  type="number"
                  min={1}
                  max={3600}
                  value={task.timeoutSeconds}
                  onChange={(event) =>
                    update(index, { timeoutSeconds: Number(event.target.value) })
                  }
                  readOnly={disabled}
                  className="font-mono"
                />
                {!disabled && (
                  <Button
                    type="button"
                    size="icon-sm"
                    variant="ghost"
                    aria-label={`Remove release task ${task.name || index + 1}`}
                    onClick={() => onChange(tasks.filter((_, i) => i !== index))}
                  >
                    <Trash />
                  </Button>
                )}
              </div>
              <Input
                aria-label={`Release task ${index + 1} working directory`}
                value={task.workingDirectory ?? ""}
                onChange={(event) => update(index, { workingDirectory: event.target.value })}
                placeholder="Working directory (source root by default)"
                readOnly={disabled}
                className="font-mono"
              />
              <Textarea
                aria-label={`Release task ${index + 1} command`}
                value={task.command}
                onChange={(event) => update(index, { command: event.target.value })}
                placeholder="./bin/migrate"
                readOnly={disabled}
                rows={3}
                className="font-mono text-xs"
              />
              <div>
                <p className="text-hint text-muted-foreground">Release task environment</p>
                {releaseVariables.length === 0 ? (
                  <p className="mt-1 text-hint text-muted-foreground">
                    Add Release task scope to a variable to make it selectable here.
                  </p>
                ) : (
                  <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1.5">
                    {releaseVariables.map((variable) => (
                      <Label key={variable.name} className="flex items-center gap-2 text-xs">
                        <Checkbox
                          checked={task.env.includes(variable.name)}
                          disabled={disabled}
                          onCheckedChange={(checked) =>
                            update(index, {
                              env:
                                checked === true
                                  ? [...new Set([...task.env, variable.name])]
                                  : task.env.filter((name) => name !== variable.name),
                            })
                          }
                        />
                        <span className="font-mono">{variable.name}</span>
                      </Label>
                    ))}
                  </div>
                )}
              </div>
              {rowError?.(index) && (
                <p role="alert" className="text-hint text-destructive">
                  {rowError(index)}
                </p>
              )}
            </Group>
          ))}
        </div>
      )}
      {!disabled && (
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() =>
            onChange([
              ...tasks,
              { name: "", command: "", workingDirectory: "", timeoutSeconds: 300, env: [] },
            ])
          }
        >
          <Plus className="size-3.5" />
          Add release task
        </Button>
      )}
    </div>
  )
}
