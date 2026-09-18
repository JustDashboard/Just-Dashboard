import { defaultConfiguration } from "../../../../frontend/src/components/deploy/deployment-defaults"
import type { DeploymentDetectionCandidate } from "../../../../frontend/src/lib/types"

const common = {
  id: "audit",
  name: "Audit fixture",
  root: ".",
  confidence: "high" as const,
  evidence: [],
  needsDecision: [],
}

const fixtures: Array<{ name: string; candidate: DeploymentDetectionCandidate }> = [
  {
    name: "static",
    candidate: { ...common, profile: "static", buildMethod: "static" },
  },
  {
    name: "vite",
    candidate: {
      ...common,
      profile: "static",
      buildMethod: "recipe",
      recipe: "node",
      framework: "vite",
      outputDirectory: "dist",
    },
  },
  {
    name: "vite-start",
    candidate: {
      ...common,
      profile: "static",
      buildMethod: "recipe",
      recipe: "node",
      framework: "vite",
      outputDirectory: "dist",
      startCommand: "npm run start",
      port: 3000,
    },
  },
  {
    name: "containerfile",
    candidate: {
      ...common,
      profile: "web",
      buildMethod: "dockerfile",
      evidence: [{ path: "Containerfile", reason: "Detected container build file" }],
    },
  },
]

for (const fixture of fixtures) {
  const result = defaultConfiguration(fixture.candidate.profile, fixture.candidate)
  console.log(
    JSON.stringify({
      fixture: fixture.name,
      internalPort: result.runtime.internalPort,
      checkCount: result.checks.length,
      dockerfile: result.build.dockerfile,
      buildSecrets: result.build.secrets,
    }),
  )
}
