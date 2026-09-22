package gitx

import (
	"context"
	"strconv"
	"strings"
)

// GraphCommit is one node in the branch graph: a commit plus the column its dot
// sits in once the lanes have been laid out. Parents carry the SHAs it connects
// down to, so the client draws an edge to each without a second git call.
type GraphCommit struct {
	Commit
	Col int `json:"col"`
}

// Graph is the whole branch topology the client renders: commits in topological
// order, newest first, each assigned a lane, and the total lane count so the
// canvas can be sized before the first row is drawn.
type Graph struct {
	Commits []GraphCommit `json:"commits"`
	Lanes   int           `json:"lanes"`
	HasMore bool          `json:"hasMore"`
	Skip    int           `json:"skip"`
}

type GraphQuery struct {
	Limit  int
	Skip   int
	Search string
	Ref    string
}

// Graph reads every local and remote branch tip plus tags and lays their shared
// history out as lanes — the "which branch came off which" view a hosted forge
// draws and a working copy otherwise hides.
//
// --topo-order is load-bearing: the default order is by date, which routes a
// branch that sat idle for a week straight through the middle of everything
// committed since. Topological order keeps a line of development contiguous, so
// a lane is a branch rather than a zigzag. --branches --remotes --tags rather
// than --all so refs/stash and note refs stay out of it.
func (s *Service) Graph(ctx context.Context, path string, limit int) (*Graph, error) {
	return s.GraphPage(ctx, path, GraphQuery{Limit: limit})
}

func (s *Service) GraphPage(ctx context.Context, path string, q GraphQuery) (*Graph, error) {
	limit := q.Limit
	if limit <= 0 || limit > 400 {
		limit = 200
	}
	args := []string{"log", "--topo-order", "--max-count=" + strconv.Itoa(limit+1),
		"--skip=" + strconv.Itoa(max(0, q.Skip)), "--pretty=format:" + commitFields}
	if q.Search != "" {
		args = append(args, "--fixed-strings", "--regexp-ignore-case", "--grep="+q.Search)
	}
	if q.Ref != "" {
		if err := ValidateRef(q.Ref); err != nil {
			return nil, err
		}
		args = append(args, q.Ref)
	} else {
		args = append(args, "--branches", "--remotes", "--tags")
	}
	out, err := s.run(ctx, path, append(args, "--")...)
	if err != nil {
		return nil, err
	}
	commits := []Commit{}
	for _, line := range strings.Split(out, "\n") {
		if c, ok := parseCommitLine(strings.TrimRight(line, "\r")); ok {
			commits = append(commits, c)
		}
	}
	more := len(commits) > limit
	if more {
		commits = commits[:limit]
	}
	graph := layoutGraph(commits)
	graph.HasMore, graph.Skip = more, max(0, q.Skip)
	return graph, nil
}

// layoutGraph assigns each commit a lane. It walks the commits newest-first, so
// a commit is always seen before its parents. A lane holds the SHA it is next
// expecting; the first commit to claim a SHA takes that lane, its first parent
// inherits the lane, extra parents (a merge) open lanes of their own, and any
// other lane that was also waiting for this commit is freed — that is a branch
// rejoining, and its column should not go on being drawn.
func layoutGraph(commits []Commit) *Graph {
	lanes := []string{} // lanes[i] == the SHA lane i is waiting to place, "" if free
	maxLanes := 0

	claim := func(sha string) int {
		for i, s := range lanes {
			if s == sha {
				return i
			}
		}
		for i, s := range lanes {
			if s == "" {
				lanes[i] = sha
				return i
			}
		}
		lanes = append(lanes, sha)
		return len(lanes) - 1
	}

	nodes := make([]GraphCommit, 0, len(commits))
	for _, c := range commits {
		col := claim(c.SHA)

		// A commit with more than one child: the other lanes that were waiting
		// for it are branches merging back and stop here.
		for i, s := range lanes {
			if i != col && s == c.SHA {
				lanes[i] = ""
			}
		}

		if len(c.Parents) == 0 {
			lanes[col] = ""
		} else {
			lanes[col] = c.Parents[0]
			for _, p := range c.Parents[1:] {
				claim(p)
			}
		}

		// Trim trailing free lanes so a merge that has since rejoined does not
		// leave the canvas permanently wide.
		for len(lanes) > 0 && lanes[len(lanes)-1] == "" {
			lanes = lanes[:len(lanes)-1]
		}
		if len(lanes) > maxLanes {
			maxLanes = len(lanes)
		}

		nodes = append(nodes, GraphCommit{Commit: c, Col: col})
	}

	if maxLanes == 0 && len(nodes) > 0 {
		maxLanes = 1
	}
	return &Graph{Commits: nodes, Lanes: maxLanes}
}
