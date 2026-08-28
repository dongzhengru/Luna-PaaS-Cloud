package deploy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"paas.local/backend/internal/model"
	"paas.local/backend/internal/secure"
)

type Runner struct {
	DB                                       *gorm.DB
	Box                                      *secure.Box
	Registry, RegistryUser, RegistryPassword string
}
type commandRunner interface {
	Run(context.Context, string, string) (string, error)
	Stream(context.Context, string, string, io.Writer) error
	Close() error
}
type localRunner struct{}

func (localRunner) Run(ctx context.Context, cmd, input string) (string, error) {
	c := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	c.Stdin = strings.NewReader(input)
	b, e := c.CombinedOutput()
	if e != nil {
		return string(b), fmt.Errorf("%w: %s", e, b)
	}
	return string(b), nil
}
func (localRunner) Stream(ctx context.Context, cmd, input string, output io.Writer) error {
	c := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	c.Stdin = strings.NewReader(input)
	w := &lockedWriter{Writer: output}
	c.Stdout = w
	c.Stderr = w
	return c.Run()
}
func (localRunner) Close() error { return nil }

type sshRunner struct{ c *ssh.Client }

func (s sshRunner) Run(ctx context.Context, cmd, input string) (string, error) {
	sess, e := s.c.NewSession()
	if e != nil {
		return "", e
	}
	defer sess.Close()
	sess.Stdin = strings.NewReader(input)
	var b bytes.Buffer
	sess.Stdout = &b
	sess.Stderr = &b
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return b.String(), ctx.Err()
	case e := <-done:
		if e != nil {
			return b.String(), fmt.Errorf("%w: %s", e, b.String())
		}
		return b.String(), nil
	}
}
func (s sshRunner) Stream(ctx context.Context, cmd, input string, output io.Writer) error {
	sess, e := s.c.NewSession()
	if e != nil {
		return e
	}
	defer sess.Close()
	sess.Stdin = strings.NewReader(input)
	w := &lockedWriter{Writer: output}
	sess.Stdout = w
	sess.Stderr = w
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return ctx.Err()
	case e := <-done:
		return e
	}
}
func (s sshRunner) Close() error { return s.c.Close() }

type lockedWriter struct {
	sync.Mutex
	io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	return w.Writer.Write(p)
}

func (r *Runner) connect(n model.Node) (commandRunner, error) {
	if n.Type == "local" {
		return localRunner{}, nil
	}
	var auth ssh.AuthMethod
	switch n.AuthType {
	case "password":
		p, e := r.Box.Open(n.EncryptedPassword)
		if e != nil {
			return nil, e
		}
		auth = ssh.Password(p)
	case "private_key":
		k, e := r.Box.Open(n.EncryptedPrivateKey)
		if e != nil {
			return nil, e
		}
		var signer ssh.Signer
		if n.EncryptedPassphrase != "" {
			p, e := r.Box.Open(n.EncryptedPassphrase)
			if e != nil {
				return nil, e
			}
			signer, e = ssh.ParsePrivateKeyWithPassphrase([]byte(k), []byte(p))
			if e != nil {
				return nil, e
			}
		} else {
			signer, e = ssh.ParsePrivateKey([]byte(k))
			if e != nil {
				return nil, e
			}
		}
		auth = ssh.PublicKeys(signer)
	default:
		return nil, fmt.Errorf("unsupported ssh auth type")
	}
	callback := func(host string, remote net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		if n.HostKey == "" {
			return fmt.Errorf("host key is not pinned; observed %s", fp)
		}
		if n.HostKey != fp {
			return fmt.Errorf("host key mismatch: observed %s", fp)
		}
		return nil
	}
	c, e := ssh.Dial(
		"tcp",
		net.JoinHostPort(n.Host, strconv.Itoa(n.Port)),
		&ssh.ClientConfig{
			User:            n.Username,
			Auth:            []ssh.AuthMethod{auth},
			HostKeyCallback: callback,
			Timeout:         10 * time.Second,
		},
	)
	if e != nil {
		return nil, e
	}
	return sshRunner{c}, nil
}

