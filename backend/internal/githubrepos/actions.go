package githubrepos

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	// maxWorkflowFile is the largest workflow file read for its inputs, the
	// contents API's own limit for an inline answer.
	maxWorkflowFile = 1 << 20
	// maxDispatchInputs is GitHub's limit on a workflow_dispatch's inputs.
	maxDispatchInputs = 25
	// maxInputValue bounds one input's value; GitHub's limit on the whole
	// payload is 65,535 characters.
	maxInputValue = 65535
	// workflowReads is how many workflow files are read at once.
	workflowReads = 4
)

// WorkflowInput is one input of a workflow_dispatch trigger.
type WorkflowInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	// Default is the value GitHub uses when none is given, as text.
	Default *string `json:"default,omitempty"`
	// Type is string, boolean, choice, number or environment.
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
}

// Dispatch is whether a workflow can be started from the page, and with
// which inputs.
type Dispatch struct {
	Available bool            `json:"available"`
	Inputs    []WorkflowInput `json:"inputs"`
	Message   string          `json:"message,omitempty"`
}

// Workflow is one GitHub Actions workflow.
type Workflow struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	State    string   `json:"state"`
	HTMLURL  string   `json:"htmlUrl"`
	Dispatch Dispatch `json:"dispatch"`
}

// WorkflowList is a repository's workflows, or a sentence when GitHub would
// not list them.
type WorkflowList struct {
	Workflows []Workflow `json:"workflows"`
	Readable  bool       `json:"readable"`
	Message   string     `json:"message,omitempty"`
}

type rawWorkflow struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
}

// Workflows lists the repository's workflows, each with what its file on the
// default branch says about workflow_dispatch. Only the default branch is
// read: a branch that adds or changes inputs shows them once merged, and a
// dispatch is checked again against the branch it runs on.
func (a *RepoAccess) Workflows(ctx context.Context) (*WorkflowList, error) {
	var raw []rawWorkflow
	_, err := a.Client.Walk(ctx, a.Path("/actions/workflows?per_page=100"), func(page []byte) error {
		var batch struct {
			Workflows []rawWorkflow `json:"workflows"`
		}
		if err := json.Unmarshal(page, &batch); err != nil {
			return err
		}
		raw = append(raw, batch.Workflows...)
		return nil
	})
	if err != nil {
		if unreadable(err) {
			return &WorkflowList{Workflows: []Workflow{}, Message: refusalWords(err, "this repository's workflows")}, nil
		}
		return nil, err
	}
	out := &WorkflowList{Workflows: make([]Workflow, len(raw)), Readable: true}
	var (
		wg       sync.WaitGroup
		slots    = make(chan struct{}, workflowReads)
		mu       sync.Mutex
		firstErr error
	)
	for i, item := range raw {
		out.Workflows[i] = Workflow{ID: item.ID, Name: item.Name, Path: item.Path, State: item.State, HTMLURL: item.HTMLURL, Dispatch: Dispatch{Inputs: []WorkflowInput{}}}
		if reason := notDispatchable(item); reason != "" {
			out.Workflows[i].Dispatch.Message = reason
			continue
		}
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			dispatch, err := a.readDispatch(ctx, path, a.Repository.DefaultBranch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			out.Workflows[i].Dispatch = dispatch
		}(i, item.Path)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// notDispatchable is why a workflow cannot be started at all, before its
// file is read: switched off, or not a file in the repository (GitHub's own
// dynamic workflows, such as Dependabot's, have a path but no file).
func notDispatchable(workflow rawWorkflow) string {
	switch {
	case workflow.State != "" && workflow.State != "active":
		return "This workflow is switched off on GitHub (" + strings.ReplaceAll(workflow.State, "_", " ") + ")."
	case !strings.HasPrefix(workflow.Path, ".github/workflows/"):
		return "GitHub runs this workflow itself; it has no file to start it from."
	}
	return ""
}

// readDispatch reads one workflow file at ref and works out its dispatch
// trigger. A file GitHub would not show, or that does not parse, is not
// dispatchable, with the reason; a rate limit or a refused sign-in is an
// error.
func (a *RepoAccess) readDispatch(ctx context.Context, path, ref string) (Dispatch, error) {
	none := func(message string) Dispatch { return Dispatch{Inputs: []WorkflowInput{}, Message: message} }
	contentsPath := a.Path("/contents/" + EscapeRef(path))
	if ref != "" {
		contentsPath += "?ref=" + url.QueryEscape(ref)
	}
	var file struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
		SHA      string `json:"sha"`
		Size     int    `json:"size"`
	}
	if _, err := a.Client.Do(ctx, http.MethodGet, contentsPath, nil, &file); err != nil {
		if unreadable(err) {
			return none("The workflow file could not be read on " + ref + "."), nil
		}
		return Dispatch{}, err
	}
	if file.Type != "file" || file.Encoding != "base64" || file.Size > maxWorkflowFile {
		return none("The workflow file is too large to read its inputs."), nil
	}
	key := a.Repository.FullName + "\x00" + path + "\x00" + file.SHA
	if cached, ok := dispatchCache.get(key); ok {
		return cached, nil
	}
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return none("GitHub's copy of the workflow file was not readable."), nil
	}
	dispatch := ParseDispatch(content)
	if file.SHA != "" {
		dispatchCache.put(key, dispatch)
	}
	return dispatch, nil
}

