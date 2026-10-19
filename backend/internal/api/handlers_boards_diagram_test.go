package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

const diagramKey = "sk-ant-diagram-key"

func diagramFixture(t *testing.T) (*client, *Server, *fakeProvider) {
	t.Helper()
	admin, s := newClient(t)
	provider := &fakeProvider{}
	server := httptest.NewServer(provider)
	t.Cleanup(server.Close)
	s.assistant.options.AnthropicURL = server.URL
	return admin, s, provider
}

func configureAssistant(t *testing.T, s *Server) {
	t.Helper()
	sealed, err := s.Sealer.Seal(fmt.Sprintf(`{"provider":"anthropic","model":"claude-opus-5-5","key":%q}`, diagramKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetSetting(t.Context(), assistantSettingKey, sealed); err != nil {
		t.Fatal(err)
	}
}

func TestBoardDiagramAnswersMermaidSource(t *testing.T) {
	admin, s, provider := diagramFixture(t)
	reader := &client{t: t, h: admin.h, cookie: signInAs(t, s, "diagramreader", auth.RoleReadOnly)}
	ask := func(c *client, prompt string) *httptest.ResponseRecorder {
		return c.do(http.MethodPost, "/api/v1/boards/diagram", fmt.Sprintf(`{"prompt":%q}`, prompt), nil)
	}

	rec := ask(admin, "login flow")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "assistant_not_configured") ||
		!strings.Contains(rec.Body.String(), "administrator") || provider.asked() != 0 {
		t.Fatalf("not configured = %d %s", rec.Code, rec.Body.String())
	}

	configureAssistant(t, s)
	provider.queue(claudeAnswer("```mermaid\nflowchart TD\n  A --> B\n```\n"))
	rec = ask(admin, "  login flow  ")
	var answer struct{ Mermaid string }
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil || rec.Code != http.StatusOK || answer.Mermaid != "flowchart TD\n  A --> B" {
		t.Fatalf("answer = %d %q", rec.Code, rec.Body.String())
	}
	sent := provider.raw[len(provider.raw)-1]
	if !strings.Contains(sent, "Mermaid") || !strings.Contains(sent, `"login flow"`) || strings.Contains(sent, "customers") {
		t.Errorf("request to the provider = %s", sent)
	}

	provider.queue(claudeAnswer("sequenceDiagram\n  A->>B: hi"))
	if rec := ask(admin, "a chat"); !strings.Contains(rec.Body.String(), "sequenceDiagram") {
		t.Errorf("plain answer = %d %s", rec.Code, rec.Body.String())
	}
	if rec := ask(reader, "a chat"); rec.Code != http.StatusForbidden {
		t.Errorf("reader = %d %s", rec.Code, rec.Body.String())
	}
	for label, prompt := range map[string]string{"empty": "", "blank": " \n\t ", "long": strings.Repeat("é", maxDiagramPrompt+1)} {
		if rec := ask(admin, prompt); rec.Code != http.StatusBadRequest {
			t.Errorf("%s prompt = %d %s", label, rec.Code, rec.Body.String())
		}
	}
	provider.queue(claudeAnswer(strings.Repeat("é", maxDiagramPrompt)))
	if rec := ask(admin, strings.Repeat("é", maxDiagramPrompt)); rec.Code != http.StatusOK {
		t.Errorf("longest prompt = %d %.200s", rec.Code, rec.Body.String())
	}

	provider.queue(claudeAnswer("```\n```"))
	if rec := ask(admin, "nothing"); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "assistant_failed") {
		t.Errorf("empty answer = %d %s", rec.Code, rec.Body.String())
	}
	provider.queue(claudeAnswer("graph TD\n" + strings.Repeat("A-->B\n", maxDiagramSource)))
	if rec := ask(admin, "huge"); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "assistant_failed") {
		t.Errorf("oversized answer = %d %.200s", rec.Code, rec.Body.String())
	}
	provider.queue(func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key %s"}}`, "xxxx")
	})
	rec = ask(admin, "refused")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "assistant_failed") || strings.Contains(rec.Body.String(), diagramKey) {
		t.Errorf("provider refusal = %d %s", rec.Code, rec.Body.String())
	}

	if ok, failed := boardAuditCount(t, s, "board.diagram", true), boardAuditCount(t, s, "board.diagram", false); ok != 3 || failed != 4 {
		t.Errorf("audited diagrams = %d ok, %d failed", ok, failed)
	}
	var detail string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'board.diagram' AND success = 1 ORDER BY id LIMIT 1`).Scan(&detail); err != nil ||
		!strings.Contains(detail, "anthropic") || strings.Contains(detail, diagramKey) || strings.Contains(detail, "login flow") {
		t.Errorf("audit detail = %q (%v)", detail, err)
	}
}

func TestMermaidSourceStripsFences(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, in, want string }{
		{"plain", "graph TD\nA-->B", "graph TD\nA-->B"},
		{"fenced", "```mermaid\ngraph TD\nA-->B\n```", "graph TD\nA-->B"},
		{"bare fence", "```\ngraph TD\n```\n", "graph TD"},
		{"indented fence", "  ```mermaid\ngraph TD\n  ```  ", "graph TD"},
		{"only a fence", "```\n```", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mermaidSource(assistant.Reply{Blocks: []assistant.Block{{Type: assistant.BlockText, Text: tc.in}}})
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
