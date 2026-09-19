package deploy

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// These exercise the actual SSH/bearer plumbing end to end: a real temporary
// credential file is written, permissioned and read back, and a real local
// bare repository stands in for "a remote" wherever the transport itself
// (rather than the credential material) is what is under test — a real
// HTTPS/SSH remote is impossible to stand up in this sandbox, exactly the
// limit the brief calls out.

func TestGitBearerEnvironmentScopesTheHeaderToTheExactRemoteAndCleansUp(t *testing.T) {
	t.Parallel()
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(),
		nil, credentialReaderFake{material: CredentialMaterial{Kind: CredentialGitBearer, Secret: "tok-en-value"}})
	environment, cleanup, err := analyzer.gitEnvironment(context.Background(), t.TempDir(), "https://example.test/owner/repo.git", 7)
	if err != nil {
		t.Fatal(err)
	}
	path := findEnvValue(t, environment, "GIT_CONFIG_GLOBAL")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential file mode = %v, want 0600", info.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `[http "https://example.test/owner/repo.git"]`) ||
		!strings.Contains(string(content), "Authorization: Basic eC1hY2Nlc3MtdG9rZW46dG9rLWVuLXZhbHVl") {
		t.Fatalf("credential file content = %q", content)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("credential file survived cleanup: %v", err)
	}
}

func TestGitBearerEnvironmentRefusesAnSSHRemote(t *testing.T) {
	t.Parallel()
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(),
		nil, credentialReaderFake{material: CredentialMaterial{Kind: CredentialGitBearer, Secret: "tok-en-value"}})
	_, _, err := analyzer.gitEnvironment(context.Background(), t.TempDir(), "git@example.test:owner/repo.git", 7)
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("bearer over ssh remote error = %v, want an HTTPS-required refusal", err)
	}
}

func TestGitSSHEnvironmentWritesAPrivateKeyFileAndPointsGitAtItExclusively(t *testing.T) {
	t.Parallel()
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(),
		nil, credentialReaderFake{material: CredentialMaterial{Kind: CredentialGitSSH, Secret: testPrivateKeyPEM}})
	environment, cleanup, err := analyzer.gitEnvironment(context.Background(), t.TempDir(), "git@example.test:owner/repo.git", 9)
	if err != nil {
		t.Fatal(err)
	}
	command := findEnvValue(t, environment, "GIT_SSH_COMMAND")
	for _, want := range []string{
		"-F /dev/null", "-o IdentitiesOnly=yes", "-o IdentityFile=",
		"-o UserKnownHostsFile=/dev/null", "-o GlobalKnownHostsFile=/dev/null",
		"-o StrictHostKeyChecking=accept-new", "-o BatchMode=yes",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("GIT_SSH_COMMAND = %q, missing %q", command, want)
		}
	}
	keyPath := extractIdentityFile(t, command)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, want 0600", info.Mode().Perm())
	}
	content, err := os.ReadFile(keyPath)
	if err != nil || string(content) != testPrivateKeyPEM {
		t.Fatalf("key file content mismatch: %v", err)
	}
	for _, entry := range environment {
		if strings.HasPrefix(entry, "SSH_AUTH_SOCK=") {
			t.Fatalf("SSH_AUTH_SOCK leaked into the git environment: %q", entry)
		}
	}
	cleanup()
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("key file survived cleanup: %v", err)
	}
}

func TestGitSSHEnvironmentRefusesAnHTTPSRemote(t *testing.T) {
	t.Parallel()
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(),
		nil, credentialReaderFake{material: CredentialMaterial{Kind: CredentialGitSSH, Secret: testPrivateKeyPEM}})
	_, _, err := analyzer.gitEnvironment(context.Background(), t.TempDir(), "https://example.test/owner/repo.git", 9)
	if err == nil || !strings.Contains(err.Error(), "SSH remote") {
		t.Fatalf("ssh key over https remote error = %v, want an SSH-required refusal", err)
	}
}

func TestGitSSHEnvironmentRefusesAMalformedKey(t *testing.T) {
	t.Parallel()
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(),
		nil, credentialReaderFake{material: CredentialMaterial{Kind: CredentialGitSSH, Secret: "not-a-pem-key"}})
	_, _, err := analyzer.gitEnvironment(context.Background(), t.TempDir(), "git@example.test:owner/repo.git", 9)
	if err == nil || !strings.Contains(err.Error(), "PEM") {
		t.Fatalf("malformed key error = %v, want a PEM refusal", err)
	}
}

func TestGitEnvironmentRefusesACredentialKindNotValidForGit(t *testing.T) {
	t.Parallel()
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(),
		nil, credentialReaderFake{material: CredentialMaterial{Kind: CredentialRegistry, Secret: "pw"}})
	_, _, err := analyzer.gitEnvironment(context.Background(), t.TempDir(), "https://example.test/owner/repo.git", 5)
	if err == nil || !strings.Contains(err.Error(), "not valid for Git") {
		t.Fatalf("registry credential for Git error = %v, want a kind refusal", err)
	}
}

// TestGitEnvironmentAgainstARealLocalRepositoryStaysUsableWithoutACredential
// proves the shared plumbing (environment sanitization, GIT_CONFIG_GLOBAL,
// GIT_TERMINAL_PROMPT) does not break an ordinary Git invocation, using a
// real bare repository over the file transport in place of a network remote.
func TestGitEnvironmentAgainstARealLocalRepositoryStaysUsableWithoutACredential(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	runPlanningGitFixture(t, repository, "init", "-q", "-b", "main")
	runPlanningGitFixture(t, repository, "config", "user.email", "fixture@example.test")
	runPlanningGitFixture(t, repository, "config", "user.name", "Fixture")
	writeBuildFixture(t, repository, "file.txt", "hello\n")
	runPlanningGitFixture(t, repository, "add", "file.txt")
	runPlanningGitFixture(t, repository, "commit", "-q", "-m", "fixture")

	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), nil, credentialReaderFake{})
	environment, cleanup, err := analyzer.gitEnvironment(context.Background(), t.TempDir(), repository, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out, err := runPlanningGit(context.Background(), "", environment, "ls-remote", "--exit-code", repository, "refs/heads/main")
	if err != nil || !strings.Contains(out, "refs/heads/main") {
		t.Fatalf("ls-remote against a real local repository = %q, %v", out, err)
	}
}

