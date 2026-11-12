package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paas.local/backend/internal/model"
)

func TestValidateComposeBoundaries(t *testing.T) {
	n := model.Node{AllowedMountRoots: "/srv/allowed"}
	cases := []struct {
		name, raw string
		ok        bool
	}{
		{"multiple services", `services:
  web:
    image: nginx:alpine
    depends_on: [redis]
    volumes: [data:/data]
  redis:
    image: redis:alpine
volumes:
  data:
`, true},
		{"build", `services: {web: {image: nginx, build: .}}`, false},
		{"include", `include: [other.yaml]
services: {web: {image: nginx}}`, false},
		{"env file", `services: {web: {image: nginx, env_file: /etc/passwd}}`, false},
		{"custom name", `services: {web: {image: nginx, container_name: shared}}`, false},
		{"unsafe mount", `services: {web: {image: nginx, volumes: ["/etc:/data"]}}`, false},
		{"traversal mount", `services: {web: {image: nginx, volumes: ["/srv/allowed/../../etc:/data"]}}`, false},
		{"safe mount", `services: {web: {image: nginx, volumes: ["/srv/allowed/web:/data:ro"]}}`, true},
		{"relative mount", `services: {web: {image: nginx, volumes: ["./data:/data"]}}`, false},
		{"driver mount bypass", `services: {web: {image: nginx}}
volumes: {data: {driver_opts: {device: /etc, type: none, o: bind}}}`, false},
		{"resource collision", `services: {web: {image: nginx}}
volumes: {data: {name: paas-compose-another-project_data}}`, false},
		{"external network", `services: {web: {image: nginx, networks: [shared]}}
networks: {shared: {external: true, name: shared}}`, true},
		{"reserved labels", `services: {web: {image: nginx, labels: ["com.docker.compose.project=other"]}}`, false},
		{"file hook", `services: {web: {image: nginx, post_start: [{command: evil}]}}`, false},
		{"multiple documents", `services: {web: {image: nginx}}
---
services: {other: {image: redis}}`, false},
		{"duplicate service", `services:
  web: {image: nginx}
  web: {image: redis}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, e := ValidateCompose(c.raw, n)
			if (e == nil) != c.ok {
				t.Fatalf("expected valid=%v, got %v", c.ok, e)
			}
		})
	}
	// Even a literal dollar in a resolved path must not bypass the allowlist.
	if _, e := validateCompose(`services: {web: {image: nginx, volumes: [{type: bind, source: '/etc/$literal', target: /data}]}}`, n, true); e == nil {
		t.Fatal("resolved dollar bypassed mount allowlist")
	}
}
func TestComposeEnvironmentAndQuoting(t *testing.T) {
	for _, key := range []string{"COMPOSE_FILE", "DOCKER_HOST", "PATH", "bad key", "$(touch /tmp/no)"} {
		if ValidateComposeEnvironment(ComposeInput{Environment: map[string]string{key: "test"}}) == nil {
			t.Fatalf("accepted %q", key)
		}
	}
	input := map[string]string{"PASSWORD": "' $(touch /tmp/never) $HOME `whoami`\\\n中文"}
	if e := ValidateComposeEnvironment(ComposeInput{Environment: input}); e != nil {
		t.Fatal(e)
	}
	a := model.App{Base: model.Base{ID: "test"}}
	cmd := composeCLI(a, model.Node{DeployRoot: "/safe path"}, "source.yaml", input)
	if !strings.Contains(cmd, "env -i ") || !strings.Contains(cmd, shell("PASSWORD="+input["PASSWORD"])) {
		t.Fatal("missing environment isolation or shell quoting")
	}
}

func requireComposeCLI(t *testing.T) {
	t.Helper()
	if _, e := exec.LookPath("docker"); e != nil {
		t.Skip("Docker CLI unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := exec.CommandContext(ctx, "docker", "compose", "version").Run(); e != nil {
		t.Skip("Compose plugin unavailable")
	}
}

// This test exercises the real Compose parser without needing a running daemon.
func TestComposeValidateCLI(t *testing.T) {
	requireComposeCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a := model.App{Base: model.Base{ID: "unit-roundtrip"}}
	n := model.Node{Type: "local", DeployRoot: t.TempDir(), AllowedMountRoots: "/srv/allowed"}
	r := &Runner{}
	t.Setenv("PAAS_TEST_SECRET_FROM_HOST", "must-not-leak")
	password := "quote' dollar$literal backslash\\\n中文"
	in := ComposeInput{YAML: `version: '3.8'
services:
  web:
    image: nginx:alpine
    command: [sh, -c, 'echo $$HOME']
    environment:
      PASSWORD: ${PASSWORD}
      FROM_HOST: ${PAAS_TEST_SECRET_FROM_HOST:-safe}
    volumes:
      - data:/data
volumes:
  data:
`, Environment: map[string]string{"PASSWORD": password}}
	canonical, p, e := r.ComposeValidate(ctx, a, n, in)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Services) != 1 || p.Services[0].Name != "web" {
		t.Fatalf("bad preview: %+v", p)
	}
	if strings.Contains(canonical, "must-not-leak") || strings.Contains(canonical, "-validation") {
		t.Fatalf("leaked host environment or validation project: %s", canonical)
	}
	if _, e = os.Stat(composeDir(model.App{Base: model.Base{ID: a.ID + "-validation"}}, n)); !os.IsNotExist(e) {
		t.Fatal("validation directory left behind")
	}
	// A second parse must preserve escaped dollars, quotes, multiline values,
	// and shell commands without expanding host environment variables.
	var doc map[string]any
	if e = json.Unmarshal([]byte(canonical), &doc); e != nil {
		t.Fatal(e)
	}
	env := doc["services"].(map[string]any)["web"].(map[string]any)["environment"].(map[string]any)
	if env["FROM_HOST"] != "safe" || env["PASSWORD"] != strings.ReplaceAll(password, "$", "$$") {
		t.Fatalf("environment not preserved: %#v", env)
	}
	x := localRunner{}
	if e = prepareComposeDirectory(ctx, x, a, n); e != nil {
		t.Fatal(e)
	}
	if e = writeComposeFile(ctx, x, filepath.Join(composeDir(a, n), "roundtrip.json"), canonical); e != nil {
		t.Fatal(e)
	}
	out, e := x.Run(ctx, composeCLI(a, n, "roundtrip.json")+" config --format json 2>/dev/null", "")
	if e != nil {
		t.Fatal(e)
	}
	var again map[string]any
	if e = json.Unmarshal([]byte(out), &again); e != nil {
		t.Fatal(e)
	}
	againEnv := again["services"].(map[string]any)["web"].(map[string]any)["environment"].(map[string]any)
	if againEnv["PASSWORD"] != env["PASSWORD"] {
		t.Fatal("password changed after canonical roundtrip")
	}
	// Interpolated mounts get validated after expansion, not just in the source YAML.
	in = ComposeInput{YAML: `services: {web: {image: nginx, volumes: ['${MOUNT}:/data']}}`, Environment: map[string]string{"MOUNT": "/etc"}}
	if _, _, e = r.ComposeValidate(ctx, a, n, in); e == nil {
		t.Fatal("interpolation bypassed mount policy")
	}
}
func TestComposeDeleteBeforeFirstDeployment(t *testing.T) {
	requireComposeCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e := exec.CommandContext(ctx, "docker", "info").Run(); e != nil {
		t.Skip("Docker daemon unavailable")
	}
	a := model.App{Base: model.Base{ID: "unit-delete-" + strings.ReplaceAll(t.Name(), "_", "-")}}
	n := model.Node{Type: "local", DeployRoot: t.TempDir()}
	if e := os.MkdirAll(composeDir(a, n), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := (&Runner{}).ComposeAction(ctx, a, n, "delete", ""); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(composeDir(a, n)); !os.IsNotExist(e) {
		t.Fatal("failed initial deployment left files")
	}
}

// Opt-in because this test pulls an image and creates temporary Docker resources.
func TestComposeLifecycleIntegration(t *testing.T) {
	if os.Getenv("PAAS_COMPOSE_INTEGRATION") != "1" {
		t.Skip("set PAAS_COMPOSE_INTEGRATION=1 for container lifecycle smoke test")
	}
	requireComposeCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if e := exec.CommandContext(ctx, "docker", "info").Run(); e != nil {
		t.Fatal("Docker daemon unavailable")
	}
	a := model.App{Base: model.Base{ID: uuid.NewString()}}
	n := model.Node{Type: "local", DeployRoot: t.TempDir()}
	r := &Runner{}
	var stages []string
	r.OnProgress = func(s string) { stages = append(stages, s) }
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_, _ = r.ComposeAction(cleanup, a, n, "delete", "")
		raw, _ := exec.CommandContext(cleanup, "docker", "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+ComposeProject(a)).Output()
		for _, v := range strings.Fields(string(raw)) {
			_ = exec.CommandContext(cleanup, "docker", "volume", "rm", v).Run()
		}
	}()
	input := ComposeInput{YAML: `services:
  worker:
    image: alpine:3.22
    init: true
    command: [sh, -c, 'echo smoke-ready; touch /data/ready; exec sleep 3600']
    environment:
      TEST_VALUE: ${TEST_VALUE}
    volumes: [data:/data]
    healthcheck:
      test: [CMD, test, -f, /data/ready]
      interval: 1s
      timeout: 1s
      retries: 10
  companion:
    image: alpine:3.22
    init: true
    command: [sleep, '3600']
volumes:
  data:
`, Environment: map[string]string{"TEST_VALUE": "quote' dollar$literal backslash\\"}}
	if image := os.Getenv("PAAS_COMPOSE_TEST_IMAGE"); image != "" {
		input.YAML = strings.ReplaceAll(input.YAML, "alpine:3.22", image)
	}
	if registry := os.Getenv("PAAS_COMPOSE_TEST_REGISTRY"); registry != "" {
		input.Registries = []ComposeRegistry{{Server: registry, Username: os.Getenv("PAAS_COMPOSE_TEST_REGISTRY_USER"), Password: os.Getenv("PAAS_COMPOSE_TEST_REGISTRY_PASSWORD")}}
	}
	canonical, _, e := r.ComposeValidate(ctx, a, n, input)
	if e != nil {
		t.Fatal(e)
	}
	logs, pinned, e := r.ComposeApply(ctx, a, n, canonical, input.Registries)
	if e != nil {
		t.Fatalf("apply: %v\n%s", e, logs)
	}
	if !strings.Contains(pinned, "@sha256:") {
		t.Fatal("images were not pinned")
	}
	list, e := r.ComposeContainers(ctx, a, n)
	if e != nil || len(list) != 2 {
		t.Fatalf("containers: %v %#v", e, list)
	}
	var workerID string
	for _, c := range list {
		if !c.State.Running {
			t.Fatalf("container not running: %#v", c)
		}
		if c.Service == "worker" {
			workerID = c.ID
		}
	}
	envValue, e := exec.CommandContext(ctx, "docker", "exec", workerID, "printenv", "TEST_VALUE").Output()
	if e != nil || strings.TrimSpace(string(envValue)) != input.Environment["TEST_VALUE"] {
		t.Fatalf("environment did not survive deployment: %q, %v", envValue, e)
	}
	// Service-level stop leaves its sibling running, and start recovers health.
	if _, e = r.ComposeAction(ctx, a, n, "stop", "worker"); e != nil {
		t.Fatal(e)
	}
	list, e = r.ComposeContainers(ctx, a, n)
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range list {
		if c.Service == "worker" && c.State.Running {
			t.Fatal("worker still running")
		}
		if c.Service == "companion" && !c.State.Running {
			t.Fatal("service stop affected sibling")
		}
	}
	if _, e = r.ComposeAction(ctx, a, n, "start", "worker"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.ComposeAction(ctx, a, n, "restart", "worker"); e != nil {
		t.Fatal(e)
	}
	// Capture the aggregate log stream, then cancel to verify clean shutdown.
	logCtx, stopLogs := context.WithCancel(ctx)
	var buffer bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- r.ComposeLogs(logCtx, a, n, "", 50, &buffer) }()
	time.AfterFunc(2*time.Second, stopLogs)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("log stream did not cancel")
	}
	if !strings.Contains(buffer.String(), "smoke-ready") {
		t.Fatalf("missing log output: %s", buffer.String())
	}
	if _, e = r.ComposeAction(ctx, a, n, "rebuild", "worker"); e != nil {
		t.Fatal(e)
	}
	list, e = r.ComposeContainers(ctx, a, n)
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range list {
		if c.Service == "worker" && c.ID == workerID {
			t.Fatal("rebuild retained container ID")
		}
	}
	// Change config and remove a service, then restore the immutable original.
	input.YAML = `services: {worker: {image: 'alpine:3.22', init: true, command: [sleep, '3600'], volumes: ['data:/data']}}
volumes: {data: {}}`
	if image := os.Getenv("PAAS_COMPOSE_TEST_IMAGE"); image != "" {
		input.YAML = strings.ReplaceAll(input.YAML, "alpine:3.22", image)
	}
	canonical, _, e = r.ComposeValidate(ctx, a, n, input)
	if e != nil {
		t.Fatal(e)
	}
	logs, _, e = r.ComposeApply(ctx, a, n, canonical, input.Registries)
	if e != nil {
		t.Fatalf("update: %v %s", e, logs)
	}
	list, e = r.ComposeContainers(ctx, a, n)
	if e != nil || len(list) != 1 {
		t.Fatalf("orphan not removed: %v %+v", e, list)
	}
	logs, e = r.ComposeRestore(ctx, a, n, pinned, input.Registries)
	if e != nil {
		t.Fatalf("restore: %v %s", e, logs)
	}
	list, e = r.ComposeContainers(ctx, a, n)
	if e != nil || len(list) != 2 {
		t.Fatalf("restore services: %v %+v", e, list)
	}
	for _, c := range list {
		if c.Service == "worker" {
			if e = exec.CommandContext(ctx, "docker", "exec", c.ID, "test", "-f", "/data/ready").Run(); e != nil {
				t.Fatal("persistent data lost")
			}
		}
	}
	if _, e = r.ComposeAction(ctx, a, n, "delete", ""); e != nil {
		t.Fatal(e)
	}
	list, e = r.ComposeContainers(ctx, a, n)
	if e != nil || len(list) != 0 {
		t.Fatalf("delete left containers: %v %+v", e, list)
	}
	volumes, e := exec.CommandContext(ctx, "docker", "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+ComposeProject(a)).Output()
	if e != nil || strings.TrimSpace(string(volumes)) == "" {
		t.Fatal("delete did not preserve named volume")
	}
	if len(stages) < 4 {
		t.Fatal("deployment progress was not reported")
	}
}

func TestSelectImageDigest(t *testing.T) {
	hash := "sha256:" + strings.Repeat("a", 64)
	for _, image := range []string{"alpine:3.22", "docker.io/alpine:3.22", "docker.io/library/alpine:3.22", "index.docker.io/library/alpine:3.22"} {
		got, e := selectImageDigest(image, []string{"other/alpine@" + hash, "alpine@" + hash})
		if e != nil || got != "alpine@"+hash {
			t.Fatalf("wrong digest for %s: %s %v", image, got, e)
		}
	}
	if got, e := selectImageDigest("localhost:5000/team/app:latest", []string{"localhost:5000/team/app@" + hash}); e != nil || got == "" {
		t.Fatal("registry port was treated as an image tag")
	}
	if _, e := selectImageDigest("alpine:3.22", []string{"other/alpine@" + hash}); e == nil {
		t.Fatal("accepted digest from a different repository")
	}
	if _, e := selectImageDigest("alpine:3.22", []string{"alpine@sha256:invalid"}); e == nil {
		t.Fatal("accepted malformed digest")
	}
}

func TestLocalRunnerCancelsPluginProcessTree(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if stream {
					done <- (localRunner{}).Stream(ctx, "sleep 30 & wait", "", io.Discard)
				} else {
					_, e := (localRunner{}).Run(ctx, "sleep 30 & wait", "")
					done <- e
				}
			}()
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("expected cancellation error")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancellation left a child holding the command output pipe")
			}
		})
	}
}

func TestComposeActionCLICompatibility(t *testing.T) {
	requireComposeCLI(t)
	for _, action := range []string{"start", "stop", "restart", "rebuild"} {
		for _, service := range []string{"", "worker"} {
			t.Run(action+"/"+service, func(t *testing.T) {
				args, err := composeActionArgs(action, service)
				if err != nil {
					t.Fatal(err)
				}
				if action == "start" && (!strings.HasPrefix(args, "up ") || !strings.Contains(args, "--no-recreate") || !strings.Contains(args, "--wait-timeout 120")) {
					t.Fatalf("start must wait without recreating existing containers: %s", args)
				}
				if service != "" && !strings.Contains(args, " "+shell(service)) {
					t.Fatalf("service scope lost: %s", args)
				}
				if service != "" && action != "stop" && !strings.Contains(args, "--no-deps") {
					t.Fatalf("service operation must not start dependencies: %s", args)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				// Cobra validates flags before printing help; no daemon or containers needed.
				out, err := exec.CommandContext(ctx, "sh", "-c", "docker compose "+args+" --help").CombinedOutput()
				if err != nil {
					t.Fatalf("unsupported command %s: %v: %s", args, err, out)
				}
			})
		}
	}
	for _, service := range []string{"worker; touch /tmp/no", "--all"} {
		if _, err := composeActionArgs("start", service); err == nil {
			t.Fatalf("accepted unsafe service %q", service)
		}
	}
}
