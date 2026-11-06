package api

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// A database connection's assistant, and what every conversation may be handed
// besides a question: what the SQL editor attaches, and the page a chat about
// the whole server was opened from.
//
// It is the same assistant as a deployment's and the server's (agentTurn), told
// about one connection when a chat begins: what the dashboard knows of it and,
// for a SQL engine that answers within agentStructureWait, the structure of its
// schema in focus as the SQL editor's assistant was told it — names, types, keys
// and references, never a row. It reads and changes the rest through the
// connection's routes, as the person.
//
// A connection the operator protected is out of every conversation's reach
// (agentProtected): the guard in front of the database routes refuses its
// assistant, and any other conversation's call to one of its routes is answered
// with why, so nothing in it is sent to a model while it is protected.

// agentStructureWait bounds how long a chat's beginning waits on the database to
// describe itself; one that does not answer by then is read by the model later.
const agentStructureWait = 8 * time.Second

// agentDatabasePages are the pages of a connection open_page offers, by the key
// it names, under /databases/{id}.
var agentDatabasePages = map[string]agentPage{
	"home":        {Label: "Home", Path: ""},
	"data":        {Label: "Data", Path: "/data"},
	"query":       {Label: "Query", Path: "/query"},
	"search":      {Label: "Search", Path: "/search"},
	"schema":      {Label: "Schema", Path: "/schema"},
	"diagram":     {Label: "Diagram", Path: "/diagram"},
	"performance": {Label: "Performance", Path: "/performance"},
	"logs":        {Label: "Logs", Path: "/logs"},
	"alerts":      {Label: "Alerts", Path: "/alerts"},
	"backups":     {Label: "Backups", Path: "/backups"},
	"access":      {Label: "Access", Path: "/access"},
	"import":      {Label: "Import", Path: "/import"},
	"settings":    {Label: "Settings", Path: "/settings"},
}

// databaseContext is what a connection's conversation begins knowing.
func (s *Server) databaseContext(ctx context.Context, conn *dbConnection) assistant.DatabaseContext {
	out := assistant.DatabaseContext{
		ID: conn.ID, Name: conn.Name, Engine: string(conn.Driver), Database: conn.Database,
		Environment: conn.Environment, Notes: conn.Notes,
	}
	if conn.Host != "" {
		out.Host = conn.Host
		if conn.Port != "" {
			out.Host += ":" + conn.Port
		}
	}
	if s.modules.sys != nil {
		if info, err := s.modules.sys.Host(ctx); err == nil && info != nil {
			out.Server = info.Hostname
		}
	}
	if conn.Broken {
		out.Unread = "the dashboard cannot open it: " + conn.BrokenReason
		return out
	}
	if !conn.Driver.IsSQL() {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, agentStructureWait)
	defer cancel()
	fresh, dsn, err := s.dbConnRow(ctx, conn.ID)
	if err != nil {
		out.Unread = "it could not be read"
		return out
	}
	db, err := s.modules.dbs.Pool(ctx, conn.ID, fresh.Driver, dsn)
	if err != nil {
		out.Unread = "it did not answer"
		return out
	}
	identity := s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifySQL(ctx, db, conn.Driver) })
	out.Engine = dbx.FlavorLabel(identity.Flavor)
	out.Version = identity.Number
	snapshot, err := assistant.ReadSchema(ctx, db, conn.Driver, "", "")
	if err != nil {
		out.Unread = "it did not answer in time"
		if ctx.Err() == nil {
			out.Unread = "its structure could not be read"
		}
		return out
	}
	text, _ := assistant.SchemaText(snapshot, "")
	out.Structure = text
	return out
}

// agentProtected says why a call to a route may not reach the connection it
// names, empty when it may: a protected connection keeps everything in it from
// the assistant. Listing the connections, and reading the one connection's own
// record, still answer, so the assistant can say it is protected.
func (s *Server) agentProtected(ctx context.Context, route *apiRoute, path string) string {
	if !strings.HasPrefix(route.Pattern, "/databases/{id}") {
		return ""
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) < 2 {
		return ""
	}
	id, err := strconv.ParseInt(segments[1], 10, 64)
	if err != nil || id <= 0 {
		return ""
	}
	if route.Method == "GET" && len(segments) == 2 {
		return ""
	}
	var readOnly bool
	err = s.Store.DB.QueryRowContext(ctx, `SELECT read_only FROM db_connections WHERE id = ?`, id).Scan(&readOnly)
	switch {
	case err == sql.ErrNoRows:
		// The route says there is no such connection.
		return ""
	case err != nil:
		// Not knowing is not leave.
		return "whether this connection is protected could not be read, so nothing in it was read"
	case readOnly:
		return "this connection is protected: nothing in it is sent to the assistant until protection is turned off in its settings, which is the person's to do"
	}
	return ""
}

// checkAgentAttached reads what was handed over with a question.
func checkAgentAttached(attached []agentAttachment) error {
	if len(attached) > maxAgentAttached {
		return httpx.BadRequest("a question carries at most %d attachments", maxAgentAttached)
	}
	for _, a := range attached {
		label := strings.TrimSpace(a.Label)
		switch {
		case label == "" || utf8.RuneCountInString(label) > maxAgentAttachedLabel || !oneLine(label):
			return httpx.BadRequest("an attachment's label is one line of at most %d characters", maxAgentAttachedLabel)
		case utf8.RuneCountInString(a.Text) > maxAgentAttachedText || !utf8.ValidString(a.Text) || strings.ContainsRune(a.Text, 0):
			return httpx.BadRequest("an attachment is at most %d characters of text", maxAgentAttachedText)
		}
	}
	return nil
}

// checkAgentAbout reads the page a chat about the server was opened from.
func checkAgentAbout(about assistant.ChatAbout) (*assistant.ChatAbout, error) {
	path, title := strings.TrimSpace(about.Path), strings.Join(strings.Fields(about.Title), " ")
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || len(path) > maxAboutPath || !oneLine(path) {
		return nil, httpx.BadRequest("about.path is a page's address in the dashboard, at most %d characters", maxAboutPath)
	}
	if utf8.RuneCountInString(title) > maxAboutTitle || !utf8.ValidString(title) {
		return nil, httpx.BadRequest("about.title is at most %d characters", maxAboutTitle)
	}
	if title == "" {
		title = path
	}
	return &assistant.ChatAbout{Path: path, Title: title}, nil
}

// oneLine is text with no line break or other control character in it.
func oneLine(s string) bool {
	return utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}

// agentQuestion is the question as the model reads it: what was asked, then
// each thing handed over with it under its label.
func agentQuestion(question string, attached []agentAttachment) string {
	if len(attached) == 0 {
		return question
	}
	var b strings.Builder
	b.WriteString(question)
	for _, a := range attached {
		fence := "```"
		for strings.Contains(a.Text, fence) {
			fence += "`"
		}
		b.WriteString("\n\n" + strings.TrimSpace(a.Label) + ", attached by the person:\n" + fence + "\n" + strings.TrimRight(a.Text, "\n") + "\n" + fence)
	}
	return b.String()
}
