package assistant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// The deployment operator's reads: what the assistant can read about one
// deployment and the server it runs on, each shaped and bounded for a model. The
// rest of what it can do is in agent.go.

// The operator's read tools.
const (
	ToolRecentRuns      = "recent_runs"
	ToolRunTranscript   = "run_transcript"
	ToolContainerLogs   = "container_logs"
	ToolReleaseMetrics  = "release_metrics"
	ToolLifecycleEvents = "lifecycle_events"
	ToolHostAlerts      = "host_alerts"
	ToolFirewallRules   = "firewall_rules"
	ToolSecurityUpdates = "security_updates"
)

// The bounds an operator read is held to, which the tools' descriptions state.
const (
	OperatorRunsDefault  = 10
	OperatorRunsMax      = 20
	OperatorLinesDefault = 120
	OperatorLinesMax     = 300
	OperatorHoursDefault = 24
	OperatorHoursMax     = 168
	operatorReasonMax    = 500
	operatorServiceMax   = 128
)

func operatorSchema(required []string, properties map[string]any) json.RawMessage {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	raw, _ := json.Marshal(schema)
	return raw
}

const approved = " The person approves each call before it runs and may decline it."

// OperatorTools are the tools the operator is offered.
var OperatorTools = []Tool{
	{
		Name: ToolRecentRuns,
		Description: fmt.Sprintf("List the deployment's most recent runs on this environment, newest first (%d by default, at most %d): "+
			"each run's number, what it did, what started it, its state and the step it reached, when it ran, the commit, and for a failed run its code and reason."+approved,
			OperatorRunsDefault, OperatorRunsMax),
		Schema: operatorSchema(nil, map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": OperatorRunsMax}}),
	},
	{
		Name: ToolRunTranscript,
		Description: fmt.Sprintf("Read one run's transcript as its page shows it: each step's state and error, and the last lines the steps printed "+
			"(%d by default, at most %d), with secret values masked."+approved, OperatorLinesDefault, OperatorLinesMax),
		Schema: operatorSchema([]string{"run"}, map[string]any{
			"run":   map[string]any{"type": "integer", "minimum": 1, "description": "The run's number, as recent_runs gives it (#12 is 12)."},
			"lines": map[string]any{"type": "integer", "minimum": 1, "maximum": OperatorLinesMax},
		}),
	},
	{
		Name: ToolContainerLogs,
		Description: fmt.Sprintf("Read the last lines the live release's containers wrote (%d by default, at most %d), with secret values masked: "+
			"what the application printed, which is where a crash or a refused connection shows."+approved, OperatorLinesDefault, OperatorLinesMax),
		Schema: operatorSchema(nil, map[string]any{
			"lines":   map[string]any{"type": "integer", "minimum": 1, "maximum": OperatorLinesMax},
			"service": map[string]any{"type": "string", "description": "A Compose service's name, to read that service alone."},
		}),
	},
	{
		Name: ToolReleaseMetrics,
		Description: "Read the live release's containers' state, health, start time and memory limit, and the right-sizing verdict over the recorded history: " +
			"CPU and memory at p95 and peak against the limits, OOM kills and throttling, whether the limits are too tight or too loose, and what they should be." + approved,
		Schema: operatorSchema(nil, map[string]any{}),
	},
	{
		Name: ToolLifecycleEvents,
		Description: fmt.Sprintf("Read the deployment's container lifecycle events — starts, exits with their codes, OOM kills, health changes, restarts — "+
			"over the last hours (%d by default, at most %d), each named with what caused it when the dashboard knows."+approved, OperatorHoursDefault, OperatorHoursMax),
		Schema: operatorSchema(nil, map[string]any{"hours": map[string]any{"type": "integer", "minimum": 1, "maximum": OperatorHoursMax}}),
	},
	{
		Name:        ToolHostAlerts,
		Description: "Read the server's host alerts that are firing or waiting to be told: CPU or memory held high, a filesystem filling, a container down, a backup failed, a vulnerable image." + approved,
		Schema:      operatorSchema(nil, map[string]any{}),
	},
	{
		Name: ToolFirewallRules,
		Description: "Read the host firewall's state and the rules that concern the ports this deployment is reached on (the ingress's 80 and 443, and any port it publishes): " +
			"whether a rule blocks or allows them." + approved,
		Schema: operatorSchema(nil, map[string]any{}),
	},
	{
		Name:        ToolSecurityUpdates,
		Description: "Read the host's pending package updates that are security updates, by package, with the installed and the candidate version." + approved,
		Schema:      operatorSchema(nil, map[string]any{}),
	},
}

// OperatorInput is every field an operator tool takes; each tool's check
// clears what it does not read.
type OperatorInput struct {
	Limit   int    `json:"limit,omitempty"`
	Run     int64  `json:"run,omitempty"`
	Lines   int    `json:"lines,omitempty"`
	Service string `json:"service,omitempty"`
	Hours   int    `json:"hours,omitempty"`
}

// Input reads a call's checked input back.
func (c ToolCall) OperatorInput() OperatorInput {
	var in OperatorInput
	_ = json.Unmarshal(c.Input, &in)
	return in
}

func checkOperatorInput(tool string, input json.RawMessage) (OperatorInput, error) {
	var fields map[string]json.RawMessage
	if len(bytes.TrimSpace(input)) == 0 {
		input = json.RawMessage("{}")
	}
	if json.Unmarshal(input, &fields) != nil {
		return OperatorInput{}, ErrMalformedCall
	}
	allowed := map[string][]string{
		ToolRecentRuns: {"limit"}, ToolRunTranscript: {"run", "lines"}, ToolContainerLogs: {"lines", "service"},
		ToolReleaseMetrics: {}, ToolLifecycleEvents: {"hours"}, ToolHostAlerts: {}, ToolFirewallRules: {},
		ToolSecurityUpdates: {},
	}
	names, known := allowed[tool]
	if !known {
		return OperatorInput{}, ErrMalformedCall
	}
	for name := range fields {
		if !slices.Contains(names, name) {
			return OperatorInput{}, ErrMalformedCall
		}
	}
	var in OperatorInput
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return OperatorInput{}, ErrMalformedCall
	}
	bounded := func(value, low, high int) bool { return value == 0 || (value >= low && value <= high) }
	switch tool {
	case ToolRecentRuns:
		if !bounded(in.Limit, 1, OperatorRunsMax) {
			return OperatorInput{}, ErrMalformedCall
		}
		if in.Limit == 0 {
			in.Limit = OperatorRunsDefault
		}
	case ToolRunTranscript, ToolContainerLogs:
		if !bounded(in.Lines, 1, OperatorLinesMax) || (tool == ToolRunTranscript && in.Run <= 0) {
			return OperatorInput{}, ErrMalformedCall
		}
		if in.Lines == 0 {
			in.Lines = OperatorLinesDefault
		}
		in.Service = strings.TrimSpace(in.Service)
		if len(in.Service) > operatorServiceMax || strings.ContainsAny(in.Service, "\x00\r\n/") {
			return OperatorInput{}, ErrMalformedCall
		}
	case ToolLifecycleEvents:
		if !bounded(in.Hours, 1, OperatorHoursMax) {
			return OperatorInput{}, ErrMalformedCall
		}
		if in.Hours == 0 {
			in.Hours = OperatorHoursDefault
		}
	}
	return in, nil
}

