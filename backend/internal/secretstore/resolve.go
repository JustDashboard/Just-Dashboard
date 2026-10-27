package secretstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// resolveTimeout bounds everything one run asks of the stores together:
// signing in to each and reading each path.
const resolveTimeout = 60 * time.Second

// Read is what a run read for one reference: the value, and the version the
// store reported, empty where the store keeps none a reader can ask for
// (Doppler). Only the version is ever recorded.
type Read struct {
	Value   string
	Version string
}

// ResolveOptions says who is asking.
type ResolveOptions struct {
	// Preview is a pull request preview's run, which reads only stores that
	// allow previews.
	Preview bool
}

// Resolve reads the values references name, fresh: each store is signed in
// to once, each path read once however many of its keys are referenced, and
// a pinned version read on its own. The first failure ends the resolution
// with the store's own answer, and nothing read before it is returned. The
// outcome for each store asked is recorded on it, without any value.
func (r *Repository) Resolve(ctx context.Context, references []Reference, options ResolveOptions) (map[string]Read, error) {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	byStore := map[string][]Reference{}
	for _, reference := range references {
		byStore[reference.Store] = append(byStore[reference.Store], reference)
	}
	names := make([]string, 0, len(byStore))
	for name := range byStore {
		names = append(names, name)
	}
	sort.Strings(names)
	out := map[string]Read{}
	for _, name := range names {
		item, err := r.byName(ctx, name)
		if errors.Is(err, ErrNotFound) {
			return nil, &Failure{Store: name, Message: "there is no secret store by that name"}
		}
		if err != nil {
			return nil, err
		}
		reads, err := r.resolveStore(ctx, item, byStore[name], options)
		if err != nil {
			r.recordRead(context.WithoutCancel(ctx), item.ID, false, err.Error())
			return nil, err
		}
		r.recordRead(context.WithoutCancel(ctx), item.ID, true, fmt.Sprintf("read %d %s", len(reads), pluralWord(len(reads), "reference")))
		for key, read := range reads {
			out[key] = read
		}
	}
	return out, nil
}

func (r *Repository) resolveStore(ctx context.Context, item *row, references []Reference, options ResolveOptions) (map[string]Read, error) {
	fail := func(reference string, err error) error {
		return &Failure{Store: item.Name, Reference: reference, Message: mask(err.Error(), item.credentials)}
	}
	if options.Preview && !item.AllowPreviews {
		return nil, fail("", errors.New("it does not serve previews; allow previews on the store, or use a store meant for them"))
	}
	for _, reference := range references {
		if err := checkFits(item.Kind, reference); err != nil {
			return nil, fail(reference.Location(), errors.New(strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")))
		}
	}
	session, err := openSession(ctx, item)
	if err != nil {
		return nil, fail("", err)
	}
	defer session.close(ctx)
	out := map[string]Read{}
	paths := map[string]map[string]secretValue{}
	for _, reference := range references {
		if _, done := out[reference.String()]; done {
			continue
		}
		var value secretValue
		if reference.Version > 0 {
			value, err = session.readVersion(ctx, reference.Path, reference.Key, reference.Version)
			if err != nil {
				return nil, fail(reference.Location(), err)
			}
		} else {
			values, read := paths[reference.Path]
			if !read {
				values, err = session.readPath(ctx, reference.Path)
				if err != nil {
					return nil, fail(reference.Location(), err)
				}
				paths[reference.Path] = values
			}
			var ok bool
			value, ok = values[reference.Key]
			if !ok {
				where := "the config"
				if reference.Path != "" {
					where = reference.Path
				} else if item.Kind == KindInfisical {
					where = "/"
				}
				return nil, fail(reference.Location(), fmt.Errorf("%s has no key %s", where, reference.Key))
			}
		}
		out[reference.String()] = Read{Value: value.value, Version: value.version}
	}
	return out, nil
}

// mask hides any credential of the connection that a store's answer, or an
// error built from one, happens to repeat. None of the stores echo a token,
// but a message is copied into a run's transcript, the audit log and the
// page, and this is the one place it can be stopped.
func mask(message string, credentials map[string]string) string {
	for _, value := range credentials {
		if len(value) >= 4 {
			message = strings.ReplaceAll(message, value, "••••")
		}
	}
	return message
}

// Probe is a path and key Test reads as well as signing in, to show the
// connection can read what references will ask for. The value is discarded.
type Probe struct {
	Path string `json:"path"`
	Key  string `json:"key"`
}

// TestResult is what Test found: whether the store answered, its own words
// for who it took the connection to be, and for a probe the version found.
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Version string `json:"version,omitempty"`
}

// Test signs in to a store, asks who it took the connection to be, and reads
// a probe if one is given. A refusal is a result, not an error: it is the
// store's answer, recorded on the connection for the page to show.
func (r *Repository) Test(ctx context.Context, id int64, probe *Probe) (*TestResult, error) {
	item, err := r.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	var reference *Reference
	if probe != nil && (strings.TrimSpace(probe.Path) != "" || strings.TrimSpace(probe.Key) != "") {
		target := item.Name
		if path := strings.Trim(strings.TrimSpace(probe.Path), "/"); path != "" {
			target += "/" + path
		}
		parsed, err := ParseReference(target + "#" + strings.TrimSpace(probe.Key))
		if err == nil {
			err = checkFits(item.Kind, parsed)
		}
		if err != nil {
			return nil, err
		}
		reference = &parsed
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := r.test(ctx, item, reference)
	r.recordTest(context.WithoutCancel(ctx), item.ID, result.OK, result.Message)
	return result, nil
}

func (r *Repository) test(ctx context.Context, item *row, reference *Reference) *TestResult {
	failed := func(err error) *TestResult {
		return &TestResult{Message: mask(err.Error(), item.credentials)}
	}
	session, err := openSession(ctx, item)
	if err != nil {
		return failed(err)
	}
	defer session.close(ctx)
	who, err := session.identity(ctx)
	if err != nil {
		return failed(err)
	}
	result := &TestResult{OK: true, Message: strings.ToUpper(who[:1]) + who[1:]}
	if reference == nil {
		return result
	}
	values, err := session.readPath(ctx, reference.Path)
	if err != nil {
		return failed(fmt.Errorf("%s; reading %s: %v", result.Message, reference.Location(), err))
	}
	value, ok := values[reference.Key]
	if !ok {
		where := reference.Path
		if where == "" {
			where = "the top level"
		}
		return failed(fmt.Errorf("%s; %s has no key %s", result.Message, where, reference.Key))
	}
	result.Version = value.version
	found := "found " + reference.Location()
	if value.version != "" {
		found += ", version " + value.version
	}
	result.Message += "; " + found
	return result
}
