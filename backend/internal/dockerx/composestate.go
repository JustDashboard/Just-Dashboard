package dockerx

import (
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// What "3 stacks" meant in two places at once.
//
// A compose stack is two different objects wearing one name. There is the
// *configuration* — a compose file on disk, which may never have been
// deployed — and there is the *deployed project*, a set of containers Docker
// labelled with a project name, which may have no file this dashboard can
// find. The stack list mixed them: it grouped labelled containers, then folded
// in every compose file it discovered, and reported `Running/Total` where
// Total counted containers. So a stack that existed only as a file read as
//
//	0/0 up
//
// and the overview, which counted only labelled containers, said "3 stacks"
// while the stacks page said 7.
//
// The vocabulary below keeps the two apart. Total counts *services declared by
// the file*; Running counts containers; Deployed says whether Docker holds
// anything for this project at all; and the state is a word rather than a
// fraction, because "0/0" is not a state.

// StackState is what a compose project is doing, in one word.
type StackState string

const (
	// StackRunning — every declared service has a running container.
	StackRunning StackState = "running"
	// StackPartial — some services are up and some are not. This is the state
	// that quietly breaks an application while its front page still answers.
	StackPartial StackState = "partial"
	// StackDegraded — everything is up, but at least one service is failing
	// its health check. Running and not working.
	StackDegraded StackState = "degraded"
	// StackStopped — the project has containers and none of them is running.
	// Somebody took it down; the containers and their configuration remain.
	StackStopped StackState = "stopped"
	// StackNotDeployed — a compose file exists and Docker holds nothing for
	// it. Never brought up, or brought down with `down`.
	StackNotDeployed StackState = "not-deployed"
	// StackUnknown — containers carry the project label but no compose file is
	// reachable, so what the project *should* consist of cannot be known.
	StackUnknown StackState = "unknown"
)

// describeStack works out the state and the sentence that goes with it.
//
// Declared is the service list from the compose file, and may be empty when no
// file is reachable — which is itself the answer, not a failure.
func describeStack(st *ComposeStack) (StackState, string) {
	declared := len(st.Declared)
	withContainers := 0
	running, unhealthy := 0, 0
	for _, svc := range st.Services {
		if svc.Missing {
			continue
		}
		withContainers++
		if svc.State == "running" {
			running++
			if svc.Health == "unhealthy" {
				unhealthy++
			}
		}
	}

	switch {
	case withContainers == 0 && declared == 0:
		// No containers and no file we can read. Nothing to say, and saying
		// "0/0 up" would be saying it anyway.
		return StackUnknown, "No containers, and no compose file this dashboard can read."
	case withContainers == 0:
		return StackNotDeployed, "Not deployed · " + plural(declared, "service defined", "services defined")
	case declared == 0:
		// Containers exist and carry the label, but the file is somewhere this
		// dashboard cannot see. Report what is true and do not invent a total.
		if running == 0 {
			return StackStopped, "Stopped · " + plural(withContainers, "container", "containers") + " · compose file not found"
		}
		return StackUnknown, itoa(running) + " of " + plural(withContainers, "container running", "containers running") + " · compose file not found"
	case running == 0:
		return StackStopped, "Stopped · " + itoa(declared) + " services defined"
	case running < declared:
		return StackPartial, "Partially running · " + itoa(running) + "/" + itoa(declared) + " services"
	case unhealthy > 0:
		return StackDegraded, "Degraded · " + itoa(running) + "/" + itoa(declared) +
			" services running, " + itoa(unhealthy) + " failing a health check"
	default:
		return StackRunning, "Running · " + itoa(running) + "/" + itoa(declared) + " services"
	}
}

// RestateStack recomputes a stack's orphans, state and summary after its
// declared service list has been replaced by a better one.
//
// The list view reads the YAML directly because it polls; the detail view asks
// compose, which resolves includes and profiles and can legitimately produce a
// different set. Everything derived from that set has to be recomputed, or the
// panel shows the good service list beside the sentence built from the weaker
// one.
func RestateStack(st ComposeStack) ComposeStack {
	st.Orphans = orphanServices(&st)
	st.Deployed = st.Containers > 0
	if st.Total == 0 {
		st.Total = st.Containers
	}
	st.State, st.Summary = describeStack(&st)
	return st
}

// Active reports whether a stack is holding running containers, which is the
// number the overview means by "active stacks" and the one that used to
// disagree with the stacks page.
func (s StackState) Active() bool {
	return s == StackRunning || s == StackPartial || s == StackDegraded || s == StackUnknown
}

// declaredServicesFromFile reads the top-level service names out of a compose
// file without running compose.
//
// The list is used for the *count* on a list that polls, where a subprocess
// per stack is not affordable. It is a weaker answer than `docker compose
// config --services`: it does not resolve `include`, profiles, `extends` or
// variable substitution, and the stack detail uses the real thing for exactly
// that reason. Where the two can disagree the model says which one it has —
// see ComposeStack.DeclaredSource — rather than presenting a guess as a fact.
func declaredServicesFromFile(paths []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc struct {
			Services map[string]yaml.Node `yaml:"services"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			continue
		}
		for name := range doc.Services {
			if name = strings.TrimSpace(name); name != "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}