// OperatorDeclined is what a call the person declined answers with.
const OperatorDeclined = "The person declined this. Nothing ran. Carry on with what you have, and say what you would need."

// OperatorContext is what the operator is told about the deployment when a
// conversation begins: names and states the page already shows, never a
// variable's value.
type OperatorContext struct {
	Server      string
	Deployment  string
	Environment string
	Kind        string
	Source      string
	Build       string
	Address     string
	Health      string
	LiveRelease int64
	ActiveRun   string
	LastRun     string
	Domains     []string
	WarmRelease int64
	Maintenance bool
}

// writeDeployment is the part of a prompt that says what the page shows.
func writeDeployment(b *strings.Builder, o OperatorContext) {
	b.WriteString("The deployment:\n")
	line := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			fmt.Fprintf(b, "- %s: %s\n", label, value)
		}
	}
	line("Server", o.Server)
	line("Deployment", o.Deployment)
	line("Environment", strings.TrimSpace(o.Environment+" ("+o.Kind+")"))
	line("Source", o.Source)
	line("Build", o.Build)
	line("Address", o.Address)
	if len(o.Domains) > 0 {
		line("Domains", strings.Join(o.Domains, ", "))
	}
	line("Health", o.Health)
	if o.LiveRelease > 0 {
		line("Live release", fmt.Sprintf("#%d", o.LiveRelease))
	} else {
		line("Live release", "none")
	}
	if o.WarmRelease > 0 {
		line("Warm release", fmt.Sprintf("#%d, kept since the last cutover", o.WarmRelease))
	}
	line("Run in progress", o.ActiveRun)
	line("Last run", o.LastRun)
	if o.Maintenance {
		line("Maintenance", "on")
	}
}
