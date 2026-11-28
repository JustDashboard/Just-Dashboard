package assistant

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// Working on code: a deployment's repository cloned into a checkout of its own
// on the server (open_workspace), its files read and changed (read_file,
// edit_file, write_file) and the result committed and pushed (push_changes), from
// where a push deploys it or a run is started. With run_command in the checkout
// for building and testing, that is what a person would do in a terminal and an
// editor, which is what these stand for.
//
// The files are read and written through the dashboard's Files routes, as the
// person, so their role, the Files page's roots, its kept versions and the audit
// log apply as to them; the checkout is made and pushed through Local Git's
// service, as the folder's owner. What these read is masked like any other read.

const (
	ToolOpenWorkspace = "open_workspace"
	ToolReadFile      = "read_file"
	ToolWriteFile     = "write_file"
	ToolEditFile      = "edit_file"
	ToolPushChanges   = "push_changes"
)

// Bounds on the file tools, which their descriptions state.
const (
	FileLinesDefault = 400
	FileLinesMax     = 2000
	// FileWriteMax is the most one write_file or edit_file sends.
	FileWriteMax = 1 << 20
	filePathMax  = 4096
	commitMax    = 2000
	branchMax    = 200
)

var (
	openWorkspaceTool = func(named bool) Tool {
		properties := map[string]any{
			"branch": map[string]any{"type": "string", "maxLength": branchMax, "description": "A branch to check out instead of the one the deployment builds."},
			"reason": map[string]any{"type": "string", "maxLength": operatorReasonMax, "description": "One short sentence the person sees beside the call."},
		}
		var required []string
		if named {
			properties["deployment"] = map[string]any{"type": "integer", "minimum": 1, "description": "The deployment's id, as your list gives it."}
			required = []string{"deployment"}
		} else {
			properties["deployment"] = map[string]any{"type": "integer", "minimum": 1, "description": "The deployment's id, for one other than this conversation's."}
		}
		return Tool{
			Name: ToolOpenWorkspace,
			Description: "Make a working copy of a deployment's code on the server that you can change: its repository cloned into a folder of its own under the terminal account's home " +
				"(fetched again, keeping what was changed, when it is already there), on the branch the deployment builds unless you name another. " +
				"A deployment built from a folder on the server works in that folder itself. It answers with the folder, the branch and commit, what is changed in it, " +
				"and how a push will be authenticated. Then read and change its files with read_file, edit_file and write_file, build or test it with run_command (cd <folder> && …), " +
				"and commit and push with push_changes." + agentRoleLine + agentHoldLine,
			Schema: operatorSchema(required, properties),
		}
	}
	readFileTool = Tool{
		Name: ToolReadFile,
		Description: fmt.Sprintf("Read a text file on the server, with its line numbers, %d lines from offset (the first line is 1) unless you ask for up to %d. "+
			"Use an absolute path, such as a file in a workspace's folder. Secret values in it reach you masked.", FileLinesDefault, FileLinesMax) + approved,
		Schema: operatorSchema([]string{"path"}, map[string]any{
			"path":   map[string]any{"type": "string", "maxLength": filePathMax, "description": "The file's absolute path."},
			"offset": map[string]any{"type": "integer", "minimum": 1, "description": "The first line to read (1 by default)."},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": FileLinesMax, "description": "How many lines to read."},
		}),
	}
	editFileTool = Tool{
		Name: ToolEditFile,
		Description: "Change a text file on the server by replacing old_text with new_text. old_text must be copied exactly from the file as read_file showed it, " +
			"without the line numbers, with enough lines around the change that it occurs once; with replace_all every occurrence is replaced. " +
			"It answers with the change as a diff. Read the file first. A file's earlier version is kept on the Files page." + agentRoleLine + agentHoldLine,
		Schema: operatorSchema([]string{"path", "old_text", "new_text", "reason"}, map[string]any{
			"path":        map[string]any{"type": "string", "maxLength": filePathMax, "description": "The file's absolute path."},
			"old_text":    map[string]any{"type": "string", "description": "The exact text to replace."},
			"new_text":    map[string]any{"type": "string", "description": "What replaces it."},
			"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence rather than one."},
			"reason":      map[string]any{"type": "string", "maxLength": operatorReasonMax, "description": "One short sentence the person sees beside the call."},
		}),
	}
	writeFileTool = Tool{
		Name: ToolWriteFile,
		Description: fmt.Sprintf("Write a whole text file on the server, making it or replacing what is in it (at most %d bytes), and the folders it needs. "+
			"Use it for a new file, or one you rewrite whole; edit_file changes part of one. It answers with the change as a diff. A file's earlier version is kept on the Files page.", FileWriteMax) +
			agentRoleLine + agentHoldLine,
		Schema: operatorSchema([]string{"path", "content", "reason"}, map[string]any{
			"path":    map[string]any{"type": "string", "maxLength": filePathMax, "description": "The file's absolute path."},
			"content": map[string]any{"type": "string", "description": "The file's whole text."},
			"reason":  map[string]any{"type": "string", "maxLength": operatorReasonMax, "description": "One short sentence the person sees beside the call."},
		}),
	}
	pushChangesTool = Tool{
		Name: ToolPushChanges,
		Description: "Commit everything changed in a workspace's folder with message, and push it to the repository: to the branch it is on, or to branch, which is made on the remote if it is new " +
			"(for a pull request, say). It never forces. It answers with the commit, what the push did, and whether the deployment deploys it on its own; " +
			"if it does not, start a deploy (call_operation, or api_request for another deployment) and follow it with wait_for_run." + agentRoleLine + agentHoldLine,
		Schema: operatorSchema([]string{"folder", "message", "reason"}, map[string]any{
			"folder":  map[string]any{"type": "string", "maxLength": filePathMax, "description": "The workspace's folder, as open_workspace gave it."},
			"message": map[string]any{"type": "string", "maxLength": commitMax, "description": "The commit message: what changed and why, in a short first line."},
			"branch":  map[string]any{"type": "string", "maxLength": branchMax, "description": "A branch on the remote to push to instead of the one checked out."},
			"reason":  map[string]any{"type": "string", "maxLength": operatorReasonMax, "description": "One short sentence the person sees beside the call."},
		}),
	}
)

