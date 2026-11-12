package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"paas.local/backend/internal/config"
	"paas.local/backend/internal/deploy"
	"paas.local/backend/internal/model"
	"paas.local/backend/internal/secure"
)

func TestComposeSecretsAndSerialization(t *testing.T) {
	box, e := secure.New(bytes.Repeat([]byte{7}, 32))
	if e != nil {
		t.Fatal(e)
	}
	s := &Server{box: box}
	input := deploy.ComposeInput{YAML: "services: {web: {image: nginx, environment: {PASSWORD: inline-secret}}}", Environment: map[string]string{"TOKEN": "variable-secret"}, Registries: []deploy.ComposeRegistry{{Server: "registry.example.com", Username: "user", Password: "registry-secret"}}}
	sealed, e := s.sealCompose(input)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"inline-secret", "variable-secret", "registry-secret"} {
		if strings.Contains(sealed, secret) {
			t.Fatal("configuration stored in plaintext")
		}
	}
	restored, e := s.openCompose(sealed)
	if e != nil || restored.YAML != input.YAML || restored.Environment["TOKEN"] != "variable-secret" || restored.Registries[0].Password != "registry-secret" {
		t.Fatalf("encrypted roundtrip failed: %v", e)
	}
	app := model.App{ComposeDraft: sealed, EnvironmentJSON: "[]"}
	raw, e := json.Marshal(s.safeApp(app))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), sealed) || strings.Contains(string(raw), "inline-secret") {
		t.Fatal("app response exposed compose content")
	}
	versionRaw, _ := json.Marshal(model.ComposeVersion{Source: sealed, Resolved: sealed})
	if strings.Contains(string(versionRaw), sealed) {
		t.Fatal("history exposed compose content")
	}
	log := scrubCompose("inline-secret variable-secret registry-secret", []string{"inline-secret", "variable-secret", "registry-secret"})
	if strings.Contains(log, "secret") {
		t.Fatal("operation logs exposed credentials")
	}
}

