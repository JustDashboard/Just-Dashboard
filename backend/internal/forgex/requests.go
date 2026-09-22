package forgex

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Request struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	Head   string `json:"head"`
	Base   string `json:"base"`
	SHA    string `json:"sha"`
	URL    string `json:"url"`
	Author string `json:"author"`
	At     string `json:"at"`
	Draft  bool   `json:"draft"`
}
type Page struct {
	Requests []Request `json:"requests"`
	HasMore  bool      `json:"hasMore"`
}
type gitlabRequest struct {
	Number int    `json:"iid"`
	Title  string `json:"title"`
	Body   string `json:"description"`
	State  string `json:"state"`
	Head   string `json:"source_branch"`
	Base   string `json:"target_branch"`
	SHA    string `json:"sha"`
	URL    string `json:"web_url"`
	At     string `json:"created_at"`
	Draft  bool   `json:"draft"`
	Author struct {
		Login string `json:"username"`
	} `json:"author"`
}
type giteaRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	URL    string `json:"html_url"`
	At     string `json:"created_at"`
	Merged bool   `json:"merged"`
	Draft  bool   `json:"draft"`
	Head   struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (r gitlabRequest) request() Request {
	return Request{Number: r.Number, Title: r.Title, Body: r.Body, State: r.State, Head: r.Head, Base: r.Base, SHA: r.SHA, URL: r.URL, At: r.At, Author: r.Author.Login, Draft: r.Draft}
}
func (r giteaRequest) request() Request {
	state := r.State
	if r.Merged {
		state = "merged"
	}
	return Request{Number: r.Number, Title: r.Title, Body: r.Body, State: state, Head: r.Head.Ref, Base: r.Base.Ref, SHA: r.Head.SHA, URL: r.URL, At: r.At, Author: r.User.Login, Draft: r.Draft}
}

func pagination(page int) error {
	if page < 1 || page > 10000 {
		return fmt.Errorf("invalid page")
	}
	return nil
}
func (s *Service) List(ctx context.Context, path, state string, page int) (*Page, error) {
	if err := pagination(page); err != nil {
		return nil, err
	}
	if state != "open" && state != "closed" && state != "all" {
		return nil, fmt.Errorf("choose open, closed or all")
	}
	c, err := s.connected(ctx, path)
	if err != nil {
		return nil, err
	}
	result := &Page{Requests: []Request{}}
	if c.Kind == "gitlab" {
		if state == "open" {
			state = "opened"
		}
		var rows []gitlabRequest
		err = s.json(ctx, c, "GET", c.requests()+fmt.Sprintf("?scope=all&state=%s&per_page=50&page=%d", state, page), nil, &rows)
		for _, row := range rows {
			result.Requests = append(result.Requests, row.request())
		}
	} else {
		var rows []giteaRequest
		err = s.json(ctx, c, "GET", c.requests()+fmt.Sprintf("?state=%s&limit=50&page=%d", state, page), nil, &rows)
		for _, row := range rows {
			result.Requests = append(result.Requests, row.request())
		}
	}
	result.HasMore = len(result.Requests) == 50
	return result, err
}
func (s *Service) view(ctx context.Context, c *credential, number int) (*Request, error) {
	if number <= 0 {
		return nil, fmt.Errorf("a request number is required")
	}
	endpoint := c.requests() + "/" + strconv.Itoa(number)
	if c.Kind == "gitlab" {
		var row gitlabRequest
		if err := s.json(ctx, c, "GET", endpoint, nil, &row); err != nil {
			return nil, err
		}
		r := row.request()
		return &r, nil
	}
	var row giteaRequest
	if err := s.json(ctx, c, "GET", endpoint, nil, &row); err != nil {
		return nil, err
	}
	r := row.request()
	return &r, nil
}
func (s *Service) View(ctx context.Context, path string, number int) (*Request, error) {
	c, err := s.connected(ctx, path)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, c, number)
}

type File struct {
	Path     string `json:"path"`
	Previous string `json:"previous,omitempty"`
	Diff     string `json:"diff"`
	Omitted  bool   `json:"omitted"`
}
type Files struct {
	Files   []File `json:"files"`
	Body    string `json:"body,omitempty"`
	HasMore bool   `json:"hasMore"`
}

func (s *Service) Files(ctx context.Context, path string, number, page int, head string) (*Files, error) {
	if err := pagination(page); err != nil {
		return nil, err
	}
	c, err := s.connected(ctx, path)
	if err != nil {
		return nil, err
	}
	before, err := s.view(ctx, c, number)
	if err != nil {
		return nil, err
	}
	if head == "" || before.SHA != head {
		return nil, fmt.Errorf("the request changed; reload it")
	}
	result := &Files{Files: []File{}}
	endpoint := c.requests() + "/" + strconv.Itoa(number)
	if c.Kind == "gitlab" {
		var rows []struct {
			New       string `json:"new_path"`
			Old       string `json:"old_path"`
			Diff      string `json:"diff"`
			TooLarge  bool   `json:"too_large"`
			Collapsed bool   `json:"collapsed"`
		}
		if err := s.json(ctx, c, "GET", endpoint+fmt.Sprintf("/diffs?per_page=50&page=%d", page), nil, &rows); err != nil {
			return nil, err
		}
		for _, row := range rows {
			result.Files = append(result.Files, File{Path: row.New, Previous: row.Old, Diff: row.Diff, Omitted: row.TooLarge || row.Collapsed || row.Diff == ""})
		}
		result.HasMore = len(rows) == 50
	} else {
		data, _, err := s.raw(ctx, c, "GET", endpoint+".diff", nil)
		if err != nil {
			return nil, err
		}
		result.Body = string(data)
	}
	after, err := s.view(ctx, c, number)
	if err != nil {
		return nil, err
	}
	if after.SHA != head {
		return nil, fmt.Errorf("the request changed while reading its diff; reload it")
	}
	return result, nil
}

