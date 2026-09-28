package gitx

import (
	"context"
	"strconv"
	"strings"
)

// GraphCommit is one node in the branch graph: a commit plus the column its dot
// sits in once the lanes have been laid out. Parents carry the SHAs it connects
// down to, so the client draws an edge to each without a second git call.
//
// ParentLanes is the lane each of those edges travels down, index for index
// with Parents. It is not the parent's own column: a branch that rejoins its
// fork point keeps to its lane until the row above the parent and only then
// bends across, and an edge that bent at the top instead would run straight
// through every other commit on the lane it bent into.
type GraphCommit struct {
	Commit
	Col         int   `json:"col"`
	ParentLanes []int `json:"parentLanes,omitempty"`
}

// Graph is the whole branch topology the client renders: commits in topological
// order, newest first, each assigned a lane, and the total lane count so the
// canvas can be sized before the first row is drawn.
type Graph struct {
	Commits []GraphCommit `json:"commits"`
	Lanes   int           `json:"lanes"`
	HasMore bool          `json:"hasMore"`
	Skip    int           `json:"skip"`
	// Total is how many commits the query reaches, counted on the first page
	// only, so the reader knows how much history the graph stopped short of.
	Total int `json:"total,omitempty"`
}

// graphDepth is how far down the history the graph will page. Each page is
// laid out from the newest commit, so reaching a deep one costs the layout of
// everything above it — and past a few thousand commits the reader is looking
// for something, which search and the branch filter find faster than a scroll.
const graphDepth = 5000

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

// GraphPage returns one page of the graph. The lanes are laid out from the top
// of the history on every call and the page sliced out afterwards, so a lane
// keeps its column across a page boundary: laying out each page on its own
// started every page with no lanes open, and a branch that was lane 2 at the
// foot of one page came back as lane 0 at the head of the next.
func (s *Service) GraphPage(ctx context.Context, path string, q GraphQuery) (*Graph, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	skip := max(0, q.Skip)
	if skip >= graphDepth {
		return &Graph{Commits: []GraphCommit{}, Skip: skip}, nil
	}
	limit = min(limit, graphDepth-skip)
	filter := []string{}
	if q.Search != "" {
		filter = append(filter, "--fixed-strings", "--regexp-ignore-case", "--grep="+q.Search)
	}
	if q.Ref != "" {
		if err := ValidateRef(q.Ref); err != nil {
			return nil, err
		}
		filter = append(filter, q.Ref)
	} else {
		filter = append(filter, "--branches", "--remotes", "--tags")
	}
	args := append([]string{"log", "--topo-order", "--max-count=" + strconv.Itoa(skip+limit+1),
		"--pretty=format:" + commitFields}, filter...)
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
	more := len(commits) > skip+limit
	if more {
		commits = commits[:skip+limit]
	}
	var graph *Graph
	if q.Search != "" {
		graph = listGraph(commits)
	} else {
		graph = layoutGraph(commits)
	}
	graph.Commits = graph.Commits[min(skip, len(graph.Commits)):]
	graph.HasMore, graph.Skip = more && skip+limit < graphDepth, skip
	if skip == 0 {
		count, err := s.run(ctx, path, append(append([]string{"rev-list", "--count"}, filter...), "--")...)
		if err != nil {
			return nil, err
		}
		graph.Total, _ = strconv.Atoi(strings.TrimSpace(count))
	}
	return graph, nil
}

// listGraph is a search result drawn as a graph: one lane, no edges. A --grep
// match's parents are mostly commits that did not match, so laying the matches
// out as a topology opened a lane for every gap between two of them and a
// search of a busy repository came back dozens of lanes wide.
func listGraph(commits []Commit) *Graph {
	nodes := make([]GraphCommit, 0, len(commits))
	for _, c := range commits {
		nodes = append(nodes, GraphCommit{Commit: c})
	}
	return &Graph{Commits: nodes, Lanes: min(1, len(nodes))}
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

		var via []int
		if len(c.Parents) == 0 {
			lanes[col] = ""
		} else {
			lanes[col] = c.Parents[0]
			via = append(via, col)
			for _, p := range c.Parents[1:] {
				via = append(via, claim(p))
			}
		}

		// Trim trailing free lanes so a merge that has since rejoined does not
		// leave the canvas permanently wide. A root commit has just freed its own
		// lane, so its column counts even when the trim took it.
		for len(lanes) > 0 && lanes[len(lanes)-1] == "" {
			lanes = lanes[:len(lanes)-1]
		}
		maxLanes = max(maxLanes, len(lanes), col+1)

		nodes = append(nodes, GraphCommit{Commit: c, Col: col, ParentLanes: via})
	}

	if maxLanes == 0 && len(nodes) > 0 {
		maxLanes = 1
	}
	return &Graph{Commits: nodes, Lanes: maxLanes}
}
