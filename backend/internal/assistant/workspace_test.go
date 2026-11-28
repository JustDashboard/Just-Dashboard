package assistant

import (
	"encoding/json"
	"strings"
	"testing"
)

// A code tool's call is read the way a model writes it, and one that cannot run
// says what it takes.
func TestCodeToolCallsAreReadAsModelsWriteThem(t *testing.T) {
	check := CheckCalls(AgentTools)
	call := func(name string, input map[string]any) ToolCall {
		t.Helper()
		raw, _ := json.Marshal(input)
		calls, err := check([]Block{{Type: BlockToolUse, ToolID: "t1", ToolName: name, Input: raw}})
		if err != nil || len(calls) != 1 {
			t.Fatalf("%s: %v %v", name, calls, err)
		}
		return calls[0]
	}

	write := call(ToolWriteFile, map[string]any{"file_path": "/srv/app/src/x.ts", "content": "  indented\n\n", "reason": "x"})
	if write.Problem != "" || write.FileInput().Content != "  indented\n\n" || write.FileInput().Path != "/srv/app/src/x.ts" {
		t.Fatalf("write_file = %+v", write)
	}
	edit := call(ToolEditFile, map[string]any{"path": "/srv/app/a.go", "old_string": "\tx := 1\n", "new_string": "\tx := 2\n", "replace_all": "true"})
	if in := edit.FileInput(); edit.Problem != "" || in.OldText != "\tx := 1\n" || in.NewText != "\tx := 2\n" || !in.ReplaceAll {
		t.Fatalf("edit_file = %+v", edit)
	}
	if same := call(ToolEditFile, map[string]any{"path": "/a", "old_text": "x", "new_text": "x"}); !strings.Contains(same.Problem, "the same") {
		t.Fatalf("an edit that changes nothing = %q", same.Problem)
	}
	if relative := call(ToolReadFile, map[string]any{"path": "src/x.ts"}); !strings.Contains(relative.Problem, "not an absolute path") {
		t.Fatalf("a relative path = %q", relative.Problem)
	}
	read := call(ToolReadFile, map[string]any{"path": "/srv/app/../app/x.ts", "offset": 10, "limit": 99999})
	if in := read.FileInput(); read.Problem != "" || in.Path != "/srv/app/x.ts" || in.Offset != 10 || in.Limit != FileLinesMax {
		t.Fatalf("read_file = %+v", read)
	}
	if push := call(ToolPushChanges, map[string]any{"message": "Fix it"}); !strings.Contains(push.Problem, "folder is required") {
		t.Fatalf("a push without its folder = %q", push.Problem)
	}
	push := call(ToolPushChanges, map[string]any{"path": "/root/jd-workspaces/app", "commit_message": "Fix the build\n\nIt read the wrong port.", "branch": "refs/heads/fix-port"})
	if in := push.WorkspaceInput(); push.Problem != "" || in.Folder != "/root/jd-workspaces/app" || in.Branch != "fix-port" || !strings.HasPrefix(in.Message, "Fix the build\n\n") {
		t.Fatalf("push_changes = %+v", push)
	}
	if bad := call(ToolPushChanges, map[string]any{"folder": "/a", "message": "x", "branch": "--force"}); !strings.Contains(bad.Problem, "not a branch's name") {
		t.Fatalf("a flag as a branch = %q", bad.Problem)
	}
	if open := call(ToolOpenWorkspace, map[string]any{"deployment": "#4", "reason": "x"}); open.Problem != "" || open.WorkspaceInput().Deployment != 4 {
		t.Fatalf("open_workspace = %+v", open)
	}
}

// Every scope with a shell has the file tools; the code tools are a
// deployment's and the server's.
func TestTheCodeToolsAreOfferedWhereThereIsCode(t *testing.T) {
	has := func(tools []Tool, name string) bool {
		for _, tool := range tools {
			if tool.Name == name {
				return true
			}
		}
		return false
	}
	for _, name := range []string{ToolOpenWorkspace, ToolReadFile, ToolEditFile, ToolWriteFile, ToolPushChanges} {
		if !has(AgentTools, name) || !has(ServerTools, name) {
			t.Fatalf("%s is not offered to a deployment's and the server's assistant", name)
		}
	}
	if !has(DatabaseTools, ToolEditFile) || has(DatabaseTools, ToolPushChanges) {
		t.Fatal("a database's assistant has the file tools and not the workspace")
	}
	if prompt := AgentSystemPrompt(OperatorContext{}, nil, APIOverview{}); !strings.Contains(prompt, "open_workspace") || !strings.Contains(prompt, "wait_for_run") {
		t.Fatalf("a deployment's prompt does not say how to change its code:\n%s", prompt)
	}
	if prompt := ServerSystemPrompt(ServerContext{}, APIOverview{}); !strings.Contains(prompt, "push_changes") || strings.Contains(prompt, "with wait_for_run") {
		t.Fatal("the server's prompt tells it to follow a run with a tool it does not have")
	}
}
