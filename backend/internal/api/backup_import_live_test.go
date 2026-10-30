package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// A server migration, end to end against a real S3 API and a real Docker:
// one server backs up a Docker volume and a directory to a bucket with
// encryption on; then a second server with a store of its own — the fresh
// host — imports the key it exported, reads the bucket's catalogue, imports
// the job's history, and restores both in place after the volume and the
// directory are gone: the volume is made again through Docker with its
// labels, the directory with its parents, and every byte is what was backed
// up. Nothing the restore staged is left behind. LocalStack 4.14.0 (the last
// community image that starts without a licence) stands in for S3.
//
// Run as root, because the volume's data lives under Docker's root:
//
//	go test -c -o api.test ./internal/api
//	sudo env JD_DEPLOY_LIVE=1 PATH=$PATH ./api.test -test.run TestLiveImportedBackupRestoresOnAFreshServer -test.v
func TestLiveImportedBackupRestoresOnAFreshServer(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	if os.Geteuid() != 0 {
		t.Skip("run as root: a volume's data is under Docker's root")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	suffix := strings.ToLower(rand.Text()[:10])

	// S3, on loopback only.
	container := "jd-import-live-s3-" + suffix
	docker("run", "-d", "--name", container, "-e", "SERVICES=s3", "-e", "EAGER_SERVICE_LOADING=1",
		"-p", "127.0.0.1::4566", "localstack/localstack:4.14.0")
	t.Cleanup(func() { _ = hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", container).Run() })
	endpoint := "http://" + docker("port", container, "4566/tcp")
	for deadline := time.Now().Add(2 * time.Minute); ; {
		response, err := http.Get(endpoint + "/_localstack/health")
		if err == nil {
			var health struct {
				Services map[string]string `json:"services"`
			}
			_ = json.NewDecoder(response.Body).Decode(&health)
			response.Body.Close()
			if state := health.Services["s3"]; state == "available" || state == "running" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("LocalStack did not come up: %v", err)
		}
		time.Sleep(time.Second)
	}
	bucket := "jd-import-" + suffix
	bucketClient := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: aws.String(endpoint), UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", "")})
	if _, err := bucketClient.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		listed, err := bucketClient.ListObjectsV2(cleanup, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
		if err == nil {
			for _, object := range listed.Contents {
				_, _ = bucketClient.DeleteObject(cleanup, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: object.Key})
			}
		}
		_, _ = bucketClient.DeleteBucket(cleanup, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	})

	// What the old server holds: a labelled volume and a directory, with
	// bytes nobody could guess.
	volume := "jd-import-live-" + suffix
	docker("volume", "create", "--label", "io.just-dashboard.test=import-"+suffix, volume)
	t.Cleanup(func() { _ = hostexec.Command(context.Background(), "docker", "volume", "rm", "-f", volume).Run() })
	mountpoint := docker("volume", "inspect", "-f", "{{.Mountpoint}}", volume)
	root := t.TempDir()
	directory := filepath.Join(root, "old-server", "srv", "site")
	want := map[string][]byte{}
	write := func(path string, size int) {
		t.Helper()
		data := make([]byte, size)
		_, _ = rand.Read(data)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o640); err != nil {
			t.Fatal(err)
		}
		want[path] = data
	}
	write(filepath.Join(mountpoint, "pgdata", "base", "16384"), 3<<20)
	write(filepath.Join(mountpoint, "PG_VERSION"), 3)
	write(filepath.Join(directory, "index.html"), 40<<10)
	write(filepath.Join(directory, "assets", "app.js"), 900<<10)

	old := backupServerWithRoots(t, "/")
	oldAdmin := &client{t: t, h: old.Routes(), cookie: signInAs(t, old, "old-admin", auth.RoleAdmin)}
	target := fmt.Sprintf(`"targetKind":"s3","target":{"bucket":%q,"prefix":"servers/old","endpoint":%q,"region":"us-east-1"},"secrets":{"accessKeyId":"test","secretAccessKey":"test"}`, bucket, endpoint)
	response := oldAdmin.do(http.MethodPost, "/api/v1/backups/", fmt.Sprintf(`{"name":"Site and database",%s,"sources":[%q,%q],"retention":3,"enabled":true,"schedule":"0 3 * * *","encrypt":true}`,
		target, mountpoint, directory), nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create job: %d %s", response.Code, response.Body.String())
	}
	job := decodeBackupJob(t, response.Body.Bytes())
	run, err := old.modules.backupRunner.Execute(ctx, job.ID, "test")
	if err != nil || run.Status != backups.StatusSuccess || run.Manifest.Encryption == nil {
		t.Fatalf("backup = %+v, %v\n%s", run, err, run.Log)
	}
	if source := run.Manifest.Sources[0]; source.Kind != backups.SourceVolume || source.Volume != volume ||
		source.VolumeLabels["io.just-dashboard.test"] != "import-"+suffix {
		t.Fatalf("the volume was not recorded as one: %+v\n%s", source, run.Log)
	}
	stored, err := bucketClient.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err != nil || len(stored.Contents) != 2 {
		t.Fatalf("the bucket holds %d objects, want the archive and its manifest: %v", len(stored.Contents), err)
	}
	exported := oldAdmin.do(http.MethodPost, "/api/v1/backups/key/export", `{}`, nil)
	if exported.Code != http.StatusOK {
		t.Fatalf("export: %d", exported.Code)
	}

	// The old host is gone, and the new one has neither the volume nor the
	// directory.
	docker("volume", "rm", volume)
	if err := os.RemoveAll(filepath.Join(root, "old-server")); err != nil {
		t.Fatal(err)
	}

	fresh := backupServerWithRoots(t, "/")
	admin := &client{t: t, h: fresh.Routes(), cookie: signInAs(t, fresh, "new-admin", auth.RoleAdmin)}
	read := func(method, path, body string, out any) {
		t.Helper()
		response := admin.do(method, path, body, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		if out != nil {
			if err := json.Unmarshal(response.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
	}
	keyBody, _ := json.Marshal(map[string]string{"key": exported.Body.String()})
	read(http.MethodPost, "/api/v1/backups/key/import", string(keyBody), nil)
	var catalogue backups.Catalogue
	read(http.MethodPost, "/api/v1/backups/catalogue", "{"+target+"}", &catalogue)
	if len(catalogue.Origins) != 1 || len(catalogue.Origins[0].Jobs) != 1 || len(catalogue.Origins[0].Jobs[0].Archives) != 1 {
		t.Fatalf("catalogue = %+v", catalogue)
	}
	archive := catalogue.Origins[0].Jobs[0].Archives[0]
	if !archive.Encrypted || !archive.Openable || !archive.Importable || strings.Join(archive.Kinds, ",") != "paths,volume" {
		t.Fatalf("archive = %+v", archive)
	}
	var result backups.ImportResult
	read(http.MethodPost, "/api/v1/backups/import", fmt.Sprintf(`{%s,"originId":%q,"originJobId":%d}`, target, catalogue.Origins[0].ID, job.ID), &result)
	if len(result.Runs) != 1 || !result.Runs[0].Imported {
		t.Fatalf("import = %+v", result)
	}
	imported := result.Runs[0]

	var plan struct {
		Targets []backups.RestoreTarget `json:"targets"`
	}
	read(http.MethodGet, fmt.Sprintf("/api/v1/backups/runs/%d/restore-plan", imported.ID), "", &plan)
	if len(plan.Targets) != 2 || plan.Targets[0].Creates != backups.SourceVolume || plan.Targets[1].Creates != "directory" {
		t.Fatalf("plan = %+v", plan.Targets)
	}
	var restored backups.RestoreResult
	read(http.MethodPost, fmt.Sprintf("/api/v1/backups/runs/%d/restore", imported.ID), `{"inPlace":true}`, &restored)
	if len(restored.Created) != 2 || restored.Created[0] != "volume "+volume || restored.Created[1] != directory {
		t.Fatalf("restore = %+v", restored)
	}
	if label := docker("volume", "inspect", "-f", `{{index .Labels "io.just-dashboard.test"}}`, volume); label != "import-"+suffix {
		t.Fatalf("the volume was made again without its label: %q", label)
	}
	newMountpoint := docker("volume", "inspect", "-f", "{{.Mountpoint}}", volume)
	for path, data := range want {
		if rel, err := filepath.Rel(mountpoint, path); err == nil && !strings.HasPrefix(rel, "..") {
			path = filepath.Join(newMountpoint, rel)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%s restored as %d bytes (%v), want %d", path, len(got), err, len(data))
		}
	}
	// Nothing more than what was backed up, and nothing staged left over.
	count := 0
	for _, tree := range []string{newMountpoint, directory} {
		_ = filepath.WalkDir(tree, func(_ string, entry fs.DirEntry, err error) error {
			if err == nil && entry.Type().IsRegular() {
				count++
			}
			return err
		})
	}
	if count != len(want) {
		t.Fatalf("the restore wrote %d files, the backup held %d", count, len(want))
	}
	for _, server := range []*Server{old, fresh} {
		entries, _ := os.ReadDir(filepath.Join(server.Cfg.DataDir, "staging"))
		if len(entries) != 0 {
			t.Fatalf("staging left behind in %s: %v", server.Cfg.DataDir, entries)
		}
	}
	// The import read the bucket and copied nothing into it or out of it.
	after, err := bucketClient.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err != nil || len(after.Contents) != 2 {
		t.Fatalf("the bucket changed: %d objects, %v", len(after.Contents), err)
	}
}
