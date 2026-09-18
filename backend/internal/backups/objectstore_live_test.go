package backups

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// An object-storage target, end to end against a real S3 API: the target
// test says a missing bucket is missing, a run uploads and drops its staging
// copy, retention deletes the older object and nothing else, and a restore
// downloads exactly what was archived. MinIO stands in for S3 and B2; the
// code path is the one both take.
func TestLiveObjectStorageBackupUploadsPrunesAndRestores(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to back up to a real S3-compatible service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	password := make([]byte, 16)
	if _, err := rand.Read(password); err != nil {
		t.Fatal(err)
	}
	secrets := &TargetSecrets{AccessKeyID: "jdbackup", SecretAccessKey: hex.EncodeToString(password)}
	name := fmt.Sprintf("jd-minio-backup-%d", time.Now().UnixNano())
	docker("run", "-d", "--name", name, "-e", "MINIO_ROOT_USER="+secrets.AccessKeyID, "-e", "MINIO_ROOT_PASSWORD="+secrets.SecretAccessKey,
		"-p", "127.0.0.1::9000", "quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z", "server", "/data")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", name).Run()
	endpoint := "http://" + docker("port", name, "9000/tcp")
	for deadline := time.Now().Add(time.Minute); ; {
		response, err := http.Get(endpoint + "/minio/health/live")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("MinIO did not come up: %v", err)
		}
		time.Sleep(time.Second)
	}

	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sealer, err := auth.NewSealer(strings.Repeat("5c", 32))
	if err != nil {
		t.Fatal(err)
	}
	backups := NewStore(db, sealer)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("first revision"), 0o600); err != nil {
		t.Fatal(err)
	}
	job := &Job{
		Name: "objects", Sources: []string{source}, Enabled: true, Schedule: "0 3 * * *", Retention: 1,
		TargetKind: TargetS3, Target: TargetConfig{Bucket: "jd-backups", Prefix: "nightly", Endpoint: endpoint, Region: "us-east-1"},
	}
	if err := TestTarget(ctx, job, secrets); err == nil {
		t.Fatal("a bucket that does not exist passed the target test")
	}
	client, err := s3Client(ctx, job, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &job.Target.Bucket}); err != nil {
		t.Fatal(err)
	}
	if err := TestTarget(ctx, job, secrets); err != nil {
		t.Fatalf("target test after creating the bucket: %v", err)
	}
	created, err := backups.Create(ctx, job, secrets)
	if err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	runner := NewRunner(backups, stage, slog.New(slog.NewTextHandler(io.Discard, nil)))
	first, err := runner.Execute(ctx, created.ID, "test")
	if err != nil || first.Status != StatusSuccess || !strings.HasPrefix(first.Artifact, "nightly/") {
		t.Fatalf("first run = %+v, %v\n%s", first, err, first.Log)
	}
	exists := func(key string) bool {
		t.Helper()
		_, err := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &job.Target.Bucket, Key: &key})
		var missing *types.NotFound
		if errors.As(err, &missing) {
			return false
		}
		if err != nil {
			t.Fatalf("head %s: %v", key, err)
		}
		return true
	}
	if !exists(first.Artifact) {
		t.Fatalf("%s was not uploaded", first.Artifact)
	}
	if entries, _ := os.ReadDir(stage); len(entries) != 0 {
		t.Fatalf("staging copy left behind: %v", entries)
	}
	if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("second revision"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The uploader names artifacts by the second; a second run inside the
	// same one would overwrite the first object rather than add a second.
	time.Sleep(1100 * time.Millisecond)
	second, err := runner.Execute(ctx, created.ID, "test")
	if err != nil || second.Status != StatusSuccess || second.Artifact == first.Artifact {
		t.Fatalf("second run = %+v, %v\n%s", second, err, second.Log)
	}
	if exists(first.Artifact) || !exists(second.Artifact) {
		t.Fatalf("retention of one kept the wrong objects: first=%v second=%v", exists(first.Artifact), exists(second.Artifact))
	}
	destination := t.TempDir()
	result, err := runner.Restore(ctx, second.ID, destination)
	if err != nil || result.Entries == 0 {
		t.Fatalf("restore = %+v, %v", result, err)
	}
	var restored []byte
	if err := filepath.WalkDir(destination, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && entry.Name() == "notes.txt" {
			restored, err = os.ReadFile(path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if string(restored) != "second revision" {
		t.Fatalf("restored notes.txt = %q", restored)
	}
}
