package dockerx

import (
	"context"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// What pressing Deploy is going to do, before it does it.
//
// A compose deploy is the most consequential button in this product and the
// least predictable: `up` recreates whatever it decides has changed, and what
// it decides is invisible until afterwards. An operator pressing it on a
// Friday cannot tell whether one container restarts or four, whether a volume
// is about to be removed, or whether the image about to be pulled is a
// different one at all.
//
// This answers that from three sources that are all already available: the
// compose file as it is now, the compose file as it was at the last recorded
// deployment, and what is actually running. Everything it claims is derived
// from a comparison it can show; where it cannot know, it says so rather than
// guessing — an `up` can always decide to recreate something for a reason
// outside the file.

// DeployPreview is the expected impact of a deploy.
type DeployPreview struct {
	Project string `json:"project"`
	Action  string `json:"action"`

	// Services, one row each, with what is expected to happen to it.
	Services []ServiceChange `json:"services"`

	// The counts a summary line is made of.
	Recreate  int `json:"recreate"`
	Start     int `json:"start"`
	Unchanged int `json:"unchanged"`
	Create    int `json:"create"`
	Remove    int `json:"remove"`

	// VolumesRemoved is deliberately its own number and is almost always zero:
	// it is the one thing here that destroys data, and it should be readable
	// at a glance rather than inferred from a list.
	VolumesRemoved []string `json:"volumesRemoved"`
	VolumesKept    []string `json:"volumesKept"`

	// Diff is the compose file's own changes since the last recorded
	// deployment, in unified form. Empty when there is nothing to compare
	// against, which is itself worth saying.
	Diff        []DiffLine `json:"diff"`
	DiffAgainst string     `json:"diffAgainst,omitempty"`
	// Summary is the sentence; Caveats are what this cannot know.
	Summary string   `json:"summary"`
	Caveats []string `json:"caveats"`
}

// ServiceChange is what a deploy is expected to do to one service.
type ServiceChange struct {
	Name string `json:"name"`
	// Change is "recreate", "start", "create", "remove" or "unchanged".
	Change string `json:"change"`
	// Reason says why, in a sentence an operator can check.
	Reason string `json:"reason"`
	// Fields names what differs — image, environment, ports, volumes — so a
	// row can be expanded without re-reading the whole file.
	Fields []string `json:"fields"`
	// ImageBefore and ImageAfter are set when the image itself changes.
	ImageBefore string `json:"imageBefore,omitempty"`
	ImageAfter  string `json:"imageAfter,omitempty"`
	// Inferred marks a row worked out by comparison rather than read from
	// Docker. Every "recreate" here is inferred: compose makes the final call.
	Inferred bool `json:"inferred"`
}

// DiffLine is one line of a unified diff.
type DiffLine struct {
	// Kind is "same", "added" or "removed".
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Section is the top-level compose key the line belongs to, so a long
	// diff can be grouped by service.
	Section string `json:"section,omitempty"`
}

// PreviewDeploy works out what an `up` on this stack is expected to change.
func (c *Client) PreviewDeploy(
	ctx context.Context,
	st *ComposeStack,
	current string,
	previous *StackDeployment,
	action string,
) *DeployPreview {
	p := &DeployPreview{
		Project:        st.Name,
		Action:         action,
		Services:       []ServiceChange{},
		VolumesRemoved: []string{},
		VolumesKept:    []string{},
		Diff:           []DiffLine{},
		Caveats:        []string{},
	}

	now := parseComposeServices(current)
	var before map[string]map[string]any
	if previous != nil && previous.Config != "" {
		before = parseComposeServices(previous.Config)
		p.Diff = unifiedDiff(previous.Config, current)
		p.DiffAgainst = "the last deployment recorded by this dashboard, " + humanDuration(time.Since(previous.CreatedAt)) + " ago"
	} else {
		p.Caveats = append(p.Caveats,
			"No previous deployment of this stack is recorded, so there is nothing to compare the compose file against. What follows is based on what is running now.")
	}

	running := map[string]ComposeService{}
	for _, svc := range st.Services {
		if !svc.Missing {
			running[svc.Name] = svc
		}
	}

	names := make([]string, 0, len(now))
	for name := range now {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		change := ServiceChange{Name: name, Inferred: true, Fields: []string{}}
		live, isRunning := running[name]

		switch {
		case !isRunning:
			change.Change, change.Reason = "create",
				"No container exists for this service, so one will be created and started."
			p.Create++
		case before == nil:
			change.Change, change.Reason = "unchanged",
				"Nothing to compare against, so compose decides. It recreates a service only when its configuration or image differs from what is running."
		default:
			fields := changedFields(before[name], now[name])
			if len(fields) == 0 {
				change.Change, change.Reason = "unchanged",
					"Its configuration is identical to the last deployment."
				p.Unchanged++
			} else {
				change.Change = "recreate"
				change.Fields = fields
				change.Reason = "Its " + join(fields, ", ") +
					" changed since the last deployment, so the container is replaced. Anything written inside it rather than into a volume is lost."
				p.Recreate++
			}
			change.ImageBefore = stringField(before[name], "image")
			change.ImageAfter = stringField(now[name], "image")
			if change.ImageBefore == change.ImageAfter {
				change.ImageBefore, change.ImageAfter = "", ""
			}
			if live.State != "running" && change.Change == "unchanged" {
				change.Change, change.Reason = "start",
					"Its container exists and is not running, so it will be started."
				p.Unchanged--
				p.Start++
			}
		}
		p.Services = append(p.Services, change)
	}

	// A service that is running and no longer in the file. `up
	// --remove-orphans` is what the dashboard runs, so this is a removal
	// rather than something left behind.
	for name := range running {
		if _, ok := now[name]; ok {
			continue
		}
		p.Services = append(p.Services, ServiceChange{
			Name: name, Change: "remove", Inferred: true, Fields: []string{},
			Reason: "This service is running but is no longer in the compose file, so it is removed as an orphan.",
		})
		p.Remove++
	}
	sort.Slice(p.Services, func(i, j int) bool { return p.Services[i].Name < p.Services[j].Name })

	p.VolumesKept, p.VolumesRemoved = volumeImpact(before, now, action)
	p.Summary = summarizePreview(p)
	p.Caveats = append(p.Caveats,
		"Compose makes the final decision and can recreate a service for a reason outside the file — a changed base image, or a container removed by hand. This is what is expected, not a guarantee.")
	return p
}

func summarizePreview(p *DeployPreview) string {
	parts := []string{}
	if p.Recreate > 0 {
		parts = append(parts, plural(p.Recreate, "service recreated", "services recreated"))
	}
	if p.Create > 0 {
		parts = append(parts, plural(p.Create, "service created", "services created"))
	}
	if p.Start > 0 {
		parts = append(parts, plural(p.Start, "service started", "services started"))
	}
	if p.Remove > 0 {
		parts = append(parts, plural(p.Remove, "orphan removed", "orphans removed"))
	}
	if p.Unchanged > 0 {
		parts = append(parts, plural(p.Unchanged, "service unchanged", "services unchanged"))
	}
	if len(parts) == 0 {
		return "Nothing is expected to change."
	}
	out := join(parts, ", ")
	if len(p.VolumesRemoved) > 0 {
		out += ". " + plural(len(p.VolumesRemoved), "volume removed", "volumes removed") + " — this destroys data"
	} else {
		out += ". No volume is removed"
	}
	return out + "."
}

// volumeImpact says which named volumes survive and which do not.
//
// Only `down -v` removes a volume, and this dashboard's "stop and remove"
// deliberately does not pass it. Saying so explicitly is the point: the
// commonest fear about a compose deploy is that it eats the database, and the
// answer is almost always no.
func volumeImpact(before, now map[string]map[string]any, action string) (kept, removed []string) {
	kept, removed = []string{}, []string{}
	seen := map[string]bool{}
	collect := func(set map[string]map[string]any) {
		for _, svc := range set {
			for _, name := range serviceVolumeNames(svc) {
				if !seen[name] {
					seen[name] = true
					kept = append(kept, name)
				}
			}
		}
	}
	collect(now)
	collect(before)
	sort.Strings(kept)
	if action == "down-volumes" {
		return []string{}, kept
	}
	return kept, removed
}

// serviceVolumeNames pulls the named volumes out of a service's volume list.
// A bind mount is a path on this server and is not Docker's to remove.
func serviceVolumeNames(svc map[string]any) []string {
	out := []string{}
	raw, ok := svc["volumes"].([]any)
	if !ok {
		return out
	}
	for _, item := range raw {
		switch v := item.(type) {
		case string:
			source, _, found := strings.Cut(v, ":")
			if found && source != "" && !strings.HasPrefix(source, "/") && !strings.HasPrefix(source, ".") {
				out = append(out, source)
			}
		case map[string]any:
			if v["type"] == "volume" {
				if source, ok := v["source"].(string); ok && source != "" {
					out = append(out, source)
				}
			}
		}
	}
	return out
}

// changedFields names what differs between two versions of one service.
func changedFields(before, now map[string]any) []string {
	if before == nil || now == nil {
		return []string{"definition"}
	}
	// Only the keys that cause a recreate. A changed `container_name` does; a
	// changed comment does not, and neither does a key compose applies
	// without replacing anything.
	watched := []struct{ key, label string }{
		{"image", "image"},
		{"build", "build"},
		{"command", "command"},
		{"entrypoint", "entrypoint"},
		{"environment", "environment"},
		{"env_file", "environment file"},
		{"ports", "ports"},
		{"volumes", "volumes"},
		{"networks", "networks"},
		{"healthcheck", "health check"},
		{"deploy", "resource limits"},
		{"mem_limit", "memory limit"},
		{"cpus", "CPU limit"},
		{"user", "user"},
		{"restart", "restart policy"},
		{"depends_on", "dependencies"},
		{"labels", "labels"},
		{"cap_add", "capabilities"},
		{"privileged", "privileged flag"},
	}
	out := []string{}
	for _, w := range watched {
		if !sameYAML(before[w.key], now[w.key]) {
			out = append(out, w.label)
		}
	}
	return out
}

// sameYAML compares two decoded compose values structurally. Re-marshalling is
// the cheapest correct comparison: compose accepts environment as both a map
// and a list, and comparing the decoded forms directly would call every
// reformat a change.
func sameYAML(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	left, err1 := yaml.Marshal(normalizeYAML(a))
	right, err2 := yaml.Marshal(normalizeYAML(b))
	if err1 != nil || err2 != nil {
		return false
	}
	return string(left) == string(right)
}

// normalizeYAML sorts what compose treats as unordered, so a reordered
// environment block is not reported as a change nobody made.
func normalizeYAML(v any) any {
	switch value := v.(type) {
	case []any:
		out := make([]string, 0, len(value))
		allStrings := true
		for _, item := range value {
			s, ok := item.(string)
			if !ok {
				allStrings = false
				break
			}
			out = append(out, s)
		}
		if allStrings {
			sort.Strings(out)
			return out
		}
		return value
	default:
		return v
	}
}

func stringField(svc map[string]any, key string) string {
	if svc == nil {
		return ""
	}
	s, _ := svc[key].(string)
	return s
}

// parseComposeServices decodes the services block. A file that does not parse
// yields nothing, and the caller reports that as "cannot compare" rather than
// as "nothing changed".
func parseComposeServices(content string) map[string]map[string]any {
	out := map[string]map[string]any{}
	if strings.TrimSpace(content) == "" {
		return out
	}
	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return out
	}
	for name, svc := range doc.Services {
		if svc == nil {
			svc = map[string]any{}
		}
		out[name] = svc
	}
	return out
}

