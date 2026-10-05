package proxysvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var ErrExistingIngressChanged = errors.New("existing proxy configuration or upstream ownership changed")

func (s *Service) WithIngressJournalDir(path string) *Service {
	s.ingressJournalDir = filepath.Clean(path)
	return s
}

// The durable journal is private because the exact surrounding proxy bytes
// may contain credentials. Release evidence contains binding identities only.
type ingressHandoffJournal struct {
	Version      int                    `json:"version"`
	Owner        int64                  `json:"owner"`
	Release      int64                  `json:"release"`
	Phase        string                 `json:"phase"`
	Binding      ExistingIngressBinding `json:"binding"`
	Before       string                 `json:"before"`
	After        string                 `json:"after"`
	BeforeDigest string                 `json:"beforeDigest"`
	AfterDigest  string                 `json:"afterDigest"`
	Mode         uint32                 `json:"mode"`
}

func (s *Service) ingressJournalPath(b ExistingIngressBinding) (string, error) {
	if s.ingressJournalDir == "" || !filepath.IsAbs(s.ingressJournalDir) {
		return "", errors.New("private ingress handoff journal is unavailable")
	}
	return filepath.Join(s.ingressJournalDir, strings.TrimPrefix(routeDigest(b.ProxyKind+"\x00"+b.SourcePath), "sha256:")+".json"), nil
}

