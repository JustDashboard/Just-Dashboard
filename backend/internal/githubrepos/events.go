package githubrepos

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// What the connected account did lately, as GitHub's activity feed lists it
// (/users/{login}/events). GitHub adds private events to that feed only for
// a token with the Events account permission, which the App's manifest does
// not ask for, so it is public events only.
// GitHub's events are a dozen payload shapes; they are folded here into one
// flat Event so the page draws a line per event without knowing GitHub's.

const eventsPerPage = 30

// Event is GitHubEvent. Only the fields its kind has are present.
type Event struct {
	ID string `json:"id"`
	// Kind is push, pull, issue, comment, review, create, delete, star,
	// fork, release, public or member.
	Kind string `json:"kind"`
	// Repository is owner/name.
	Repository string    `json:"repository"`
	CreatedAt  time.Time `json:"createdAt,omitzero"`
	// Action is GitHub's word for what happened: opened, closed, merged,
	// reopened, published, approved…
	Action string `json:"action,omitempty"`
	// Ref is a branch or tag, without refs/heads/.
	Ref string `json:"ref,omitempty"`
	// RefType is branch, tag or repository, for create and delete.
	RefType string `json:"refType,omitempty"`
	Number  int64  `json:"number,omitempty"`
	Title   string `json:"title,omitempty"`
	// Commits is how many commits a push carried, when GitHub's event says:
	// its documented push payload no longer carries a count or the commits.
	Commits int    `json:"commits,omitempty"`
	HTMLURL string `json:"htmlUrl,omitempty"`
}

// EventList is GitHubEventList.
type EventList struct {
	Events []Event `json:"events"`
}

type rawEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Repo struct {
		Name string `json:"name"`
	} `json:"repo"`
	CreatedAt *time.Time      `json:"created_at"`
	Payload   json.RawMessage `json:"payload"`
}

// rawEventThing is a pull request, issue, comment, review, release, fork or
// member inside a payload: the few fields any of them is read for.
type rawEventThing struct {
	Number   int64  `json:"number"`
	Title    string `json:"title"`
	HTMLURL  string `json:"html_url"`
	Merged   bool   `json:"merged"`
	State    string `json:"state"`
	Name     string `json:"name"`
	TagName  string `json:"tag_name"`
	FullName string `json:"full_name"`
}

// rawEventPayload is every payload field any kept event is read for.
type rawEventPayload struct {
	Action      string            `json:"action"`
	Number      int64             `json:"number"`
	Ref         *string           `json:"ref"`
	RefType     string            `json:"ref_type"`
	Before      string            `json:"before"`
	Head        string            `json:"head"`
	Size        *int              `json:"size"`
	Commits     []json.RawMessage `json:"commits"`
	PullRequest *rawEventThing    `json:"pull_request"`
	Issue       *rawEventThing    `json:"issue"`
	Comment     *rawEventThing    `json:"comment"`
	Review      *rawEventThing    `json:"review"`
	Release     *rawEventThing    `json:"release"`
	Forkee      *rawEventThing    `json:"forkee"`
}

func thing(t *rawEventThing) rawEventThing {
	if t == nil {
		return rawEventThing{}
	}
	return *t
}

func refType(value string) string {
	switch value {
	case "branch", "tag", "repository":
		return value
	}
	return ""
}

// eventRepository is repo.name when it is an owner/name, which is the only
// form a link is built from.
func eventRepository(name string) string {
	owner, repo, found := strings.Cut(name, "/")
	if !found || !searchLoginRE.MatchString(owner) || !ValidRepoName(repo) {
		return ""
	}
	return name
}

