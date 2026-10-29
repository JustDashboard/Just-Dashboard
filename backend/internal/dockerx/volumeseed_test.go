package dockerx

import (
	"context"
	"strings"
	"testing"
)

// A seed's path is judged before the daemon is asked anything: a path that
// leaves its base, or a base that is not a clean absolute container path, is
// refused by the helpers themselves, whatever their caller validated.
func TestVolumeSeedHelpersRefuseUncontainedPathsBeforeTheDaemon(t *testing.T) {
	t.Parallel()
	client := New("unix:///nonexistent/docker.sock")
	ctx := context.Background()
	for _, relative := range []string{"", "../etc/passwd", "a/../../b", "a//b"} {
		err := client.WriteContainerTree(ctx, "helper", "/data", relative, []byte("x"), 0, 0, 0)
		if err == nil || !strings.Contains(err.Error(), "not a path inside") {
			t.Errorf("relative %q = %v", relative, err)
		}
	}
	for _, base := range []string{"data", "/data/../etc", "/da\x00ta"} {
		if err := client.WriteContainerTree(ctx, "helper", base, "a", []byte("x"), 0, 0, 0); err == nil {
			t.Errorf("base %q was accepted", base)
		}
		if _, err := client.PathOwnership(ctx, "helper", base); err == nil {
			t.Errorf("ownership of %q was asked", base)
		}
		if _, _, err := client.DigestContainerFile(ctx, "helper", base, 1); err == nil {
			t.Errorf("a digest of %q was asked", base)
		}
	}
	big := make([]byte, maxContainerFileBytes+1)
	if err := client.WriteContainerTree(ctx, "helper", "/data", "a", big, 0, 0, 0); err != ErrContainerFileTooLarge {
		t.Errorf("an oversized file = %v", err)
	}
}
