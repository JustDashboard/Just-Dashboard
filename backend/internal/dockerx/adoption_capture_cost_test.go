package dockerx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAdoptionCaptureUsesOnlyAuthoritativeImageRead(t *testing.T) {
	imageID := "sha256:" + strings.Repeat("a", 64)
	var mu sync.Mutex
	counts := map[string]int{}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.47")
		case strings.HasSuffix(r.URL.Path, "/containers/original/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "original", "Image": imageID, "Config": map[string]any{}, "HostConfig": map[string]any{}})
		case strings.HasSuffix(r.URL.Path, "/changes"):
			_ = json.NewEncoder(w).Encode([]any{})
		case strings.Contains(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": imageID, "Os": "linux", "Architecture": "amd64", "Config": map[string]any{"Env": []string{"PLAIN=original-image-default"}}})
		default:
			t.Errorf("capture queried unrelated image presentation: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer engine.Close()
	client := New(engine.URL)
	defer client.Close()
	capture, err := client.CaptureAdoptionContainer(t.Context(), "original")
	if err != nil {
		t.Fatal(err)
	}
	if capture.Image == nil || capture.Image.ID != imageID || len(capture.Image.Env) != 1 {
		t.Fatal("lean read lost authoritative image metadata")
	}
	mu.Lock()
	defer mu.Unlock()
	for path, count := range counts {
		if count != 1 || strings.HasSuffix(path, "/history") || strings.HasSuffix(path, "/containers/json") {
			t.Fatal("unneeded repeated Docker read", path, count)
		}
	}
}

func TestAdoptionCaptureTimeoutNamesSafePhaseAndRetainsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := adoptionCaptureFailure(ctx, "writable_layer", errors.New("untrusted-engine-private-response"))
	var captureError *AdoptionCaptureError
	if !errors.As(err, &captureError) || captureError.Stage != "writable_layer" || !errors.Is(err, context.Canceled) {
		t.Fatal("capture error lost its phase/cancellation", err)
	}
	expired, expireCancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer expireCancel()
	if err := adoptionCaptureFailure(expired, "image", errors.New("private engine reply")); !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "time limit") {
		t.Fatal("deadline diagnostic lost", err)
	}
	if strings.Contains(err.Error(), "private-response") {
		t.Fatal("capture error leaked the underlying engine response")
	}
}