// event folds one of GitHub's events into the page's, or reports that it is
// a type the page does not list. web is github.com, for the addresses
// GitHub's payload leaves out: a push has none, and the pull request GitHub
// documents inside an event is a minimal one (number, head, base) with no
// title and no page address, though older answers carried both.
func (raw rawEvent) event(web string) (Event, bool) {
	repository := eventRepository(raw.Repo.Name)
	if repository == "" || raw.ID == "" {
		return Event{}, false
	}
	var payload rawEventPayload
	if len(raw.Payload) > 0 {
		// A payload that does not read leaves the event with what the
		// envelope says: who, where and when still hold.
		_ = json.Unmarshal(raw.Payload, &payload)
	}
	out := Event{ID: raw.ID, Repository: repository, CreatedAt: timeOf(raw.CreatedAt)}
	home := strings.TrimRight(web, "/") + "/" + repository
	// link prefers GitHub's own address and falls back to one built from the
	// repository, so an event always leads somewhere when it can.
	link := func(given, built string) string {
		if address := webURL(given); address != "" {
			return address
		}
		return webURL(built)
	}
	numbered := func(kind string, number int64) string {
		if number <= 0 {
			return ""
		}
		return home + "/" + kind + "/" + strconv.FormatInt(number, 10)
	}
	switch raw.Type {
	case "PushEvent":
		out.Kind = "push"
		ref := deref(payload.Ref)
		branch, isBranch := strings.CutPrefix(ref, "refs/heads/")
		if !isBranch {
			branch = strings.TrimPrefix(ref, "refs/tags/")
		}
		out.Ref = branch
		switch {
		case payload.Size != nil:
			out.Commits = max(*payload.Size, 0)
		default:
			out.Commits = len(payload.Commits)
		}
		switch {
		case isBranch && IsFullSHA(payload.Before) && IsFullSHA(payload.Head) && strings.Trim(payload.Before, "0") != "":
			out.HTMLURL = link("", home+"/compare/"+payload.Before+"..."+payload.Head)
		case branch != "":
			out.HTMLURL = link("", home+"/tree/"+EscapeRef(branch))
		}
	case "PullRequestEvent":
		pull := thing(payload.PullRequest)
		out.Kind, out.Action, out.Title = "pull", payload.Action, pull.Title
		out.Number = max(payload.Number, pull.Number)
		if payload.Action == "closed" && pull.Merged {
			out.Action = "merged"
		}
		out.HTMLURL = link(pull.HTMLURL, numbered("pull", out.Number))
	case "IssuesEvent":
		issue := thing(payload.Issue)
		out.Kind, out.Action, out.Number, out.Title = "issue", payload.Action, issue.Number, issue.Title
		out.HTMLURL = link(issue.HTMLURL, numbered("issues", issue.Number))
	case "IssueCommentEvent":
		issue := thing(payload.Issue)
		out.Kind, out.Action, out.Number, out.Title = "comment", payload.Action, issue.Number, issue.Title
		out.HTMLURL = link(thing(payload.Comment).HTMLURL, link(issue.HTMLURL, numbered("issues", issue.Number)))
	case "PullRequestReviewEvent":
		pull, review := thing(payload.PullRequest), thing(payload.Review)
		out.Kind, out.Action, out.Number, out.Title = "review", strings.ToLower(review.State), pull.Number, pull.Title
		out.HTMLURL = link(review.HTMLURL, link(pull.HTMLURL, numbered("pull", pull.Number)))
	case "CreateEvent":
		out.Kind, out.RefType, out.Ref = "create", refType(payload.RefType), deref(payload.Ref)
	case "DeleteEvent":
		out.Kind, out.RefType, out.Ref = "delete", refType(payload.RefType), deref(payload.Ref)
	case "WatchEvent":
		out.Kind = "star"
	case "ForkEvent":
		out.Kind = "fork"
		out.HTMLURL = webURL(thing(payload.Forkee).HTMLURL)
	case "ReleaseEvent":
		release := thing(payload.Release)
		out.Kind, out.Action, out.Ref, out.Title = "release", payload.Action, release.TagName, strings.TrimSpace(release.Name)
		if out.Title == "" {
			out.Title = release.TagName
		}
		out.HTMLURL = webURL(release.HTMLURL)
	case "PublicEvent":
		out.Kind = "public"
	case "MemberEvent":
		out.Kind, out.Action = "member", payload.Action
	default:
		return Event{}, false
	}
	return out, true
}

// readEvents is the account's last thirty events, newest first as GitHub
// lists them, without the types the page does not draw.
func readEvents(ctx context.Context, client *Client, login, web string) (EventList, error) {
	if !searchLoginRE.MatchString(login) {
		return EventList{}, unusableLogin("/users/events")
	}
	path := "/users/" + url.PathEscape(login) + "/events?" + url.Values{"per_page": {strconv.Itoa(eventsPerPage)}}.Encode()
	var raw []rawEvent
	if _, err := client.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return EventList{}, err
	}
	list := EventList{Events: make([]Event, 0, min(len(raw), eventsPerPage))}
	for _, item := range raw {
		if len(list.Events) == eventsPerPage {
			break
		}
		if event, ok := item.event(web); ok {
			list.Events = append(list.Events, event)
		}
	}
	return list, nil
}

// Events is what the connected account did lately, answered from memory for
// a minute unless refresh is set.
func (s *Service) Events(ctx context.Context, userID, id int64, refresh bool) (EventList, error) {
	c, err := s.active(ctx, userID, id)
	if err != nil {
		return EventList{}, err
	}
	if !refresh {
		s.mu.Lock()
		cached, ok := s.events.get(id, s.now())
		s.mu.Unlock()
		if ok {
			return cached, nil
		}
	}
	client := s.client(c)
	login, err := s.currentLogin(ctx, c, client)
	if err != nil {
		return EventList{}, err
	}
	list, err := readEvents(ctx, client, login, s.webURL())
	if err != nil {
		return EventList{}, err
	}
	s.mu.Lock()
	s.events.put(id, list, s.now(), eventsTTL)
	s.mu.Unlock()
	return list, nil
}
