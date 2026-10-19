package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Text to diagram asks the provider an administrator set up for the SQL
// editor's assistant (handlers_db_assistant.go) for Mermaid source, which the
// editor turns into shapes. The prompt is the only thing sent: no board, no
// database and no server data goes to the model.

const (
	maxDiagramPrompt = 4000
	maxDiagramSource = 20000
	diagramTurn      = 90 * time.Second
)

const diagramSystem = `You turn a request into one Mermaid diagram.
Choose the diagram type that answers the request best: flowchart, sequence, class, state, ER, gantt, mindmap or another type Mermaid supports.
Reply with the Mermaid source only. Do not wrap it in a code fence and do not add any other text.`

func (s *Server) handleBoardDiagram(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	prompt := strings.TrimSpace(req.Prompt)
	if n := utf8.RuneCountInString(prompt); n < 1 || n > maxDiagramPrompt {
		return httpx.BadRequest("describe the diagram in 1 to %d characters", maxDiagramPrompt)
	}
	httpx.SetAudit(r, "board.diagram", "", nil)
	settings, err := s.assistantSettings(r.Context())
	if err != nil {
		return err
	}
	if settings == nil {
		return httpx.Err(http.StatusConflict, "assistant_not_configured",
			"the assistant is not set up: an administrator chooses its provider in the assistant's settings in the SQL editor, under Databases")
	}
	if !s.assistant.limit.Allow("install") {
		return httpx.Err(http.StatusTooManyRequests, "rate_limited",
			"the assistant has answered as many questions as this install allows in a minute; ask again shortly")
	}
	select {
	case s.assistant.slots <- struct{}{}:
		defer func() { <-s.assistant.slots }()
	default:
		return httpx.Err(http.StatusTooManyRequests, "assistant_busy",
			"the assistant is answering several questions already; ask again when one of them has finished")
	}
	client, err := assistant.New(*settings, s.assistant.options)
	if err != nil {
		return httpx.Internal(err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), diagramTurn)
	defer cancel()
	reply, err := client.Stream(ctx, assistant.Request{
		System:   diagramSystem,
		Messages: []assistant.Message{{Role: "user", Blocks: []assistant.Block{{Type: assistant.BlockText, Text: prompt}}}},
	}, func(assistant.Event) {})
	detail := map[string]any{"provider": settings.Provider, "model": settings.Model, "usage": reply.Usage}
	if err != nil {
		detail["outcome"] = "failed"
		httpx.SetAudit(r, "board.diagram", "", detail)
		return diagramFailure(r.Context(), ctx, err)
	}
	source := mermaidSource(reply)
	if reply.Stop == assistant.StopRefusal || source == "" || len(source) > maxDiagramSource {
		detail["outcome"] = "unusable"
		httpx.SetAudit(r, "board.diagram", "", detail)
		return httpx.Err(http.StatusBadGateway, "assistant_failed",
			"the assistant did not answer with a diagram that fits on a board; describe it differently and try again")
	}
	detail["outcome"] = "answered"
	httpx.SetAudit(r, "board.diagram", "", detail)
	httpx.JSON(w, http.StatusOK, map[string]string{"mermaid": source})
	return nil
}

func diagramFailure(request, turn context.Context, err error) error {
	switch {
	case request.Err() != nil:
		return httpx.Err(http.StatusBadGateway, "assistant_failed", "the request was stopped")
	case errors.Is(turn.Err(), context.DeadlineExceeded):
		return httpx.Err(http.StatusBadGateway, "assistant_failed", "the assistant took longer than 90 seconds and was stopped").Retry()
	}
	apiErr := httpx.Err(http.StatusBadGateway, "assistant_failed", err.Error())
	if pe, ok := assistant.IsProviderError(err); ok {
		apiErr.Raw, apiErr.Retryable = pe.Raw, pe.Retryable
	}
	return apiErr
}

// mermaidSource is the answer's text without the code fence a model adds
// despite being told not to.
func mermaidSource(reply assistant.Reply) string {
	var text strings.Builder
	for _, b := range reply.Blocks {
		if b.Type == assistant.BlockText {
			text.WriteString(b.Text)
		}
	}
	var lines []string
	for _, line := range strings.Split(text.String(), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "```") {
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