type Comment struct {
	ID     int64  `json:"id"`
	Body   string `json:"body"`
	Author string `json:"author"`
	At     string `json:"at"`
}
type Conversation struct {
	Comments []Comment `json:"comments"`
	HasMore  bool      `json:"hasMore"`
}

func (s *Service) Conversation(ctx context.Context, path string, number, page int) (*Conversation, error) {
	if number <= 0 {
		return nil, fmt.Errorf("invalid request")
	}
	if err := pagination(page); err != nil {
		return nil, err
	}
	c, err := s.connected(ctx, path)
	if err != nil {
		return nil, err
	}
	endpoint := c.repo() + fmt.Sprintf("/issues/%d/comments?limit=50&page=%d", number, page)
	if c.Kind == "gitlab" {
		endpoint = c.requests() + fmt.Sprintf("/%d/notes?per_page=50&page=%d&sort=asc", number, page)
	}
	var rows []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		At   string `json:"created_at"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Author struct {
			Login string `json:"username"`
		} `json:"author"`
	}
	if err := s.json(ctx, c, "GET", endpoint, nil, &rows); err != nil {
		return nil, err
	}
	result := &Conversation{Comments: []Comment{}, HasMore: len(rows) == 50}
	for _, row := range rows {
		author := row.User.Login
		if author == "" {
			author = row.Author.Login
		}
		result.Comments = append(result.Comments, Comment{ID: row.ID, Body: row.Body, Author: author, At: row.At})
	}
	return result, nil
}

type NewRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Draft bool   `json:"draft"`
}

func (s *Service) Create(ctx context.Context, path string, req NewRequest) (*Request, error) {
	if strings.TrimSpace(req.Title) == "" || len(req.Title) > 255 || len(req.Body) > 60000 || req.Head == "" || req.Base == "" || len(req.Head) > 255 || len(req.Base) > 255 {
		return nil, fmt.Errorf("a title, source and target branch are required")
	}
	c, err := s.connected(ctx, path)
	if err != nil {
		return nil, err
	}
	title := req.Title
	if req.Draft && !strings.HasPrefix(strings.ToLower(title), "draft:") {
		title = "Draft: " + title
	}
	if c.Kind == "gitlab" {
		var row gitlabRequest
		err := s.json(ctx, c, "POST", c.requests(), map[string]any{"title": title, "description": req.Body, "source_branch": req.Head, "target_branch": req.Base}, &row)
		r := row.request()
		return &r, err
	}
	var row giteaRequest
	err = s.json(ctx, c, "POST", c.requests(), map[string]any{"title": title, "body": req.Body, "head": req.Head, "base": req.Base}, &row)
	r := row.request()
	return &r, err
}

type Action struct {
	Action string `json:"action"`
	Body   string `json:"body"`
	SHA    string `json:"sha"`
	Method string `json:"method"`
}

var shaPattern = regexp.MustCompile(`^[a-fA-F0-9]{40,64}$`)

func (s *Service) Act(ctx context.Context, path string, number int, req Action) error {
	if !shaPattern.MatchString(req.SHA) || len(req.Body) > 60000 {
		return fmt.Errorf("a reviewed commit SHA and a body below 60,000 bytes are required")
	}
	if req.Action != "comment" && req.Action != "approve" && req.Action != "request_changes" && req.Action != "merge" {
		return fmt.Errorf("unknown request action")
	}
	if (req.Action == "comment" || req.Action == "request_changes") && strings.TrimSpace(req.Body) == "" {
		return fmt.Errorf("a comment is required")
	}
	c, err := s.connected(ctx, path)
	if err != nil {
		return err
	}
	current, err := s.view(ctx, c, number)
	if err != nil {
		return err
	}
	if current.SHA != req.SHA {
		return fmt.Errorf("the request changed; reload it before publishing")
	}
	if current.State != "open" && current.State != "opened" {
		return fmt.Errorf("the request is no longer open")
	}
	endpoint := c.requests() + "/" + strconv.Itoa(number)
	if c.Kind == "gitlab" {
		switch req.Action {
		case "comment":
			return s.json(ctx, c, "POST", endpoint+"/notes", map[string]string{"body": req.Body}, nil)
		case "approve":
			return s.json(ctx, c, "POST", endpoint+"/approve", map[string]string{"sha": req.SHA}, nil)
		case "request_changes":
			return fmt.Errorf("use a comment to request changes on GitLab; approval rules are managed by the project")
		case "merge":
			if req.Method != "merge" && req.Method != "squash" {
				return fmt.Errorf("choose merge or squash")
			}
			return s.json(ctx, c, "PUT", endpoint+"/merge", map[string]any{"sha": req.SHA, "squash": req.Method == "squash"}, nil)
		}
	}
	if req.Action == "comment" {
		return s.json(ctx, c, "POST", c.repo()+fmt.Sprintf("/issues/%d/comments", number), map[string]string{"body": req.Body}, nil)
	}
	if req.Action == "merge" {
		if req.Method != "merge" && req.Method != "squash" && req.Method != "rebase" {
			return fmt.Errorf("choose merge, squash or rebase")
		}
		return s.json(ctx, c, "POST", endpoint+"/merge", map[string]string{"Do": req.Method, "head_commit_id": req.SHA}, nil)
	}
	event := "APPROVED"
	if req.Action == "request_changes" {
		event = "REQUEST_CHANGES"
	}
	return s.json(ctx, c, "POST", endpoint+"/reviews", map[string]string{"event": event, "body": req.Body, "commit_id": req.SHA}, nil)
}