func (r *Runner) Test(ctx context.Context, n model.Node) (string, error) {
	x, e := r.connect(n)
	if e != nil {
		return "", e
	}
	defer x.Close()
	root := shell(n.DeployRoot)
	return x.Run(
		ctx,
		"docker version --format '{{.Server.Version}}' && docker compose version && mkdir -p "+root+" && test -w "+root,
		"",
	)
}

// ContainerStats is a point-in-time view of an application's Docker container.
// Docker's display values are retained so the API reflects the daemon's units.
type ContainerStats struct {
	ContainerName string `json:"container_name"`
	Status        string `json:"status"`
	Health        string `json:"health,omitempty"`
	Running       bool   `json:"running"`
	OOMKilled     bool   `json:"oom_killed"`
	ExitCode      int    `json:"exit_code"`
	RestartCount  int    `json:"restart_count"`
	StartedAt     string `json:"started_at,omitempty"`
	CPUPercent    string `json:"cpu_percent,omitempty"`
	MemoryUsage   string `json:"memory_usage,omitempty"`
	MemoryPercent string `json:"memory_percent,omitempty"`
	NetworkIO     string `json:"network_io,omitempty"`
	BlockIO       string `json:"block_io,omitempty"`
	PIDs          string `json:"pids,omitempty"`
}

type dockerState struct {
	Status    string `json:"Status"`
	Running   bool   `json:"Running"`
	OOMKilled bool   `json:"OOMKilled"`
	ExitCode  int    `json:"ExitCode"`
	StartedAt string `json:"StartedAt"`
	Health    *struct {
		Status string `json:"Status"`
	} `json:"Health"`
}

type dockerStats struct {
	CPUPercent    string `json:"CPUPerc"`
	MemoryUsage   string `json:"MemUsage"`
	MemoryPercent string `json:"MemPerc"`
	NetworkIO     string `json:"NetIO"`
	BlockIO       string `json:"BlockIO"`
	PIDs          string `json:"PIDs"`
}

func parseContainerStats(name, stateRaw, restartRaw, statsRaw string) (ContainerStats, error) {
	var state dockerState
	if e := json.Unmarshal([]byte(strings.TrimSpace(stateRaw)), &state); e != nil {
		return ContainerStats{}, fmt.Errorf("parse container state: %w", e)
	}
	restarts, e := strconv.Atoi(strings.TrimSpace(restartRaw))
	if e != nil {
		return ContainerStats{}, fmt.Errorf("parse restart count: %w", e)
	}
	result := ContainerStats{
		ContainerName: name,
		Status:        state.Status,
		Running:       state.Running,
		OOMKilled:     state.OOMKilled,
		ExitCode:      state.ExitCode,
		RestartCount:  restarts,
		StartedAt:     state.StartedAt,
	}
	if state.Health != nil {
		result.Health = state.Health.Status
	}
	if strings.TrimSpace(statsRaw) != "" {
		var stats dockerStats
		if e := json.Unmarshal([]byte(strings.TrimSpace(statsRaw)), &stats); e != nil {
			return ContainerStats{}, fmt.Errorf("parse container resources: %w", e)
		}
		result.CPUPercent = stats.CPUPercent
		result.MemoryUsage = stats.MemoryUsage
		result.MemoryPercent = stats.MemoryPercent
		result.NetworkIO = stats.NetworkIO
		result.BlockIO = stats.BlockIO
		result.PIDs = stats.PIDs
	}
	return result, nil
}

func (r *Runner) Stats(ctx context.Context, a model.App, n model.Node) (ContainerStats, error) {
	x, e := r.connect(n)
	if e != nil {
		return ContainerStats{}, e
	}
	defer x.Close()
	state, e := x.Run(ctx, "docker inspect --format '{{json .State}}' "+shell(a.Slug), "")
	if e != nil {
		return ContainerStats{}, e
	}
	restarts, e := x.Run(ctx, "docker inspect --format '{{.RestartCount}}' "+shell(a.Slug), "")
	if e != nil {
		return ContainerStats{}, e
	}
	resources, statsErr := x.Run(
		ctx,
		"docker stats --no-stream --format '{{json .}}' "+shell(a.Slug),
		"",
	)
	if statsErr != nil {
		// Stopped containers still have useful inspect state but no live resources.
		resources = ""
	}
	return parseContainerStats(a.Slug, state, restarts, resources)
}

