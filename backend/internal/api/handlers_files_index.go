package api

import (
	"context"
	"errors"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

// Find's name index (files/index.go). The index answers for the whole tree and
// a short walk answers for the folder on screen, so a name typed in Find is
// found wherever it is and a file made a moment ago still shows. With
// JD_FILES_INDEX=false, or until the first build is published, Find is the
// walk it always was.

// fileIndexMaxAge is how old the index may be before a search answered from
// it starts a new build; fileIndexEvery is the schedule that rebuilds it
// whether or not anybody searches.
const (
	fileIndexMaxAge = time.Hour
	fileIndexEvery  = 6 * time.Hour
	// fileIndexLiveBudget is the walk of the folder on screen beside an
	// index answer: short, since the index has the rest.
	fileIndexLiveBudget = 300 * time.Millisecond
)

// findResponse is GET /files/find's answer: the result, where it came from,
// and the index's age when the index answered.
type findResponse struct {
	*files.FindResult
	Source string           `json:"source"`
	Index  *files.IndexInfo `json:"index,omitempty"`
}

// findNames answers a Find from the index where there is one, merged with a
// live walk of here (the folder on screen) or of the search's root.
func (s *Server) findNames(ctx context.Context, opts files.FindOptions, here string) (*findResponse, error) {
	index := s.modules.fileIndex
	if index == nil {
		result, err := s.modules.hostFiles.Find(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &findResponse{FindResult: result, Source: "walk"}, nil
	}
	indexed, info, err := index.Find(ctx, opts)
	if errors.Is(err, files.ErrNoIndex) {
		s.refreshFileIndex()
		result, err := s.modules.hostFiles.Find(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &findResponse{FindResult: result, Source: "walk"}, nil
	}
	if err != nil {
		return nil, err
	}
	if time.Duration(info.AgeSeconds)*time.Second > fileIndexMaxAge {
		s.refreshFileIndex()
		info.Building = true
	}
	live := opts
	live.Budget = fileIndexLiveBudget
	if here != "" {
		if resolved, err := s.modules.hostFiles.Resolve(here); err == nil && withinPath(resolved, indexed.Root) {
			live.Root = resolved
		}
	}
	walked, err := s.modules.hostFiles.Find(ctx, live)
	if err != nil {
		walked = nil
	}
	merged := files.MergeFind(files.RebaseFind(walked, indexed.Root, opts.Query), indexed, opts.Limit)
	// The index walked everything it could reach; what it says it could not
	// is the one way its answer is partial.
	merged.Truncated = !info.Complete || indexed.Truncated
	return &findResponse{FindResult: merged, Source: "index", Index: info}, nil
}

func withinPath(path, dir string) bool {
	return dir == "/" || path == dir || len(path) > len(dir) && path[:len(dir)] == dir && path[len(dir)] == '/'
}

func (s *Server) refreshFileIndex() {
	if s.modules.fileIndex == nil {
		return
	}
	s.modules.fileIndex.Refresh(func(err error) {
		s.Log.Warn("the Files name index could not be built; Find walks the disk instead", "err", err)
	})
}

// keepFileIndex builds the index shortly after start when it is missing or
// stale, and again on a schedule.
func (s *Server) keepFileIndex(ctx context.Context) {
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if info := s.modules.fileIndex.Info(); info == nil || info.AgeSeconds > int(fileIndexEvery.Seconds()) {
			s.refreshFileIndex()
		}
		timer.Reset(30 * time.Minute)
	}
}
