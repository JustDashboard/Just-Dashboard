import Link from "next/link"
import { FormNote } from "@/components/form"

export function ComposeSettingsNote({
  projectId,
  sourceSection,
}: {
  projectId: number
  sourceSection: "source" | "compose-source"
}) {
  return (
    <FormNote>
      Per-service settings remain in the{" "}
      <Link
        href={`/deploy/${projectId}/settings/general#${sourceSection}`}
        className="rounded-sm underline underline-offset-2 focus-ring hover:text-foreground"
      >
        Compose source
      </Link>
      . These fields add deployment overrides; empty fields keep the settings in each service’s
      YAML. Inspect the current services on{" "}
      <Link
        href={`/deploy/${projectId}/runtime`}
        className="rounded-sm underline underline-offset-2 focus-ring hover:text-foreground"
      >
        Runtime
      </Link>
      .
    </FormNote>
  )
}