func (r *Runner) StreamLogs(
	ctx context.Context,
	a model.App,
	n model.Node,
	tail int,
	output io.Writer,
) error {
	x, e := r.connect(n)
	if e != nil {
		return e
	}
	defer x.Close()
	if tail < 1 {
		tail = 300
	}
	if tail > 2000 {
		tail = 2000
	}
	cmd := "exec docker logs --tail " + strconv.Itoa(
		tail,
	) + " --timestamps --follow " + shell(
		a.Slug,
	)
	return x.Stream(ctx, cmd, "", output)
}

type snapshot struct {
	Image                   string                 `json:"image"`
	Env                     []model.EnvironmentVar `json:"env"`
	Volumes                 []model.Volume         `json:"volumes"`
	Health                  model.HealthCheck      `json:"health"`
	HostPort, ContainerPort int
	Restart                 string
}

func (r *Runner) Deploy(
	ctx context.Context,
	a model.App,
	n model.Node,
	image string,
) (string, string, error) {
	var env []model.EnvironmentVar
	var vols []model.Volume
	var health model.HealthCheck
	if e := json.Unmarshal([]byte(a.EnvironmentJSON), &env); e != nil {
		return "", "", e
	}
	_ = json.Unmarshal([]byte(a.VolumesJSON), &vols)
	_ = json.Unmarshal([]byte(a.HealthJSON), &health)
	for i := range env {
		if env[i].Secret && strings.HasPrefix(env[i].Value, "enc:") {
			v, e := r.Box.Open(strings.TrimPrefix(env[i].Value, "enc:"))
			if e != nil {
				return "", "", e
			}
			env[i].Value = v
		}
	}
	snap := snapshot{
		Image:         image,
		Env:           env,
		Volumes:       vols,
		Health:        health,
		HostPort:      a.HostPort,
		ContainerPort: a.ContainerPort,
		Restart:       a.RestartPolicy,
	}
	raw, _ := json.Marshal(snap)
	sealed, e := r.Box.Seal(string(raw))
	if e != nil {
		return "", "", e
	}
	logs, e := r.apply(ctx, a, n, snap)
	return logs, sealed, e
}

func (r *Runner) Restore(
	ctx context.Context,
	a model.App,
	n model.Node,
	raw string,
) (string, error) {
	var s snapshot
	plain, e := r.Box.Open(raw)
	if e != nil {
		return "", e
	}
	if e := json.Unmarshal([]byte(plain), &s); e != nil {
		return "", e
	}
	return r.apply(ctx, a, n, s)
}

// Remove stops the application's Compose project and removes its generated
// configuration. Named volumes are deliberately preserved for recovery.
func (r *Runner) Remove(ctx context.Context, a model.App, n model.Node) (string, error) {
	x, e := r.connect(n)
	if e != nil {
		return "", e
	}
	defer x.Close()
	dir := filepath.Join(n.DeployRoot, a.Slug)
	cmd := "if [ -f " + shell(
		filepath.Join(dir, "compose.yaml"),
	) + " ]; then cd " + shell(
		dir,
	) + " && docker compose down --remove-orphans; fi && rm -f " + shell(
		filepath.Join(dir, ".env"),
	) + " " + shell(
		filepath.Join(dir, "compose.yaml"),
	) + " && (rmdir " + shell(
		dir,
	) + " 2>/dev/null || true)"
	return x.Run(ctx, cmd, "")
}

