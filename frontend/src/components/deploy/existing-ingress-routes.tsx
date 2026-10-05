import { FormNote, FormSection } from "@/components/form"
import { Status } from "@/components/status-dot"
import type { ExistingIngressBinding } from "@/lib/workload-import"

export function ExistingIngressRoutes({ bindings }: { bindings: ExistingIngressBinding[] }) {
  if (bindings.length === 0) return null
  return (
    <FormSection title="Existing domains · keep current proxy">
      <ul className="space-y-3" aria-label="Existing domain routes">
        {bindings.map((binding) => (
          <li key={binding.id} className="min-w-0 space-y-1">
            <p className="font-mono text-body break-all">
              {binding.hostname}
              {binding.path === "/" ? "" : binding.path}
            </p>
            <Status
              tone={binding.status === "linked" ? "running" : "warning"}
              label={`${binding.proxyKind} · ${binding.service} · ${binding.status === "linked" ? "verified route" : "verification required"}`}
            />
            <FormNote>
              {binding.plannedChange ||
                (binding.continuity === "retarget"
                  ? "Deploy changes prepares a reversible upstream update."
                  : binding.continuity === "unverified"
                    ? "Resolve this route before replacing the application."
                    : "The current upstream remains unchanged.")}
            </FormNote>
          </li>
        ))}
      </ul>
      <FormNote>
        Adoption leaves these routes unchanged. Their existing TLS and proxy settings remain with
        the current proxy.
      </FormNote>
    </FormSection>
  )
}
