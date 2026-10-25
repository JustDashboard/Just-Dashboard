package mcp

import "fmt"

// The deployment tools, by name. They act on a deployment's production
// environment, the one its page leads with: a preview is created, approved and
// torn down by its own lifecycle, and none of that is offered here.
const (
	ToolListDeployments = "list_deployments"
	ToolListRuns        = "list_runs"
	ToolReadRunLog      = "read_run_log"
	ToolListReleases    = "list_releases"
	ToolDeploy          = "deploy"
	ToolRollback        = "rollback"
)

// The bounds the deployment tools are held to, which their descriptions state.
const (
	RunsDefault     = 10
	RunsMax         = 50
	ReleasesDefault = 10
	ReleasesMax     = 30
	LogDefault      = 200
	LogMax          = 1000
	// LineBytes is the most of one transcript line a read carries; the whole
	// read is held to ResultBytes like a result set.
	LineBytes = CellBytes
)

// IsDeployTool reports whether a tool acts on the deployments, which decides
// whether a key is offered it and what its audit entry is named for.
func IsDeployTool(name string) bool {
	switch name {
	case ToolListDeployments, ToolListRuns, ToolReadRunLog, ToolListReleases, ToolDeploy, ToolRollback:
		return true
	}
	return false
}

var deploymentProperty = map[string]any{
	"type":        "string",
	"description": "The deployment, by the name list_deployments gives.",
}

var deployTools = []Tool{
	{
		Name:  ToolListDeployments,
		Title: "List deployments",
		Description: "List the deployments: each one's name, what it deploys (source, repository, branch and build), and its production environment's health, address, live release, " +
			"whether settings are waiting for a deploy, the run in progress and the last run's outcome. Archived deployments and preview environments are not listed.",
		InputSchema: object(nil, map[string]any{}),
		Annotations: reading("List deployments"),
	},
	{
		Name:  ToolListRuns,
		Title: "List runs",
		Description: fmt.Sprintf("List a deployment's runs on its production environment, newest first, %d by default and at most %d: each run's number, what it did "+
			"(deploy, redeploy, rollback, restart…), what started it and who, its state and current step, when it was requested and ended, the commit, and for a failed run its code and reason.", RunsDefault, RunsMax),
		InputSchema: object([]string{"deployment"}, map[string]any{
			"deployment": deploymentProperty,
			"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": RunsMax},
		}),
		Annotations: reading("List runs"),
	},
	{
		Name:  ToolReadRunLog,
		Title: "Read a run's transcript",
		Description: fmt.Sprintf("Read one run's transcript as its page in the dashboard shows it: the run, each step's state and error, and the last lines the steps printed, "+
			"%d by default and at most %d, each cut at %d bytes. They are the lines the run's page shows any signed-in account, with the build's secret values masked as they were written. "+
			"Without run, the deployment's newest run is read.", LogDefault, LogMax, LineBytes),
		InputSchema: object([]string{"deployment"}, map[string]any{
			"deployment": deploymentProperty,
			"run":        map[string]any{"type": "integer", "minimum": 1, "description": "The run's number, as list_runs gives it (#12 is 12)."},
			"lines":      map[string]any{"type": "integer", "minimum": 1, "maximum": LogMax},
		}),
		Annotations: reading("Read a run's transcript"),
	},
	{
		Name:  ToolListReleases,
		Title: "List releases",
		Description: fmt.Sprintf("List the releases of a deployment's production environment, newest first, %d by default and at most %d: each release's id, number, state "+
			"(live, retained or retired), commit, image digest, when it was made and went live, and whether it is pinned. A rollback goes to a retained release.", ReleasesDefault, ReleasesMax),
		InputSchema: object([]string{"deployment"}, map[string]any{
			"deployment": deploymentProperty,
			"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": ReleasesMax},
		}),
		Annotations: reading("List releases"),
	},
	{
		Name:  ToolDeploy,
		Title: "Deploy",
		Description: "Queue a deploy of a deployment's production environment and return the run it queued. It builds the configured branch's head, or for a Git source a branch or tag " +
			"named by ref or a commit named by revision (not both). It is held to what the key's owner may do on the deployment pages: the same checks and backup gate run, and a pull request's head " +
			"is never deployed to production. It returns once the run is queued; follow it with read_run_log.",
		InputSchema: object([]string{"deployment"}, map[string]any{
			"deployment": deploymentProperty,
			"ref":        map[string]any{"type": "string", "description": "A branch or tag of the deployment's Git repository to build instead of its configured branch."},
			"revision":   map[string]any{"type": "string", "description": "A full commit id of the deployment's Git repository to build."},
		}),
		Annotations: Annotations{Title: "Deploy"},
	},
	{
		Name:  ToolRollback,
		Title: "Roll back",
		Description: "Queue a rollback of a deployment's production environment to a retained release: the one release names (an id from list_releases), or without it the newest " +
			"retained release older than the live one. The release's own image, configuration and variables go live again, and the run is returned once it is queued. " +
			"It needs a role that may take destructive actions, as the dashboard's Roll back does, and deployments that predate releases are rolled back from the dashboard.",
		InputSchema: object([]string{"deployment"}, map[string]any{
			"deployment": deploymentProperty,
			"release":    map[string]any{"type": "integer", "minimum": 1, "description": "The release to go back to, by the id list_releases gives."},
		}),
		Annotations: Annotations{Title: "Roll back", DestructiveHint: true},
	},
}
