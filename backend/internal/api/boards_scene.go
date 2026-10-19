package api

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"
)

// Excalidraw's own DELETED_ELEMENT_TIMEOUT: a deleted element is kept this
// long so a peer that was offline still learns of the deletion, then dropped.
const boardTombstoneTTL = 24 * time.Hour

// elementMeta is the part of an element the server reads. The rest of the
// element stays raw JSON, so a field this server has never heard of survives
// a save.
type elementMeta struct {
	ID           string  `json:"id"`
	Version      int64   `json:"version"`
	VersionNonce int64   `json:"versionNonce"`
	Index        *string `json:"index"`
	IsDeleted    bool    `json:"isDeleted"`
	Updated      float64 `json:"updated"`
	FileID       string  `json:"fileId"`
}

type sceneElement struct {
	raw  json.RawMessage
	meta elementMeta
	// ok is false for an element that is not an object the server can read.
	// It is kept exactly as it came and never pruned, merged or reported.
	ok bool
}

func (e sceneElement) index() string {
	if e.meta.Index == nil {
		return ""
	}
	return *e.meta.Index
}

func decodeSceneElements(raw json.RawMessage) ([]sceneElement, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := make([]sceneElement, len(items))
	for i, item := range items {
		out[i].raw = item
		if t := bytes.TrimSpace(item); len(t) > 0 && t[0] == '{' {
			out[i].ok = json.Unmarshal(item, &out[i].meta) == nil
		}
	}
	return out, nil
}

// pruneTombstones drops deleted elements whose last change is older than the
// timeout. An element that does not say when it changed has no age to prune by.
func pruneTombstones(elements []sceneElement, now time.Time) []sceneElement {
	cutoff := float64(now.Add(-boardTombstoneTTL).UnixMilli())
	kept := elements[:0:0]
	for _, e := range elements {
		if e.ok && e.meta.IsDeleted && e.meta.Updated > 0 && e.meta.Updated < cutoff {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// mergeElements reconciles the stored elements with an incoming set by id,
// the way Excalidraw's reconcileElements does: the stored copy stays when it
// is newer, or equally new with the lower nonce, and the incoming one wins
// otherwise. Elements on one side only are kept. Equal version and nonce
// is the same element, so the incoming copy is taken and not reported back.
func mergeElements(stored, incoming []sceneElement) []sceneElement {
	merged := make([]sceneElement, len(stored), len(stored)+len(incoming))
	copy(merged, stored)
	at := make(map[string]int, len(stored))
	for i, e := range merged {
		if e.ok && e.meta.ID != "" {
			at[e.meta.ID] = i
		}
	}
	for _, in := range incoming {
		i, found := at[in.meta.ID]
		if !found {
			at[in.meta.ID] = len(merged)
			merged = append(merged, in)
			continue
		}
		cur := merged[i].meta
		keep := cur.Version > in.meta.Version ||
			(cur.Version == in.meta.Version && cur.VersionNonce < in.meta.VersionNonce)
		if !keep {
			merged[i] = in
		}
	}
	return merged
}

// orderByIndex sorts by Excalidraw's fractional index, which compares as
// plain bytes. An element without one goes after those with one, in the
// order it already had.
func orderByIndex(elements []sceneElement) {
	sort.SliceStable(elements, func(i, j int) bool {
		a, b := elements[i].index(), elements[j].index()
		return a != "" && (b == "" || a < b)
	})
}

// differingFrom is the elements the sender does not already hold as they
// are: the stored copy won, or the element was only here.
func differingFrom(merged, sent []sceneElement) []json.RawMessage {
	known := make(map[string]elementMeta, len(sent))
	for _, e := range sent {
		known[e.meta.ID] = e.meta
	}
	out := []json.RawMessage{}
	for _, e := range merged {
		if !e.ok || e.meta.ID == "" {
			continue
		}
		had, ok := known[e.meta.ID]
		if !ok || had.Version != e.meta.Version || had.VersionNonce != e.meta.VersionNonce {
			out = append(out, e.raw)
		}
	}
	return out
}

func elementsJSON(elements []sceneElement) (json.RawMessage, error) {
	raws := make([]json.RawMessage, len(elements))
	for i, e := range elements {
		raws[i] = e.raw
	}
	return json.Marshal(raws)
}

// fileRefs is every file id an element names, deleted elements included: an
// undo can bring a deleted image back.
func fileRefs(elements []sceneElement) map[string]bool {
	refs := map[string]bool{}
	for _, e := range elements {
		if e.ok && e.meta.FileID != "" {
			refs[e.meta.FileID] = true
		}
	}
	return refs
}