// ParseDispatch reads a workflow file's workflow_dispatch trigger and its
// inputs. `on` may be one event, a list of them, or a map of events to their
// settings. YAML 1.1 readers turn a bare `on` key into true; yaml.v3 keeps
// it a string, and a key written as true is accepted too.
func ParseDispatch(content []byte) Dispatch {
	none := func(message string) Dispatch { return Dispatch{Inputs: []WorkflowInput{}, Message: message} }
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return none("The workflow file is not valid YAML: " + strings.TrimPrefix(err.Error(), "yaml: "))
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return none("The workflow file is not a workflow.")
	}
	var on *yaml.Node
	root := document.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if key.Kind == yaml.ScalarNode && (key.Value == "on" || (key.Tag == "!!bool" && strings.EqualFold(key.Value, "true"))) {
			on = root.Content[i+1]
		}
	}
	noTrigger := none("This workflow has no workflow_dispatch trigger, so only its own events start it.")
	if on == nil {
		return none("The workflow file names no events (`on`).")
	}
	switch on.Kind {
	case yaml.ScalarNode:
		if on.Value == "workflow_dispatch" {
			return Dispatch{Available: true, Inputs: []WorkflowInput{}}
		}
		return noTrigger
	case yaml.SequenceNode:
		for _, event := range on.Content {
			if event.Kind == yaml.ScalarNode && event.Value == "workflow_dispatch" {
				return Dispatch{Available: true, Inputs: []WorkflowInput{}}
			}
		}
		return noTrigger
	case yaml.MappingNode:
	default:
		return noTrigger
	}
	trigger := mappingValue(on, "workflow_dispatch")
	if trigger == nil {
		return noTrigger
	}
	out := Dispatch{Available: true, Inputs: []WorkflowInput{}}
	inputs := mappingValue(trigger, "inputs")
	if inputs == nil || inputs.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(inputs.Content); i += 2 {
		name := inputs.Content[i].Value
		spec := inputs.Content[i+1]
		input := WorkflowInput{Name: name, Type: "string"}
		if spec.Kind == yaml.MappingNode {
			if node := mappingValue(spec, "description"); node != nil && node.Kind == yaml.ScalarNode {
				input.Description = node.Value
			}
			if node := mappingValue(spec, "required"); node != nil && node.Kind == yaml.ScalarNode {
				input.Required = strings.EqualFold(node.Value, "true")
			}
			if node := mappingValue(spec, "type"); node != nil && node.Kind == yaml.ScalarNode {
				switch node.Value {
				case "string", "boolean", "choice", "number", "environment":
					input.Type = node.Value
				}
			}
			if node := mappingValue(spec, "default"); node != nil && node.Kind == yaml.ScalarNode && node.Tag != "!!null" {
				value := node.Value
				input.Default = &value
			}
			if node := mappingValue(spec, "options"); node != nil && node.Kind == yaml.SequenceNode {
				for _, option := range node.Content {
					if option.Kind == yaml.ScalarNode {
						input.Options = append(input.Options, option.Value)
					}
				}
			}
		}
		out.Inputs = append(out.Inputs, input)
	}
	if len(out.Inputs) > maxDispatchInputs {
		return none(fmt.Sprintf("The workflow declares %d inputs; GitHub accepts at most %d.", len(out.Inputs), maxDispatchInputs))
	}
	return out
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Kind == yaml.ScalarNode && node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// dispatchCache keeps parsed workflow files by repository, path and blob
// sha. The sha names the content, so an entry can never be stale; the bound
// keeps a dashboard with many repositories from holding every file it ever
// read.
var dispatchCache = &boundedCache{max: 512, entries: map[string]Dispatch{}}

type boundedCache struct {
	mu      sync.Mutex
	max     int
	order   []string
	entries map[string]Dispatch
}