func TestGitTestRemoteBuildsAFullURLOrCombinesAnOwnerNameShorthandWithTheSavedTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, kind, target, repository, want string
		wantErr                              bool
	}{
		{name: "full https url is used as-is", kind: CredentialGitBearer, repository: "https://example.test/owner/repo.git", want: "https://example.test/owner/repo.git"},
		{name: "owner/name combines with the saved target over https", kind: CredentialGitBearer, target: "example.test", repository: "owner/repo", want: "https://example.test/owner/repo.git"},
		{name: "owner/name combines with the saved target over ssh for git_ssh", kind: CredentialGitSSH, target: "example.test", repository: "owner/repo", want: "git@example.test:owner/repo.git"},
		{name: "owner/name without a saved target is refused", kind: CredentialGitBearer, repository: "owner/repo", wantErr: true},
		{name: "empty repository is refused", kind: CredentialGitBearer, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := gitTestRemote(test.kind, test.target, test.repository)
			if test.wantErr {
				if err == nil {
					t.Fatalf("gitTestRemote(%q, %q, %q) = %q, want an error", test.kind, test.target, test.repository, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("gitTestRemote(%q, %q, %q) = %q, %v, want %q", test.kind, test.target, test.repository, got, err, test.want)
			}
		})
	}
}

// TestTestCredentialRunsAGitProbeAndReportsFailureWithoutTheSecret exercises
// TestCredential's real control flow — OpenCredential, gitEnvironment,
// runPlanningGit — against a loopback address nothing listens on, since a
// reachable HTTPS Git host is not available in this sandbox. The connection
// is refused immediately rather than timing out, and the assertion that
// matters is that the secret never appears in the answer.
func TestTestCredentialRunsAGitProbeAndReportsFailureWithoutTheSecret(t *testing.T) {
	t.Parallel()
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), nil,
		credentialReaderFake{material: CredentialMaterial{Kind: CredentialGitBearer, Secret: "super-secret-token"}})
	ok, message, err := analyzer.TestCredential(context.Background(), 11, "https://127.0.0.1:1/owner/repo.git")
	if err != nil {
		t.Fatalf("TestCredential returned an error instead of ok:false: %v", err)
	}
	if ok {
		t.Fatalf("TestCredential against a refused connection reported ok:true")
	}
	if strings.Contains(message, "super-secret-token") {
		t.Fatalf("TestCredential leaked the secret into its message: %q", message)
	}
	if message == "" {
		t.Fatal("TestCredential returned an empty failure message")
	}
}

func TestTestCredentialRefusesAnEmptyRepositoryForAGitKind(t *testing.T) {
	t.Parallel()
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), nil,
		credentialReaderFake{material: CredentialMaterial{Kind: CredentialGitBearer, Secret: "tok-en"}})
	_, _, err := analyzer.TestCredential(context.Background(), 11, "")
	if err == nil {
		t.Fatal("TestCredential with no repository did not error for a Git credential")
	}
}

func TestTestCredentialResolvesARegistryManifestAndReportsFailureWithoutTheSecret(t *testing.T) {
	t.Parallel()
	docker := &planningDockerFake{}
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), docker, credentialReaderFake{
		material: CredentialMaterial{
			Kind:   CredentialRegistry,
			Config: mustJSON(credentialConfig{ServerAddress: "registry.example.test", Username: "svc"}),
			Secret: "registry-super-secret",
		},
	})
	ok, message, err := analyzer.TestCredential(context.Background(), 12, "registry.example.test/team/app")
	if err != nil {
		t.Fatalf("registry test with no resolvable image returned an error: %v", err)
	}
	if ok {
		t.Fatal("registry test with no configured image reported ok:true")
	}
	if strings.Contains(message, "registry-super-secret") {
		t.Fatalf("registry test leaked the secret: %q", message)
	}

	docker.image = &dockerx.DistributionImage{Digest: "sha256:" + strings.Repeat("a", 64)}
	ok, message, err = analyzer.TestCredential(context.Background(), 12, "registry.example.test/team/app")
	if err != nil || !ok {
		t.Fatalf("registry test with a resolvable image = %v, %q, %v, want ok", ok, message, err)
	}
	if docker.registryAuth == "" {
		t.Fatal("registry test never passed an auth header to ResolveDistributionImage")
	}
	if strings.Contains(message, "registry-super-secret") {
		t.Fatalf("registry test leaked the secret on success: %q", message)
	}
}

func findEnvValue(t *testing.T, environment []string, key string) string {
	t.Helper()
	prefix := key + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	t.Fatalf("environment has no %s entry: %v", key, environment)
	return ""
}

// extractIdentityFile pulls the path out of "-o IdentityFile='<path>'" the
// same shellQuote wrapping produced it with.
func extractIdentityFile(t *testing.T, command string) string {
	t.Helper()
	marker := "-o IdentityFile='"
	start := strings.Index(command, marker)
	if start < 0 {
		t.Fatalf("command has no quoted IdentityFile: %q", command)
	}
	start += len(marker)
	end := strings.Index(command[start:], "'")
	if end < 0 {
		t.Fatalf("command has an unterminated IdentityFile: %q", command)
	}
	return command[start : start+end]
}
