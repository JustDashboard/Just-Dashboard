package backups

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// An encrypting job against a real S3 API: what lands in the bucket is an
// age file and not the archive, and a restore downloads it, proves its digest
// and decrypts it on the way to disk. JD_TEST_S3_ENDPOINT names an
// S3-compatible service (a MinIO is enough; B2 takes the same path) and
// JD_TEST_S3_ACCESS_KEY / JD_TEST_S3_SECRET_KEY its keys. The test makes a
// bucket of its own and empties and removes it afterwards.
func TestLiveEncryptedArchiveRoundTripsThroughObjectStorage(t *testing.T) {
	endpoint := os.Getenv("JD_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set JD_TEST_S3_ENDPOINT, JD_TEST_S3_ACCESS_KEY and JD_TEST_S3_SECRET_KEY to an S3-compatible service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	secrets := &TargetSecrets{AccessKeyID: os.Getenv("JD_TEST_S3_ACCESS_KEY"), SecretAccessKey: os.Getenv("JD_TEST_S3_SECRET_KEY")}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	job := &Job{
		Name: "sealed-objects", Enabled: true, Schedule: "0 3 * * *", Encrypt: true,
		TargetKind: TargetS3, Target: TargetConfig{Bucket: "jdt-backup-encryption-" + hex.EncodeToString(suffix),
			Prefix: "nightly", Endpoint: endpoint, Region: "us-east-1"},
	}
	client, err := s3Client(ctx, job, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &job.Target.Bucket}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		listed, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &job.Target.Bucket})
		if err == nil {
			for _, object := range listed.Contents {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &job.Target.Bucket, Key: object.Key})
			}
		}
		if _, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: &job.Target.Bucket}); err != nil {
			t.Errorf("bucket cleanup: %v", err)
		}
	})

	db, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sealer, err := auth.NewSealer(strings.Repeat("5c", 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, sealer)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("sealed revision"), 0o600); err != nil {
		t.Fatal(err)
	}
	job.Sources = []string{source}
	created, err := store.Create(ctx, job, secrets)
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(store, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	run, err := runner.Execute(ctx, created.ID, "test")
	if err != nil || run.Status != StatusSuccess || !strings.HasPrefix(run.Artifact, "nightly/") || !strings.HasSuffix(run.Artifact, ".tar.gz.age") {
		t.Fatalf("run = %+v, %v", run, err)
	}
	object, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: &job.Target.Bucket, Key: &run.Artifact})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := io.ReadAll(object.Body)
	object.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(stored, []byte("age-encryption.org/v1\n")) || digestOf(stored) != run.Manifest.ArtifactDigest {
		t.Fatalf("the bucket holds %d bytes that are not the sealed artifact", len(stored))
	}
	destination := t.TempDir()
	if _, err := runner.Restore(ctx, run.ID, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(filepath.Join(destination, "source-0001", "notes.txt"))
	if err != nil || string(restored) != "sealed revision" {
		t.Fatalf("restored notes.txt = %q, %v", restored, err)
	}
}
