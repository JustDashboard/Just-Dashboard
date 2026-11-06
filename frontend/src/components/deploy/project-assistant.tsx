"use client"

import { useCallback, useMemo } from "react"
import { Archive, Gauge, Globe, Key, Logs, Sparkles } from "@/components/icons"
import { Notice } from "@/components/state"
import { useProject } from "@/components/deploy/project-context"
import type { AgentSuggestion } from "@/components/assistant/agent/chat"
import type { AgentChatSummary } from "@/components/assistant/agent/chats"
import { DIAGNOSE_QUESTION } from "@/components/assistant/agent/model"
import type { AgentScope } from "@/components/assistant/agent/sessions"
import { SubjectAssistant } from "@/components/assistant/agent/subject-page"

const SUGGESTIONS: AgentSuggestion[] = [
  { label: "Diagnose it", question: DIAGNOSE_QUESTION, icon: Sparkles },
  {
    label: "Check the logs for errors",
    question: "Read the logs of the live containers and tell me about any errors.",
    product: "docker",
  },
  {
    label: "Which requests are failing?",
    question: "Which requests are failing or slow right now, and why?",
    icon: Globe,
  },
  {
    label: "Is it sized right?",
    question: "How are its CPU and memory against its limits? Are the limits right?",
    icon: Gauge,
  },
  {
    label: "Add a domain",
    question: "I want to add a domain to this deployment. What do you need from me?",
    product: "caddy",
  },
  {
    label: "Set a variable",
    question: "I want to set an environment variable. Which ones does it have now?",
    icon: Key,
  },
  {
    label: "Back up its data",
    question:
      "What backs up this deployment's data, and when did it last run? Run a backup now if one is set up.",
    icon: Archive,
  },
  {
    label: "What happened lately?",
    question: "Summarise its last few runs and container events.",
    icon: Logs,
  },
]

/**
 * A deployment's assistant, as a page of its own (`SubjectAssistant`), about the
 * environment the header has chosen. A chat from the other environment switches the
 * page to it, and `?ask=diagnose` is the overview's Diagnose.
 */
export function ProjectAssistant() {
  const project = useProject()
  const { deployment, project: record } = project.detail
  const scope = useMemo<AgentScope>(
    () => ({
      kind: "deployment",
      projectId: project.projectId,
      environmentId: project.environmentId,
      environment: project.environmentQuery?.environment,
      name: record.name,
      environmentName: deployment.environmentName,
    }),
    [
      project.projectId,
      project.environmentId,
      project.environmentQuery?.environment,
      record.name,
      deployment.environmentName,
    ],
  )
  const { selectEnvironment, staging, production } = project
  const otherEnvironment = useCallback(
    (environment: number) => {
      if (environment === staging?.id) selectEnvironment("staging")
      else if (environment === production?.id) selectEnvironment("production")
    },
    [staging, production, selectEnvironment],
  )
  const environmentName = (id?: number) =>
    id === staging?.id ? "Staging" : id === production?.id ? "Production" : undefined
  return (
    <SubjectAssistant
      slot="project-assistant"
      scope={scope}
      chats={{ deployment: project.projectId }}
      refusal={
        (!project.normalized || project.archived) && (
          <Notice title="The assistant works on a live project" className="mt-6">
            {project.archived
              ? "This project is archived. Restore it and the assistant can read and change it."
              : "This project was made before environments, which are what the assistant acts through, so it cannot work on it."}
          </Notice>
        )
      }
      title={`Ask anything about ${record.name}`}
      description="It can read its logs, requests and metrics, change its settings, domains and variables, take backups and deploy it, as you and under your role."
      placeholder={`Ask anything about ${record.name}…`}
      sends={`what the overview shows about ${deployment.environmentName} and what it reads to answer`}
      what="it can read this deployment, answer anything about it, and change its settings for you."
      suggestions={SUGGESTIONS}
      asks={ASKS}
      onChanged={project.refresh}
      onOtherEnvironment={otherEnvironment}
      environmentOf={(chat: AgentChatSummary) =>
        project.staging && chat.scope.environment !== project.environmentId
          ? environmentName(chat.scope.environment)
          : undefined
      }
    />
  )
}

/** What the overview's Diagnose asks, by its word in the address. */
const ASKS = { diagnose: DIAGNOSE_QUESTION }