// Requires an isolated, disposable MySQL database and a running local Docker daemon.
// PAAS_COMPOSE_TEST_DSN must never point to a shared or production database.
func TestComposeAPIIntegration(t *testing.T) {
	dsn := os.Getenv("PAAS_COMPOSE_TEST_DSN")
	if dsn == "" {
		t.Skip("set PAAS_COMPOSE_TEST_DSN to an isolated MySQL test database")
	}
	parsed, parseError := mysqlDriver.ParseDSN(dsn)
	if parseError != nil || parsed.DBName != "paas_compose_smoke" {
		t.Fatal("integration test requires database named paas_compose_smoke")
	}
	db, e := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AutoMigrate(&model.AllowedUser{}, &model.AppPermission{}, &model.App{}, &model.Node{}, &model.ComposeVersion{}, &model.Task{}, &model.Session{}, &model.SecretSetting{}, &model.Build{}, &model.Release{}); e != nil {
		t.Fatal(e)
	}
	box, e := secure.New(bytes.Repeat([]byte{8}, 32))
	if e != nil {
		t.Fatal(e)
	}
	user := model.AllowedUser{Base: model.Base{ID: id()}, Phone: "test-" + id()[:20], Role: "admin", Enabled: true}
	if e = db.Create(&user).Error; e != nil {
		t.Fatal(e)
	}
	defer db.Delete(&user)
	token := "compose-smoke-test-session"
	session := model.Session{Base: model.Base{ID: id()}, TokenHash: hash(token), Phone: user.Phone, ExpiresAt: time.Now().Add(time.Hour)}
	if e = db.Create(&session).Error; e != nil {
		t.Fatal(e)
	}
	defer db.Delete(&session)
	n := model.Node{Base: model.Base{ID: id()}, Name: "compose-smoke-" + id(), Type: "local", DeployRoot: t.TempDir(), AllowedMountRoots: "/srv/allowed", Status: "ready"}
	if e = db.Create(&n).Error; e != nil {
		t.Fatal(e)
	}
	defer db.Delete(&n)
	h := New(config.Config{}, db, box)
	server := httptest.NewServer(h)
	defer server.Close()
	request := func(method, path string, body any, want int) []byte {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			if e := json.NewEncoder(&buf).Encode(body); e != nil {
				t.Fatal(e)
			}
		}
		req, e := http.NewRequest(method, server.URL+path, &buf)
		if e != nil {
			t.Fatal(e)
		}
		req.AddCookie(&http.Cookie{Name: "paas_session", Value: token})
		req.Header.Set("Content-Type", "application/json")
		resp, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, resp.StatusCode, want, raw)
		}
		return raw
	}
	taskID := func(raw []byte) string {
		t.Helper()
		var out map[string]any
		if e := json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		id, _ := out["task_id"].(string)
		if id == "" {
			t.Fatalf("missing task id: %s", raw)
		}
		return id
	}
	wait := func(task string, want string) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) {
			var job model.Task
			raw := request("GET", "/api/tasks/"+task, nil, 200)
			_ = json.Unmarshal(raw, &job)
			if job.Status == "succeeded" || job.Status == "failed" {
				if job.Status != want {
					t.Fatalf("task %s: %s %s\n%s", task, job.Status, job.Error, job.Logs)
				}
				return
			}
			time.Sleep(300 * time.Millisecond)
		}
		t.Fatal("task timed out")
	}
	input := composeRequest{Name: "smoke-" + id()[:8], NodeID: n.ID, ComposeInput: deploy.ComposeInput{YAML: `services:
  worker:
    image: alpine:3.22
    init: true
    pull_policy: missing
    command: [sleep, '3600']
    environment:
      MARKER: ${MARKER}
    volumes: [data:/data]
volumes:
  data:
`, Environment: map[string]string{"MARKER": "initial-secret-value"}}}
	if image := os.Getenv("PAAS_COMPOSE_TEST_IMAGE"); image != "" {
		input.YAML = strings.ReplaceAll(input.YAML, "alpine:3.22", image)
	}
	if registry := os.Getenv("PAAS_COMPOSE_TEST_REGISTRY"); registry != "" {
		input.Registries = []deploy.ComposeRegistry{{Server: registry, Username: os.Getenv("PAAS_COMPOSE_TEST_REGISTRY_USER"), Password: os.Getenv("PAAS_COMPOSE_TEST_REGISTRY_PASSWORD")}}
	}
	raw := request("POST", "/api/compose/apps", input, 202)
	var created struct {
		App model.App `json:"app"`
	}
	_ = json.Unmarshal(raw, &created)
	a := created.App
	runner := &deploy.Runner{Box: box}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = runner.ComposeAction(ctx, a, n, "delete", "")
		vols, _ := exec.CommandContext(ctx, "docker", "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+deploy.ComposeProject(a)).Output()
		for _, v := range strings.Fields(string(vols)) {
			_ = exec.CommandContext(ctx, "docker", "volume", "rm", v).Run()
		}
		db.Where("app_id = ?", a.ID).Delete(&model.ComposeVersion{})
		db.Where("resource_id = ?", a.ID).Delete(&model.Task{})
		db.Delete(&a)
	}()
	request("POST", "/api/apps/"+a.ID+"/compose/actions", map[string]string{"action": "stop"}, 409)
	wait(taskID(raw), "succeeded")
	// Generic GitHub mutations must not accidentally operate on a Compose app.
	request("PUT", "/api/apps/"+a.ID, map[string]any{}, 409)
	request("DELETE", "/api/apps/"+a.ID, nil, 409)
	request("GET", "/api/apps/"+a.ID+"/compose", nil, 200)
	public := request("GET", "/api/apps/"+a.ID, nil, 200)
	if strings.Contains(string(public), "initial-secret-value") {
		t.Fatal("public app endpoint leaked configuration")
	}
	var stored model.App
	if e = db.First(&stored, "id = ?", a.ID).Error; e != nil {
		t.Fatal(e)
	}
	if stored.ComposeActiveVersionID == "" || strings.Contains(stored.ComposeDraft, "initial-secret-value") {
		t.Fatal("active version or encryption missing")
	}
	proposed := input.ComposeInput
	proposed.YAML = strings.Replace(proposed.YAML, "worker:", "replacement:", 1)
	previewRaw := request("POST", "/api/compose/validate", composeRequest{AppID: a.ID, NodeID: n.ID, ComposeInput: proposed}, 200)
	var preview deploy.ComposePreview
	if e = json.Unmarshal(previewRaw, &preview); e != nil {
		t.Fatal(e)
	}
	if len(preview.Removed) != 1 || preview.Removed[0] != "worker" || len(preview.Added) != 1 || preview.Added[0] != "replacement" {
		t.Fatalf("incorrect configuration change preview: %+v", preview)
	}
	draft := input.ComposeInput
	draft.Environment = map[string]string{"MARKER": "draft-secret-value"}
	request("PUT", "/api/apps/"+a.ID+"/compose", draft, 200)
	// Updating images must use the active source, even while a different draft exists.
	imageTask := request("POST", "/api/apps/"+a.ID+"/compose/actions", map[string]string{"action": "update_images"}, 202)
	wait(taskID(imageTask), "succeeded")
	var list []deploy.ComposeContainer
	_ = json.Unmarshal(request("GET", "/api/apps/"+a.ID+"/containers", nil, 200), &list)
	if len(list) != 1 {
		t.Fatalf("wrong container count %d", len(list))
	}
	value, e := exec.Command("docker", "exec", list[0].ID, "printenv", "MARKER").Output()
	if e != nil || strings.TrimSpace(string(value)) != "initial-secret-value" {
		t.Fatalf("image update applied draft: %q %v", value, e)
	}
	// The deployment request carries an immutable snapshot, independent of later drafts.
	deployTask := request("POST", "/api/apps/"+a.ID+"/compose/actions", map[string]any{"action": "deploy", "config": draft}, 202)
	later := input.ComposeInput
	later.Environment = map[string]string{"MARKER": "later-draft-value"}
	request("PUT", "/api/apps/"+a.ID+"/compose", later, 200)
	wait(taskID(deployTask), "succeeded")
	_ = json.Unmarshal(request("GET", "/api/apps/"+a.ID+"/containers", nil, 200), &list)
	value, e = exec.Command("docker", "exec", list[0].ID, "printenv", "MARKER").Output()
	if e != nil || strings.TrimSpace(string(value)) != "draft-secret-value" {
		t.Fatalf("running task did not retain its snapshot: %q %v", value, e)
	}
	tasks := request("GET", "/api/apps/"+a.ID+"/compose/tasks", nil, 200)
	for _, s := range []string{"initial-secret-value", "draft-secret-value", "later-draft-value"} {
		if strings.Contains(string(tasks), s) {
			t.Fatal("task logs leaked a configuration value")
		}
	}
	// A second Compose app must coexist despite the legacy unique repository index.
	invalid := input
	invalid.Name = "failed-" + id()[:8]
	invalid.YAML = `services: {web: {image: '${MISSING:?required}'}}`
	failedRaw := request("POST", "/api/compose/apps", invalid, 202)
	var failed struct {
		App model.App `json:"app"`
	}
	_ = json.Unmarshal(failedRaw, &failed)
	wait(taskID(failedRaw), "failed")
	deleteFailed := request("POST", "/api/apps/"+failed.App.ID+"/compose/actions", map[string]string{"action": "delete"}, 202)
	wait(taskID(deleteFailed), "succeeded")
	deleted := request("POST", "/api/apps/"+a.ID+"/compose/actions", map[string]string{"action": "delete"}, 202)
	wait(taskID(deleted), "succeeded")
	request("GET", "/api/apps/"+a.ID, nil, 404)
	// Recovery settles abandoned tasks instead of silently replaying mutations.
	abandoned := model.Task{Base: model.Base{ID: id()}, Kind: "compose:restart", ResourceID: a.ID, Status: "running"}
	db.Create(&abandoned)
	defer db.Delete(&abandoned)
	recovery := &Server{db: db}
	recovery.recoverComposeTasks()
	if e = db.First(&abandoned, "id = ?", abandoned.ID).Error; e != nil || abandoned.Status != "failed" {
		t.Fatalf("recovery failed: %v %#v", e, abandoned)
	}
	fmt.Println("Compose API verified: creation, uniqueness, serialized tasks, immutable drafts, image update, encryption, deletion, restart recovery")
}