func (r *Runner) apply(ctx context.Context, a model.App, n model.Node, s snapshot) (string, error) {
	x, e := r.connect(n)
	if e != nil {
		return "", e
	}
	defer x.Close()
	dir := filepath.Join(n.DeployRoot, a.Slug)
	type healthY struct {
		Test        []string `yaml:"test,omitempty"`
		Interval    string   `yaml:"interval,omitempty"`
		Timeout     string   `yaml:"timeout,omitempty"`
		Retries     int      `yaml:"retries,omitempty"`
		StartPeriod string   `yaml:"start_period,omitempty"`
	}
	type svc struct {
		Image         string   `yaml:"image"`
		ContainerName string   `yaml:"container_name"`
		Restart       string   `yaml:"restart,omitempty"`
		EnvFile       []string `yaml:"env_file,omitempty"`
		Ports         []string `yaml:"ports,omitempty"`
		Volumes       []string `yaml:"volumes,omitempty"`
		Health        healthY  `yaml:"healthcheck,omitempty"`
	}
	type compose struct {
		Services map[string]svc            `yaml:"services"`
		Volumes  map[string]map[string]any `yaml:"volumes,omitempty"`
	}
	ss := svc{
		Image:         s.Image,
		ContainerName: a.Slug,
		Restart:       s.Restart,
		EnvFile:       []string{".env"},
		Ports:         []string{fmt.Sprintf("127.0.0.1:%d:%d", s.HostPort, s.ContainerPort)},
	}
	named := map[string]map[string]any{}
	for _, v := range s.Volumes {
		mount := v.Source + ":" + v.Target
		if v.ReadOnly {
			mount += ":ro"
		}
		ss.Volumes = append(ss.Volumes, mount)
		if v.Type == "named" {
			named[v.Source] = map[string]any{}
		}
	}
	if s.Health.Command != "" {
		ss.Health = healthY{
			Test:        []string{"CMD-SHELL", s.Health.Command},
			Interval:    s.Health.Interval,
			Timeout:     s.Health.Timeout,
			Retries:     s.Health.Retries,
			StartPeriod: s.Health.StartPeriod,
		}
	}
	y, _ := yaml.Marshal(compose{
		Services: map[string]svc{"app": ss},
		Volumes:  named,
	})
	var envBuf strings.Builder
	for _, v := range s.Env {
		envBuf.WriteString(v.Key + "=" + escapeEnv(v.Value) + "\n")
	}
	write := func(name string, data []byte) error {
		cmd := "mkdir -p " + shell(
			dir,
		) + " && echo " + shell(
			base64.StdEncoding.EncodeToString(data),
		) + " | base64 -d > " + shell(
			filepath.Join(dir, name),
		) + " && chmod 600 " + shell(
			filepath.Join(dir, name),
		)
		_, e := x.Run(ctx, cmd, "")
		return e
	}
	if e = write("compose.yaml", y); e != nil {
		return "", e
	}
	if e = write(".env", []byte(envBuf.String())); e != nil {
		return "", e
	}
	out, e := x.Run(
		ctx,
		"docker login "+shell(r.Registry)+" --username "+shell(r.RegistryUser)+" --password-stdin",
		r.RegistryPassword+"\n",
	)
	logs := redact(out, r.RegistryPassword)
	if e != nil {
		return logs, e
	}
	out, e = x.Run(
		ctx,
		"cd "+shell(dir)+" && docker compose pull && docker compose up -d --remove-orphans",
		"",
	)
	logs += redact(out, r.RegistryPassword)
	if e != nil {
		return logs, e
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		out, e = x.Run(
			ctx,
			"docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "+shell(
				a.Slug,
			),
			"",
		)
		logs += out
		if e == nil &&
			(strings.Contains(out, "healthy") || strings.Contains(out, "running") && s.Health.Command == "") {
			return logs, nil
		}
		if strings.Contains(out, "unhealthy") || time.Now().After(deadline) {
			return logs, fmt.Errorf("container health check failed")
		}
		select {
		case <-ctx.Done():
			return logs, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
func shell(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func escapeEnv(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "\n", "\\n")
}
func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}
func ValidateVolumes(n model.Node, vs []model.Volume) error {
	roots := strings.Split(n.AllowedMountRoots, ",")
	for _, v := range vs {
		if v.Target == "" || !strings.HasPrefix(v.Target, "/") {
			return fmt.Errorf("volume target must be absolute")
		}
		if v.Type == "named" {
			if v.Source == "" || strings.ContainsAny(v.Source, "/ ") {
				return fmt.Errorf("invalid named volume")
			}
			continue
		}
		if v.Type != "bind" {
			return fmt.Errorf("unsupported volume type")
		}
		clean := filepath.Clean(v.Source)
		if clean == "/" || clean == "/var/run/docker.sock" {
			return fmt.Errorf("unsafe bind mount")
		}
		ok := false
		for _, root := range roots {
			root = filepath.Clean(strings.TrimSpace(root))
			if root != "." &&
				(clean == root || strings.HasPrefix(clean, root+string(filepath.Separator))) {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("bind mount outside allowed roots: %s", clean)
		}
	}
	return nil
}