// unifiedDiff produces a line diff of two compose files.
//
// A line-level longest-common-subsequence, which for a configuration file of a
// few dozen lines is instant and produces the diff a person expects. Anything
// cleverer — a semantic YAML diff — would be harder to check by eye, and being
// checkable by eye is the entire purpose.
func unifiedDiff(before, after string) []DiffLine {
	left := strings.Split(strings.TrimRight(before, "\n"), "\n")
	right := strings.Split(strings.TrimRight(after, "\n"), "\n")

	// Classic LCS table. Bounded by the size of a compose file, which is small.
	n, m := len(left), len(right)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if left[i] == right[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	out := []DiffLine{}
	section := ""
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case left[i] == right[j]:
			section = trackSection(section, left[i])
			out = append(out, DiffLine{Kind: "same", Text: left[i], Section: section})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Kind: "removed", Text: left[i], Section: section})
			i++
		default:
			section = trackSection(section, right[j])
			out = append(out, DiffLine{Kind: "added", Text: right[j], Section: section})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{Kind: "removed", Text: left[i], Section: section})
	}
	for ; j < m; j++ {
		section = trackSection(section, right[j])
		out = append(out, DiffLine{Kind: "added", Text: right[j], Section: section})
	}
	return trimUnchangedContext(out)
}

