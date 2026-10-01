"use client"

import { AreaStub } from "@/components/database/shell/area-stub"

/** ORM and code generation from the live schema. */
export function Generate() {
  return (
    <AreaStub section="generate" area="generate">
      Code from the live schema: Prisma, Drizzle, TypeScript, Zod and the other generators.
    </AreaStub>
  )
}
