package ghx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type boundedOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *boundedOutput) Len() int       { return b.buffer.Len() }
func (b *boundedOutput) String() string { return b.buffer.String() }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	room := (8 << 20) - b.Len()
	if len(p) > room {
		p = p[:room]
		b.overflow = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

var fullSHA = regexp.MustCompile(`^[a-fA-F0-9]{40,64}$`)

// The repository's authenticated CLI context chooses the host; a request can
// choose a page or request number, never an arbitrary API URL.
func (s *Service) repoAPI(ctx context.Context, dir string) (host, endpoint string, err error) {
	r, err := s.RepoInfo(ctx, dir)
	if err != nil {
		return "", "", err
	}
	u, err := url.Parse(r.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(r.NameWithOwner, "?%#\\") || len(strings.Split(r.NameWithOwner, "/")) != 2 {
		return "", "", fmt.Errorf("GitHub returned an invalid repository address")
	}
	for _, part := range strings.Split(r.NameWithOwner, "/") {
		if part == "" || part == "." || part == ".." {
			return "", "", fmt.Errorf("invalid repository name")
		}
	}
	return u.Host, "repos/" + r.NameWithOwner, nil
}

func (s *Service) apiJSON(ctx context.Context, dir, host, endpoint string, body any, result any) error {
	args := []string{"api", "--hostname", host, endpoint}
	input := ""
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = string(data)
		args = append(args, "--method", "POST", "--input", "-")
	}
	out, err := s.run(ctx, dir, input, args...)
	if err != nil {
		return ghErr("read or update GitHub", out)
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal([]byte(out), result)
}

type PullFile struct {
	Path      string `json:"filename"`
	Previous  string `json:"previous_filename,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"`
}

type PullFiles struct {
	HeadSHA string     `json:"headSha"`
	Files   []PullFile `json:"files"`
	HasMore bool       `json:"hasMore"`
	Limited bool       `json:"limited"`
}

func (s *Service) PullFiles(ctx context.Context, dir string, number, page int, head string) (*PullFiles, error) {
	if number < 1 || page < 1 || page > 30 || !fullSHA.MatchString(head) {
		return nil, fmt.Errorf("a request, page and reviewed head SHA are required")
	}
	p, err := s.ViewPull(ctx, dir, number)
	if err != nil {
		return nil, err
	}
	if p.HeadSHA != head {
		return nil, fmt.Errorf("the pull request changed; reload it before reviewing")
	}
	host, endpoint, err := s.repoAPI(ctx, dir)
	if err != nil {
		return nil, err
	}
	rows := []PullFile{}
	err = s.apiJSON(ctx, dir, host, fmt.Sprintf("%s/pulls/%d/files?per_page=100&page=%d", endpoint, number, page), nil, &rows)
	if err != nil {
		return nil, err
	}
	latest, err := s.ViewPull(ctx, dir, number)
	if err != nil {
		return nil, err
	}
	if latest.HeadSHA != head || latest.BaseSHA != p.BaseSHA {
		return nil, fmt.Errorf("the pull request changed while loading; reload it")
	}
	return &PullFiles{HeadSHA: head, Files: rows, HasMore: page < 30 && page*100 < p.Files, Limited: p.Files > 3000}, nil
}

type ReviewRequest struct {
	Event   string `json:"event"`
	Body    string `json:"body"`
	HeadSHA string `json:"headSha"`
}

func (s *Service) ReviewPull(ctx context.Context, dir string, number int, req ReviewRequest) error {
	if number < 1 || !fullSHA.MatchString(req.HeadSHA) {
		return fmt.Errorf("a request and reviewed head SHA are required")
	}
	switch req.Event {
	case "COMMENT", "APPROVE", "REQUEST_CHANGES":
	default:
		return fmt.Errorf("choose comment, approve or request changes")
	}
	if len(req.Body) > 60000 || (req.Event != "APPROVE" && strings.TrimSpace(req.Body) == "") {
		return fmt.Errorf("a comment of at most 60,000 bytes is required")
	}
	p, err := s.ViewPull(ctx, dir, number)
	if err != nil {
		return err
	}
	if p.HeadSHA != req.HeadSHA {
		return fmt.Errorf("the pull request changed; reload and review the new commits")
	}
	if p.State != "open" {
		return fmt.Errorf("the pull request is no longer open")
	}
	host, endpoint, err := s.repoAPI(ctx, dir)
	if err != nil {
		return err
	}
	return s.apiJSON(ctx, dir, host, fmt.Sprintf("%s/pulls/%d/reviews", endpoint, number), map[string]string{"commit_id": req.HeadSHA, "event": req.Event, "body": req.Body}, nil)
}

type ConversationEntry struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Author string `json:"author"`
	Body   string `json:"body"`
	State  string `json:"state,omitempty"`
	At     string `json:"at"`
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
	URL    string `json:"url"`
}
type Conversation struct {
	Entries []ConversationEntry `json:"entries"`
	HasMore bool                `json:"hasMore"`
}

func (s *Service) Conversation(ctx context.Context, dir string, number, page int) (*Conversation, error) {
	if number < 1 || page < 1 || page > 10000 {
		return nil, fmt.Errorf("invalid request or conversation page")
	}
	host, endpoint, err := s.repoAPI(ctx, dir)
	if err != nil {
		return nil, err
	}
	result := &Conversation{Entries: []ConversationEntry{}}
	for _, kind := range []string{"comments", "reviews", "inline"} {
		part := fmt.Sprintf("pulls/%d/reviews", number)
		if kind == "comments" {
			part = fmt.Sprintf("issues/%d/comments", number)
		}
		if kind == "inline" {
			part = fmt.Sprintf("pulls/%d/comments", number)
		}
		var rows []struct {
			ID    int64  `json:"id"`
			Body  string `json:"body"`
			State string `json:"state"`
			User  struct {
				Login string `json:"login"`
			} `json:"user"`
			Created   string `json:"created_at"`
			Submitted string `json:"submitted_at"`
			URL       string `json:"html_url"`
			Path      string `json:"path"`
			Line      int    `json:"line"`
		}
		if err := s.apiJSON(ctx, dir, host, endpoint+"/"+part+"?per_page=100&page="+strconv.Itoa(page), nil, &rows); err != nil {
			return nil, err
		}
		result.HasMore = result.HasMore || len(rows) == 100
		for _, row := range rows {
			at := row.Created
			if at == "" {
				at = row.Submitted
			}
			result.Entries = append(result.Entries, ConversationEntry{ID: row.ID, Kind: kind, Author: row.User.Login, Body: row.Body, State: row.State, At: at, URL: row.URL, Path: row.Path, Line: row.Line})
		}
	}
	sort.SliceStable(result.Entries, func(i, j int) bool { return result.Entries[i].At < result.Entries[j].At })
	return result, nil
}

type WorkflowStep struct {
	Name       string `json:"name"`
	Number     int    `json:"number"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}
type WorkflowJob struct {
	ID         int64          `json:"databaseId"`
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	Conclusion string         `json:"conclusion"`
	Steps      []WorkflowStep `json:"steps"`
	URL        string         `json:"url"`
}
type RunDetail struct {
	Name       string        `json:"name"`
	URL        string        `json:"url"`
	Status     string        `json:"status"`
	Conclusion string        `json:"conclusion"`
	Jobs       []WorkflowJob `json:"jobs"`
}

func (s *Service) ViewRun(ctx context.Context, dir string, id int64) (*RunDetail, error) {
	if id <= 0 {
		return nil, fmt.Errorf("a workflow run id is required")
	}
	out, err := s.run(ctx, dir, "", "run", "view", strconv.FormatInt(id, 10), "--json", "name,url,status,conclusion,jobs")
	if err != nil {
		return nil, ghErr("read workflow jobs", out)
	}
	var run RunDetail
	if err := json.Unmarshal([]byte(out), &run); err != nil {
		return nil, err
	}
	return &run, nil
}
func (s *Service) RunLog(ctx context.Context, dir string, id, jobID int64, step string, failed bool) (string, error) {
	d, err := s.ViewRun(ctx, dir, id)
	if err != nil {
		return "", err
	}
	var job *WorkflowJob
	for i := range d.Jobs {
		if d.Jobs[i].ID == jobID {
			job = &d.Jobs[i]
			break
		}
	}
	if job == nil {
		return "", fmt.Errorf("the job does not belong to this run")
	}
	if step != "" {
		found := false
		for _, st := range job.Steps {
			found = found || st.Name == step
		}
		if !found {
			return "", fmt.Errorf("the step does not belong to this job")
		}
	}
	flag := "--log"
	if failed {
		flag = "--log-failed"
	}
	out, err := s.run(ctx, dir, "", "run", "view", strconv.FormatInt(id, 10), "--job", strconv.FormatInt(jobID, 10), flag)
	if err != nil {
		return "", ghErr("read workflow logs", out)
	}
	if step != "" {
		lines := []string{}
		for _, line := range strings.Split(out, "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) == 3 && parts[1] == step {
				lines = append(lines, parts[2])
			}
		}
		if len(lines) == 0 {
			return "", fmt.Errorf("GitHub did not identify log lines for this step; select the whole job")
		}
		out = strings.Join(lines, "\n")
	}
	return out, nil
}