func (c *boundedCache) get(key string) (Dispatch, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.entries[key]
	if ok {
		// A copy, so the caller cannot change what the next one reads.
		value.Inputs = append([]WorkflowInput{}, value.Inputs...)
	}
	return value, ok
}

func (c *boundedCache) put(key string, value Dispatch) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; ok {
		return
	}
	if len(c.order) >= c.max {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
	c.order = append(c.order, key)
	c.entries[key] = Dispatch{Available: value.Available, Message: value.Message, Inputs: append([]WorkflowInput{}, value.Inputs...)}
}

// Run is one workflow run.
type Run struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Title      string    `json:"title"`
	WorkflowID int64     `json:"workflowId"`
	Event      string    `json:"event"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion,omitempty"`
	Branch     string    `json:"branch"`
	SHA        string    `json:"sha"`
	Actor      string    `json:"actor,omitempty"`
	RunNumber  int64     `json:"runNumber"`
	RunAttempt int64     `json:"runAttempt"`
	CreatedAt  time.Time `json:"createdAt,omitzero"`
	UpdatedAt  time.Time `json:"updatedAt,omitzero"`
	HTMLURL    string    `json:"htmlUrl"`
}

// RunPage is one page of runs.
type RunPage struct {
	Runs    []Run `json:"runs"`
	HasMore bool  `json:"hasMore"`
}

// Runs reads one page of 30 runs, newest first: the repository's, or one
// workflow's when workflow is not zero, on one branch when branch is set.
func (a *RepoAccess) Runs(ctx context.Context, workflow int64, branch string, page int) (*RunPage, error) {
	if workflow < 0 {
		return nil, invalid("a workflow id must be positive")
	}
	if branch != "" {
		if err := ValidBranchName(branch); err != nil {
			return nil, err
		}
	}
	path := a.Path("/actions/runs")
	if workflow > 0 {
		path = a.Path(fmt.Sprintf("/actions/workflows/%d/runs", workflow))
	}
	var raw struct {
		WorkflowRuns []struct {
			ID           int64      `json:"id"`
			Name         string     `json:"name"`
			DisplayTitle string     `json:"display_title"`
			WorkflowID   int64      `json:"workflow_id"`
			Event        string     `json:"event"`
			Status       string     `json:"status"`
			Conclusion   *string    `json:"conclusion"`
			HeadBranch   string     `json:"head_branch"`
			HeadSHA      string     `json:"head_sha"`
			Actor        *rawUser   `json:"actor"`
			RunNumber    int64      `json:"run_number"`
			RunAttempt   int64      `json:"run_attempt"`
			CreatedAt    *time.Time `json:"created_at"`
			UpdatedAt    *time.Time `json:"updated_at"`
			HTMLURL      string     `json:"html_url"`
		} `json:"workflow_runs"`
	}
	more, err := a.Client.Page(ctx, pagedPath(path, 30, page, url.Values{"branch": {branch}}), &raw)
	if err != nil {
		return nil, err
	}
	out := &RunPage{Runs: make([]Run, 0, len(raw.WorkflowRuns)), HasMore: more}
	for _, run := range raw.WorkflowRuns {
		item := Run{
			ID: run.ID, Name: run.Name, Title: run.DisplayTitle, WorkflowID: run.WorkflowID, Event: run.Event,
			Status: run.Status, Branch: run.HeadBranch, SHA: run.HeadSHA, Actor: loginOf(run.Actor),
			RunNumber: run.RunNumber, RunAttempt: run.RunAttempt, CreatedAt: timeOf(run.CreatedAt),
			UpdatedAt: timeOf(run.UpdatedAt), HTMLURL: run.HTMLURL,
		}
		if run.Conclusion != nil {
			item.Conclusion = *run.Conclusion
		}
		out.Runs = append(out.Runs, item)
	}
	return out, nil
}

// DispatchRequest starts a workflow on ref with inputs.
type DispatchRequest struct {
	Ref    string            `json:"ref"`
	Inputs map[string]string `json:"inputs"`
}

// DispatchResult is the run GitHub started, when it said which.
type DispatchResult struct {
	RunID   int64  `json:"runId,omitempty"`
	HTMLURL string `json:"htmlUrl,omitempty"`
}