func (s *Service) readIngressJournal(b ExistingIngressBinding) (*ingressHandoffJournal, error) {
	path, err := s.ingressJournalPath(b)
	if err != nil {
		return nil, err
	}
	text, err := boundedIngressFileLimit(path, 10<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var journal ingressHandoffJournal
	if json.Unmarshal([]byte(text), &journal) != nil || journal.Version != 1 || journal.BeforeDigest != routeDigest(journal.Before) || journal.AfterDigest != routeDigest(journal.After) || journal.Binding.SourcePath != b.SourcePath || journal.Binding.ProxyKind != b.ProxyKind {
		return nil, ErrExistingIngressChanged
	}
	return &journal, nil
}

func (s *Service) writeIngressJournal(journal *ingressHandoffJournal) error {
	path, err := s.ingressJournalPath(journal.Binding)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.ingressJournalDir, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(s.ingressJournalDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafePath
	}
	raw, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.ingressJournalDir, ".ingress-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	dir, err := os.Open(s.ingressJournalDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Service) existingIngressSource(ctx context.Context, b ExistingIngressBinding) (string, error) {
	if err := s.verifyIngressOwnership(ctx, b); err != nil {
		return "", err
	}
	if b.ProxyKind == "docker-caddy" {
		edge, err := s.dockerCaddy(ctx)
		if err != nil || edge == nil || edge.configurationSynced(ctx) != nil {
			return "", ErrExistingIngressChanged
		}
	}
	if b.ProxyKind == "caddy" && nativeCaddySynced(ctx, b.SourcePath) != nil {
		return "", ErrExistingIngressChanged
	}
	return boundedIngressFile(b.SourcePath)
}

func (s *Service) verifyIngressOwnership(ctx context.Context, b ExistingIngressBinding) error {
	if b.Status != "linked" || b.ID == "" || b.SourceDigest == "" || b.SourcePath == "" {
		return ErrExistingIngressChanged
	}
	if b.SourceIdentity != "" && ingressFileIdentity(b.SourcePath) != b.SourceIdentity {
		return ErrExistingIngressChanged
	}
	switch b.ProxyKind {
	case "nginx", "caddy":
		resolved, err := s.allowedPath(b.SourcePath)
		if err != nil {
			return err
		}
		if resolved != b.SourcePath {
			return ErrExistingIngressChanged
		}
	case "docker-caddy":
		edge, err := s.dockerCaddy(ctx)
		if err != nil {
			return err
		}
		if edge == nil || edge.Identity != b.IngressIdentity || edge.Source != b.SourcePath {
			return ErrExistingIngressChanged
		}
		containers, err := ingressContainers(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, container := range containers {
			if container.ID == edge.ID {
				_, found = container.NetworkSettings.Networks[b.Network]
			}
		}
		if b.Network == "" || !found {
			return ErrExistingIngressChanged
		}
		if b.Continuity == "network_alias" {
			if err := s.verifyIngressAlias(ctx, b, containers, true); err != nil {
				return err
			}
		}
	default:
		return ErrExistingIngressChanged
	}
	return nil
}

func (s *Service) VerifyExistingIngress(ctx context.Context, bindings []ExistingIngressBinding) error {
	// Pending reads need the service lock themselves. Do not call them inside
	// the mutation lock; the final write rechecks the captured source bytes.
	for _, b := range bindings {
		if b.ProxyKind == "nginx" && s.ingressReload == nil {
			pending, err := s.Pending(ctx, "")
			if err != nil || !pending.Running || pending.Reason != "" || pending.Problem != "" || len(pending.Files) > 0 {
				return ErrExistingIngressChanged
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range bindings {
		text, err := s.existingIngressSource(ctx, b)
		if err != nil {
			return err
		}
		digest := routeDigest(text)
		if digest == b.SourceDigest {
			continue
		}
		journal, err := s.readIngressJournal(b)
		if err != nil {
			return err
		}
		if journal == nil || !((journal.Phase == "applied" && digest == journal.AfterDigest) || (journal.Phase == "restored" && digest == journal.BeforeDigest)) {
			return ErrExistingIngressChanged
		}
	}
	return nil
}

func (s *Service) RecoverExistingIngress(ctx context.Context, owner, release int64, bindings []ExistingIngressBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range bindings {
		journal, err := s.readIngressJournal(b)
		if err != nil {
			return err
		}
		if journal == nil {
			continue
		}
		if journal.Owner != owner {
			current, err := boundedIngressFile(b.SourcePath)
			if err != nil || b.Continuity == "retarget" || routeDigest(current) != b.SourceDigest || (journal.Phase != "applied" && journal.Phase != "restored") {
				return ErrExistingIngressChanged
			}
			continue
		}
		if journal.Phase == "applied" || journal.Phase == "restored" {
			continue
		}
		// Resume only the exact operation whose run recorded the candidate;
		// another run first restores a safely attributable interrupted write.
		if err := s.restoreIngressJournal(ctx, journal); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ApplyExistingIngress(ctx context.Context, owner, release int64, bindings []ExistingIngressBinding, targets []ExistingIngressTarget) (resultErr error) {
	if owner <= 0 || release <= 0 {
		return ErrExistingIngressChanged
	}
	if err := s.VerifyExistingIngress(ctx, bindings); err != nil {
		return err
	}
	for _, binding := range bindings {
		if binding.Continuity == "network_alias" {
			target, mode, matched := ingressMatches(binding.Upstream, targets, true)
			containers, err := ingressContainers(ctx)
			if err != nil {
				return err
			}
			if !matched || mode != "network_alias" || target.Service != binding.Service || target.Stopped || !runningIngressAliasOwned(binding, target.ContainerID, containers) {
				return ErrExistingIngressChanged
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byFile := map[string][]ExistingIngressBinding{}
	for _, b := range bindings {
		byFile[b.ProxyKind+"\x00"+b.SourcePath] = append(byFile[b.ProxyKind+"\x00"+b.SourcePath], b)
	}
	completed := []*ingressHandoffJournal{}
	defer func() {
		if resultErr != nil {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			for i := len(completed) - 1; i >= 0; i-- {
				resultErr = errors.Join(resultErr, s.restoreIngressJournal(recovery, completed[i]))
			}
		}
	}()
	for _, group := range byFile {
		b := group[0]
		before, err := s.existingIngressSource(ctx, b)
		if err != nil {
			return err
		}
		journal, err := s.readIngressJournal(b)
		if err != nil {
			return err
		}
		if routeDigest(before) != b.SourceDigest && (journal == nil || journal.Owner != owner || !((journal.Phase == "applied" && routeDigest(before) == journal.AfterDigest) || (journal.Phase == "restored" && routeDigest(before) == journal.BeforeDigest))) {
			return ErrExistingIngressChanged
		}
		after := before
		seen := map[string]bool{}
		for _, binding := range group {
			if binding.Continuity != "retarget" {
				target, mode, ok := ingressMatches(binding.Upstream, targets, binding.ProxyKind == "docker-caddy")
				if !ok || mode != binding.Continuity || target.Service != binding.Service || (binding.Network != "" && target.Network != binding.Network) {
					return ErrExistingIngressChanged
				}
				continue
			}
			key := binding.Selector
			if seen[key] {
				continue
			}
			seen[key] = true
			var target *ExistingIngressTarget
			for i := range targets {
				candidate := &targets[i]
				if candidate.Service == binding.Service && candidate.Network == binding.Network && (candidate.ContainerPort == binding.Port || candidate.ContainerPort == 0) && candidate.Address != "" {
					if target != nil && target.Address != candidate.Address {
						return ErrExistingIngressChanged
					}
					target = candidate
				}
			}
			if target == nil {
				return ErrExistingIngressChanged
			}
			old := binding.Upstream
			if routeDigest(before) != binding.SourceDigest {
				old = ingressCurrentLiteral(before, binding)
			}
			if old == "" {
				return ErrExistingIngressChanged
			}
			next, err := ingressReplacement(old, target.Address, binding.Port)
			if err != nil {
				return err
			}
			after, err = replaceIngressLiteral(after, binding, old, next)
			if err != nil {
				return err
			}
		}
		if before == after {
			continue
		}
		info, err := os.Stat(b.SourcePath)
		if err != nil {
			return err
		}
		operation := &ingressHandoffJournal{Version: 1, Owner: owner, Release: release, Phase: "prepared", Binding: b, Before: before, After: after, BeforeDigest: routeDigest(before), AfterDigest: routeDigest(after), Mode: uint32(info.Mode().Perm())}
		if err := s.writeIngressJournal(operation); err != nil {
			return err
		}
		completed = append(completed, operation)
		if err := writeIngressCAS(b.SourcePath, before, after, os.FileMode(operation.Mode), b.SourceIdentity); err != nil {
			return err
		}
		if err := s.reloadExistingIngress(ctx, b); err != nil {
			return err
		}
		current, err := boundedIngressFile(b.SourcePath)
		if err != nil || routeDigest(current) != operation.AfterDigest {
			return ErrExistingIngressChanged
		}
		operation.Phase = "applied"
		if err := s.writeIngressJournal(operation); err != nil {
			return err
		}
		s.recordChange(ctx, Change{Path: b.SourcePath, Action: ChangeWrite, Before: []byte(before), BeforeExisted: true, After: []byte(after)})
	}
	return nil
}

func (s *Service) RestoreExistingIngress(ctx context.Context, owner, release int64, bindings []ExistingIngressBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range bindings {
		journal, err := s.readIngressJournal(b)
		if err != nil {
			return err
		}
		if journal == nil || journal.Release != release || journal.Phase == "restored" {
			continue
		}
		if journal.Owner != owner {
			return ErrExistingIngressChanged
		}
		if err := s.restoreIngressJournal(ctx, journal); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) restoreIngressJournal(ctx context.Context, journal *ingressHandoffJournal) error {
	if err := s.verifyIngressOwnership(ctx, journal.Binding); err != nil {
		return err
	}
	current, err := boundedIngressFile(journal.Binding.SourcePath)
	if err != nil {
		return err
	}
	if routeDigest(current) != journal.BeforeDigest && routeDigest(current) != journal.AfterDigest && !(journal.Phase == "prepared" && attributableIngressWrite(current, journal.Before, journal.After)) {
		return ErrExistingIngressChanged
	}
	if current != journal.Before {
		if err := writeIngressCAS(journal.Binding.SourcePath, current, journal.Before, os.FileMode(journal.Mode), journal.Binding.SourceIdentity); err != nil {
			return err
		}
	}
	if err := s.reloadExistingIngress(ctx, journal.Binding); err != nil {
		return err
	}
	journal.Phase = "restored"
	return s.writeIngressJournal(journal)
}

// A prepared inode-preserving write can die between WriteAt and Truncate.
// Recover only a prefix written from the recorded candidate over the recorded
// original; an unrelated edit remains a conflict and is never overwritten.
func attributableIngressWrite(current, before, after string) bool {
	if len(current) < len(before) || len(current) > max(len(before), len(after)) {
		return false
	}
	prefix := 0
	for prefix < len(current) && prefix < len(after) && current[prefix] == after[prefix] {
		prefix++
	}
	if prefix == 0 {
		return false
	}
	firstDifference := 0
	for firstDifference < len(before) && firstDifference < len(after) && before[firstDifference] == after[firstDifference] {
		firstDifference++
	}
	if prefix <= firstDifference {
		return false
	}
	if prefix >= len(before) {
		return len(current) == prefix
	}
	return current[prefix:] == before[prefix:]
}

func writeIngressCAS(path, before, after string, mode os.FileMode, expectedIdentity string) error {
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(before)) {
		return ErrExistingIngressChanged
	}
	openedIdentity := ingressInfoIdentity(info)
	if expectedIdentity == "" || openedIdentity != expectedIdentity || ingressFileIdentity(path) != openedIdentity {
		return ErrExistingIngressChanged
	}
	current := make([]byte, len(before))
	if _, err := f.ReadAt(current, 0); err != nil && len(current) > 0 {
		return err
	}
	if string(current) != before {
		return ErrExistingIngressChanged
	}
	if ingressFileIdentity(path) != openedIdentity {
		return ErrExistingIngressChanged
	}
	// Single-file Docker bind mounts retain their inode. Atomic rename would
	// make Caddy continue reading the old file from its pinned mount.
	if _, err := f.WriteAt([]byte(after), 0); err != nil {
		return err
	}
	if err := f.Truncate(int64(len(after))); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if ingressFileIdentity(path) != openedIdentity {
		return ErrExistingIngressChanged
	}
	return nil
}

func (s *Service) reloadExistingIngress(ctx context.Context, b ExistingIngressBinding) error {
	if s.ingressReload != nil {
		return s.ingressReload(ctx, b)
	}
	if b.ProxyKind == "docker-caddy" {
		edge, err := s.dockerCaddy(ctx)
		if err != nil {
			return err
		}
		if edge == nil || edge.Identity != b.IngressIdentity {
			return ErrExistingIngressChanged
		}
		if err := edge.reload(ctx); err != nil {
			return err
		}
		return edge.configurationSynced(ctx)
	}
	if b.ProxyKind == "caddy" {
		if _, err := hostexec.Command(ctx, "caddy", "validate", "--config", b.SourcePath, "--adapter", "caddyfile").CombinedOutput(); err != nil {
			return ErrExistingIngressChanged
		}
		if _, err := hostexec.Command(ctx, "caddy", "reload", "--config", b.SourcePath, "--adapter", "caddyfile").CombinedOutput(); err != nil {
			return ErrExistingIngressChanged
		}
		return nativeCaddySynced(ctx, b.SourcePath)
	}
	prior, reason, err := s.pending.running(ctx, s.pendingMain(), "")
	if err != nil || reason != "" {
		return ErrExistingIngressChanged
	}
	result, err := s.Reload(ctx, KindNginx)
	if err != nil {
		return err
	}
	if result == nil || !result.Reloaded {
		return ErrExistingIngressChanged
	}
	// Verify nginx loaded this generation instead of treating an accepted
	// reload signal as proof that new workers serve the candidate bytes.
	files, err := s.dumpNginx(ctx)
	if err != nil {
		return err
	}
	gen, reason, err := s.pending.running(ctx, s.pendingMain(), prior.token())
	if err != nil || reason != "" {
		return ErrExistingIngressChanged
	}
	paths := []string{}
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	pending, judged := s.pending.compare(s, gen, paths, true)
	if !judged || len(pending) > 0 {
		return ErrExistingIngressChanged
	}
	return nil
}

func ingressCurrentLiteral(content string, b ExistingIngressBinding) string {
	if b.ProxyKind == "nginx" {
		parts := strings.SplitN(b.Selector, ":", 3)
		if len(parts) != 3 {
			return ""
		}
		line, _ := strconv.Atoi(parts[1])
		name := "proxy_pass"
		if parts[0] == "nginx-upstream" {
			name = "server"
		}
		tokens, err := tokenizeNginx(b.SourcePath, content)
		if err != nil {
			return ""
		}
		for i, t := range tokens {
			if t.line == line && t.text == name && i+2 < len(tokens) {
				return tokens[i+1].text
			}
		}
		return ""
	}
	for _, line := range strings.Split(content, "\n") {
		words := strings.Fields(line)
		if (len(words) == 2 || (len(words) == 3 && words[2] == "{")) && words[0] == "reverse_proxy" {
			host, port, ok := ingressEndpoint(words[1])
			_, originalPort, _ := ingressEndpoint(b.Upstream)
			if ok && port == originalPort && host != "" {
				return words[1]
			}
		}
	}
	return ""
}

func replaceIngressLiteral(content string, b ExistingIngressBinding, old, next string) (string, error) {
	if old == next {
		return content, nil
	}
	if b.ProxyKind == "nginx" {
		parts := strings.SplitN(b.Selector, ":", 3)
		if len(parts) != 3 {
			return "", ErrExistingIngressChanged
		}
		line, _ := strconv.Atoi(parts[1])
		name := "proxy_pass"
		if parts[0] == "nginx-upstream" {
			name = "server"
		}
		tokens, err := tokenizeNginx(b.SourcePath, content)
		if err != nil {
			return "", err
		}
		found := -1
		directives := 0
		for i, t := range tokens {
			if t.line == line && t.text == name {
				directives++
			}
			if t.line == line && t.text == name && i+2 < len(tokens) && tokens[i+1].text == old {
				if found >= 0 {
					return "", ErrExistingIngressChanged
				}
				found = i + 1
			}
		}
		if found < 0 || directives != 1 {
			return "", ErrExistingIngressChanged
		}
		token := tokens[found]
		value := next
		if token.quoted {
			value = strconv.Quote(next)
		}
		return content[:token.start] + value + content[token.end:], nil
	}
	if !closedCaddyLiteral(content, old) {
		return "", ErrExistingIngressChanged
	}
	lines := strings.SplitAfter(content, "\n")
	for i, line := range lines {
		words := strings.Fields(line)
		if (len(words) == 2 || (len(words) == 3 && words[2] == "{")) && words[0] == "reverse_proxy" && words[1] == old {
			start := strings.Index(line, old)
			lines[i] = line[:start] + next + line[start+len(old):]
			return strings.Join(lines, ""), nil
		}
	}
	return "", fmt.Errorf("%w: persisted literal is unavailable", ErrExistingIngressChanged)
}