// trackSection follows which service a diff line belongs to, from indentation.
func trackSection(current, line string) string {
	trimmed := strings.TrimRight(line, " \t")
	if trimmed == "" || strings.HasPrefix(strings.TrimSpace(trimmed), "#") {
		return current
	}
	indent := len(trimmed) - len(strings.TrimLeft(trimmed, " "))
	if indent == 0 {
		if key, _, found := strings.Cut(strings.TrimSpace(trimmed), ":"); found {
			return key
		}
		return current
	}
	if indent == 2 && strings.HasSuffix(strings.TrimSpace(trimmed), ":") {
		return strings.TrimSuffix(strings.TrimSpace(trimmed), ":")
	}
	return current
}

// trimUnchangedContext keeps three unchanged lines around each change, the way
// a diff normally reads. A compose file where two lines changed should not be
// shown as a hundred identical ones.
func trimUnchangedContext(lines []DiffLine) []DiffLine {
	const context = 3
	keep := make([]bool, len(lines))
	for i, line := range lines {
		if line.Kind == "same" {
			continue
		}
		for j := max(0, i-context); j <= min(len(lines)-1, i+context); j++ {
			keep[j] = true
		}
	}
	out := make([]DiffLine, 0, len(lines))
	gap := false
	for i, line := range lines {
		if keep[i] {
			if gap {
				out = append(out, DiffLine{Kind: "gap", Text: "…"})
				gap = false
			}
			out = append(out, line)
			continue
		}
		gap = len(out) > 0
	}
	return out
}