// Dispatch starts a workflow. The workflow and its file on ref are read
// again rather than trusting what the page was shown: a workflow without a
// dispatch trigger, a required input left out, an input the file does not
// declare and a value that is not one of a choice's options are refused
// here with the reason, before GitHub is asked.
func (a *RepoAccess) Dispatch(ctx context.Context, workflow int64, request DispatchRequest) (*DispatchResult, error) {
	if workflow <= 0 {
		return nil, invalid("a workflow id must be positive")
	}
	ref := strings.TrimSpace(request.Ref)
	if ref == "" {
		ref = a.Repository.DefaultBranch
	}
	if err := ValidBranchName(ref); err != nil {
		return nil, err
	}
	if len(request.Inputs) > maxDispatchInputs {
		return nil, invalid("at most %d inputs", maxDispatchInputs)
	}
	for name, value := range request.Inputs {
		if utf8.RuneCountInString(value) > maxInputValue {
			return nil, invalid("the value of %s is too long", name)
		}
	}
	if err := a.Require(a.Repository.Can.Actions, "actions"); err != nil {
		return nil, err
	}
	var raw rawWorkflow
	if _, err := a.Client.Do(ctx, http.MethodGet, a.Path(fmt.Sprintf("/actions/workflows/%d", workflow)), nil, &raw); err != nil {
		return nil, err
	}
	if reason := notDispatchable(raw); reason != "" {
		return nil, invalid("%s", reason)
	}
	dispatch, err := a.readDispatch(ctx, raw.Path, ref)
	if err != nil {
		return nil, err
	}
	if !dispatch.Available {
		return nil, invalid("%s cannot be started from here on %s: %s", raw.Name, ref, dispatch.Message)
	}
	inputs, err := checkInputs(dispatch.Inputs, request.Inputs)
	if err != nil {
		return nil, err
	}
	// ref and inputs are the only fields GitHub documents for this call; an
	// undocumented one risks a 422 for every dispatch.
	body := map[string]any{"ref": ref}
	if len(inputs) > 0 {
		body["inputs"] = inputs
	}
	// GitHub documents a 200 naming the run it started; an older answer was
	// a 204 with nothing, which leaves the run to be found in the list.
	var started struct {
		WorkflowRunID int64  `json:"workflow_run_id"`
		HTMLURL       string `json:"html_url"`
	}
	if _, err := a.Client.Do(ctx, http.MethodPost, a.Path(fmt.Sprintf("/actions/workflows/%d/dispatches", workflow)), body, &started); err != nil {
		return nil, err
	}
	return &DispatchResult{RunID: started.WorkflowRunID, HTMLURL: webURL(started.HTMLURL)}, nil
}

// checkInputs holds the values to what the file declares. A value left out
// is left to the input's default; a required one with no default must be
// given.
func checkInputs(declared []WorkflowInput, given map[string]string) (map[string]string, error) {
	byName := map[string]WorkflowInput{}
	for _, input := range declared {
		byName[input.Name] = input
	}
	out := map[string]string{}
	for name, value := range given {
		input, ok := byName[name]
		if !ok {
			return nil, invalid("the workflow has no input called %q", name)
		}
		switch input.Type {
		case "choice":
			found := false
			for _, option := range input.Options {
				if option == value {
					found = true
				}
			}
			if !found {
				return nil, invalid("%q is not one of the options of %s", value, name)
			}
		case "boolean":
			if value != "true" && value != "false" {
				return nil, invalid("%s is true or false, not %q", name, value)
			}
		case "number":
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return nil, invalid("%s is a number, not %q", name, value)
			}
		}
		out[name] = value
	}
	for _, input := range declared {
		value, given := out[input.Name]
		if input.Required && (!given || value == "") && (input.Default == nil || *input.Default == "") {
			return nil, invalid("%s is required", input.Name)
		}
	}
	return out, nil
}

// Rerun starts a run again: all of it, or only its failed jobs.
func (a *RepoAccess) Rerun(ctx context.Context, run int64, failedOnly bool) error {
	if run <= 0 {
		return invalid("a run id must be positive")
	}
	if err := a.Require(a.Repository.Can.Actions, "actions"); err != nil {
		return err
	}
	suffix := "/rerun"
	if failedOnly {
		suffix = "/rerun-failed-jobs"
	}
	_, err := a.Client.Request(ctx, http.MethodPost, a.Path(fmt.Sprintf("/actions/runs/%d%s", run, suffix)), nil)
	return err
}

// CancelRun stops a run that is queued or in progress. GitHub answers 409
// for one that already finished.
func (a *RepoAccess) CancelRun(ctx context.Context, run int64) error {
	if run <= 0 {
		return invalid("a run id must be positive")
	}
	if err := a.Require(a.Repository.Can.Actions, "actions"); err != nil {
		return err
	}
	_, err := a.Client.Request(ctx, http.MethodPost, a.Path(fmt.Sprintf("/actions/runs/%d/cancel", run)), nil)
	return err
}