// FileTools are the file tools every scope with a shell is offered.
var FileTools = []Tool{readFileTool, editFileTool, writeFileTool}

// codeTools are a deployment's and the server's: the workspace and the file
// tools, and the push.
func codeTools(named bool) []Tool {
	return append([]Tool{openWorkspaceTool(named)}, append(append([]Tool{}, FileTools...), pushChangesTool)...)
}

// WorkspaceInput is what open_workspace and push_changes take.
type WorkspaceInput struct {
	Deployment int64  `json:"deployment,omitempty"`
	Folder     string `json:"folder,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Message    string `json:"message,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// WorkspaceInput reads a workspace call's checked input back.
func (c ToolCall) WorkspaceInput() WorkspaceInput {
	var in WorkspaceInput
	_ = json.Unmarshal(c.Input, &in)
	return in
}

// FileInput is what the file tools take.
type FileInput struct {
	Path       string `json:"path"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	OldText    string `json:"oldText,omitempty"`
	NewText    string `json:"newText,omitempty"`
	ReplaceAll bool   `json:"replaceAll,omitempty"`
	Content    string `json:"content,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// FileInput reads a file call's checked input back.
func (c ToolCall) FileInput() FileInput {
	var in FileInput
	_ = json.Unmarshal(c.Input, &in)
	return in
}

func readWorkspaceInput(tool string, f *fields) WorkspaceInput {
	var in WorkspaceInput
	in.Reason = clip(f.text("reason", "why"), operatorReasonMax)
	in.Branch = f.text("branch", "ref", "branch_name", "branchName")
	if len(in.Branch) > branchMax || strings.ContainsAny(in.Branch, " \t\r\n\x00~^:?*[\\") || strings.HasPrefix(in.Branch, "-") {
		f.fail(fmt.Sprintf("branch %q is not a branch's name.", clipLine(in.Branch, 60)))
		in.Branch = ""
	}
	in.Branch = strings.TrimPrefix(in.Branch, "refs/heads/")
	switch tool {
	case ToolOpenWorkspace:
		if id, given := f.whole("deployment", "deployment_id", "deploymentId", "project", "project_id", "id"); given {
			if id <= 0 {
				f.fail("deployment is a deployment's id, a whole number above 0.")
			}
			in.Deployment = id
		}
	case ToolPushChanges:
		in.Folder = absolutePath(f, "folder", "path", "dir", "directory", "workspace", "cwd")
		if in.Folder == "" && f.problem == "" {
			f.fail("folder is required: the workspace's folder, as open_workspace gave it.")
		}
		in.Message = strings.TrimSpace(f.raw("message", "commit_message", "commitMessage", "msg"))
		if len(in.Message) > commitMax || strings.ContainsRune(in.Message, 0) {
			f.fail(fmt.Sprintf("message is at most %d characters of text.", commitMax))
			in.Message = ""
		}
	}
	return in
}

func readFileInput(tool string, f *fields) FileInput {
	var in FileInput
	in.Path = absolutePath(f, "path", "file", "file_path", "filePath", "filename")
	if in.Path == "" && f.problem == "" {
		f.fail("path is required: the file's absolute path.")
	}
	in.Reason = clip(f.text("reason", "why"), operatorReasonMax)
	switch tool {
	case ToolReadFile:
		in.Offset = int(f.bounded("offset", 1, 1<<30, 1, "start", "start_line", "line", "from"))
		in.Limit = int(f.bounded("limit", 1, FileLinesMax, FileLinesDefault, "lines", "count", "max_lines"))
	case ToolEditFile:
		in.OldText = f.raw("old_text", "oldText", "old", "old_string", "oldString", "search", "find")
		in.NewText = f.raw("new_text", "newText", "new", "new_string", "newString", "replace", "replacement")
		in.ReplaceAll = f.flag("replace_all", "replaceAll", "all")
		switch {
		case in.OldText == "":
			f.fail("old_text is required: the exact text to replace, copied from the file.")
		case in.OldText == in.NewText:
			f.fail("old_text and new_text are the same, so nothing would change.")
		case len(in.NewText) > FileWriteMax:
			f.fail(fmt.Sprintf("new_text is %d bytes, and at most %d are written.", len(in.NewText), FileWriteMax))
			in.NewText = ""
		}
	case ToolWriteFile:
		raw, _ := f.take("content", "text", "contents", "data", "body")
		if raw == nil {
			f.fail("content is required: the file's whole text (an empty file is \"\").")
			break
		}
		if json.Unmarshal(raw, &in.Content) != nil {
			f.fail("content has to be text.")
		}
		if len(in.Content) > FileWriteMax {
			f.fail(fmt.Sprintf("content is %d bytes, and at most %d are written.", len(in.Content), FileWriteMax))
			in.Content = ""
		}
	}
	if strings.ContainsRune(in.OldText+in.NewText+in.Content, 0) {
		f.fail("a text file holds no NUL.")
	}
	return in
}

// absolutePath reads a path field, which has to be absolute and clean.
func absolutePath(f *fields, names ...string) string {
	value := f.text(names...)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "~") || !path.IsAbs(value) {
		f.fail(fmt.Sprintf("%s %q is not an absolute path: write it from / (open_workspace gives a workspace's folder).", names[0], clipLine(value, 120)))
		return ""
	}
	if len(value) > filePathMax || strings.ContainsAny(value, "\x00\r\n") {
		f.fail(fmt.Sprintf("%s is not a path.", names[0]))
		return ""
	}
	return path.Clean(value)
}

// raw reads a text field as it was sent, spaces and all: a file's text and the
// text it replaces are matched exactly.
func (f *fields) raw(names ...string) string {
	value, name := f.take(names...)
	if value == nil {
		return ""
	}
	var s string
	if json.Unmarshal(value, &s) != nil {
		f.fail(name + " has to be text.")
		return ""
	}
	return s
}

// flag reads a yes-or-no field: a boolean, or its text.
func (f *fields) flag(names ...string) bool {
	value, name := f.take(names...)
	if value == nil {
		return false
	}
	var b bool
	if json.Unmarshal(value, &b) == nil {
		return b
	}
	var s string
	if json.Unmarshal(value, &s) == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "yes", "1":
			return true
		case "false", "no", "0", "":
			return false
		}
	}
	f.note(name + " has to be true or false and was read as false")
	return false
}

// workspaceTakes says what the code tools take, for a call that got one wrong.
var workspaceTakes = map[string]string{
	ToolOpenWorkspace: "It takes deployment (a deployment's id), branch (to check out another) and reason.",
	ToolReadFile:      fmt.Sprintf("It takes path (absolute, such as /root/jd-workspaces/app/package.json), offset (the first line, from 1) and limit (1–%d lines).", FileLinesMax),
	ToolEditFile:      "It takes path (absolute), old_text (copied exactly from the file), new_text, replace_all and reason.",
	ToolWriteFile:     "It takes path (absolute), content (the whole file) and reason.",
	ToolPushChanges:   "It takes folder (the workspace's folder), message (the commit message), branch (another branch to push to) and reason.",
}
