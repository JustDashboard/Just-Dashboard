package githubrepos

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

const (
	// listTTL is how long a connection's repository list is answered from
	// memory. The page reads it on every visit and after every change it
	// makes; GitHub's hourly budget is the person's, not the page's.
	listTTL = 60 * time.Second
	// accessTTL is how long one repository's access (GitHub's answer for
	// it, the person's role, the installation) is reused between the
	// requests of one page view. Every change made here drops it.
	accessTTL = 30 * time.Second
	// optionsTTL bounds the gitignore and licence catalogues, which change
	// a few times a year.
	optionsTTL = 24 * time.Hour
	// installationsTTL is how old a connection's installation list may be
	// before an answer that depends on it reads it again.
	installationsTTL = 60 * time.Second
	// profileTTL is how long a connection's profile and contribution
	// calendar are answered from memory: both change by the day, not by the
	// minute.
	profileTTL = 5 * time.Minute
	// inboxTTL is how long one page of the account's pull requests or issues
	// is reused. GitHub allows thirty searches a minute, far fewer than any
	// other read, and a page with four filters and two states spends them
	// quickly.
	inboxTTL = 60 * time.Second
	// eventsTTL is how long the account's recent activity is reused.
	eventsTTL = 60 * time.Second
	// maxCached bounds each cache map, so a dashboard left open for a month
	// on a large account keeps a bounded memory.
	maxCached = 512
)

// Service is the GitHub page's backend: the App, the sign-in flows, each
// dashboard account's connections and what they reach.
type Service struct {
	store *Store
	// APIURL is api.github.com, WebURL github.com (sign-in, settings
	// links), GitURL where clones are fetched from. Tests point all three at
	// a fake; nothing else changes them.
	APIURL string
	WebURL string
	GitURL string
	HTTP   *http.Client
	now    func() time.Time

	mu         sync.Mutex
	states     map[string]pendingState
	devices    map[string]*deviceFlow
	refreshing map[int64]*sync.Mutex
	lists      map[int64]cachedList
	access     map[accessKey]cachedAccess
	options    cachedOptions
	profiles   answers[int64, Profile]
	inboxes    answers[inboxKey, InboxPage]
	events     answers[int64, EventList]
}

func New(db *sql.DB, sealer *auth.Sealer) *Service {
	return &Service{
		store:  NewStore(db, sealer),
		APIURL: DefaultAPIURL, WebURL: DefaultWebURL, GitURL: DefaultWebURL,
		HTTP: &http.Client{Timeout: 30 * time.Second}, now: time.Now,
		states: map[string]pendingState{}, devices: map[string]*deviceFlow{}, refreshing: map[int64]*sync.Mutex{},
		lists: map[int64]cachedList{}, access: map[accessKey]cachedAccess{},
		profiles: answers[int64, Profile]{}, inboxes: answers[inboxKey, InboxPage]{}, events: answers[int64, EventList]{},
	}
}

// SetClock replaces the service's clock, for tests of expiry.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

func (s *Service) webURL() string { return strings.TrimRight(s.WebURL, "/") }
func (s *Service) apiURL() string { return strings.TrimRight(s.APIURL, "/") }

type cachedList struct {
	at   time.Time
	list RepositoryList
}

type accessKey struct {
	connection  int64
	owner, name string
}

func keyOf(connection int64, owner, name string) accessKey {
	return accessKey{connection: connection, owner: strings.ToLower(owner), name: strings.ToLower(name)}
}

type cachedAccess struct {
	at     time.Time
	access RepoAccess
	raw    rawRepository
}

type cachedOptions struct {
	at         time.Time
	gitignores []string
	licenses   []License
}

// answers is a bounded map of answers kept until a time each. It is guarded
// by Service.mu like the maps beside it; what it holds is shared between
// requests, so nobody changes an answer once it is in.
type answers[K comparable, V any] map[K]answer[V]

type answer[V any] struct {
	until time.Time
	value V
}

func (m answers[K, V]) get(key K, now time.Time) (V, bool) {
	entry, ok := m[key]
	if !ok || !now.Before(entry.until) {
		var none V
		return none, false
	}
	return entry.value, true
}

// put keeps value until now+ttl, making room first: what has expired goes,
// then anything, so the map never passes maxCached.
func (m answers[K, V]) put(key K, value V, now time.Time, ttl time.Duration) {
	if _, replacing := m[key]; !replacing && len(m) >= maxCached {
		for other, entry := range m {
			if !now.Before(entry.until) {
				delete(m, other)
			}
		}
		for other := range m {
			if len(m) < maxCached {
				break
			}
			delete(m, other)
		}
	}
	m[key] = answer[V]{until: now.Add(ttl), value: value}
}

// forgetConnection drops everything cached for one connection: its list,
// every repository read through it, and the account's own profile, inbox
// pages and activity.
func (s *Service) forgetConnection(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.lists, id)
	for key := range s.access {
		if key.connection == id {
			delete(s.access, key)
		}
	}
	delete(s.profiles, id)
	delete(s.events, id)
	for key := range s.inboxes {
		if key.connection == id {
			delete(s.inboxes, key)
		}
	}
}

// Forget drops what is cached about one repository after a change to it: its
// access, the connection's repository list, which shows it, and the
// account's pull requests, issues and activity, which may list what changed
// (a request merged, an issue closed or opened). The inbox pages are dropped
// whole: one that did not hold the repository's items yet may hold the one
// just opened.
func (s *Service) Forget(connection int64, owner, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.lists, connection)
	delete(s.access, keyOf(connection, owner, name))
	delete(s.events, connection)
	for key := range s.inboxes {
		if key.connection == connection {
			delete(s.inboxes, key)
		}
	}
}

func (s *Service) forgetAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists = map[int64]cachedList{}
	s.access = map[accessKey]cachedAccess{}
	s.devices = map[string]*deviceFlow{}
	s.profiles = answers[int64, Profile]{}
	s.inboxes = answers[inboxKey, InboxPage]{}
	s.events = answers[int64, EventList]{}
}

// trimLocked keeps a cache map under maxCached by dropping the oldest
// entries. Called with s.mu held.
func trimAccessLocked(m map[accessKey]cachedAccess, now time.Time) {
	if len(m) < maxCached {
		return
	}
	for key, entry := range m {
		if now.Sub(entry.at) > accessTTL {
			delete(m, key)
		}
	}
	for key := range m {
		if len(m) < maxCached {
			break
		}
		delete(m, key)
	}
}

func trimListsLocked(m map[int64]cachedList, now time.Time) {
	if len(m) < maxCached {
		return
	}
	for key, entry := range m {
		if now.Sub(entry.at) > listTTL {
			delete(m, key)
		}
	}
	for key := range m {
		if len(m) < maxCached {
			break
		}
		delete(m, key)
	}
}

// backgroundContext is for the writes that must happen after the request
// that caused them has gone: marking a connection revoked, revoking a token.
func backgroundContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}
