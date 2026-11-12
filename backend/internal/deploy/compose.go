package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"paas.local/backend/internal/model"
)

// ComposeInput is encrypted as a whole: YAML may contain inline credentials.
type ComposeRegistry struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}
type ComposeInput struct {
	YAML        string            `json:"yaml"`
	Environment map[string]string `json:"environment"`
	Registries  []ComposeRegistry `json:"registries,omitempty"`
}
type ComposeService struct {
	Name    string `json:"name"`
	Image   string `json:"image"`
	Ports   any    `json:"ports"`
	Volumes any    `json:"volumes"`
}
type ComposePreview struct {
	Services []ComposeService `json:"services"`
	Warnings []string         `json:"warnings"`
	Added    []string         `json:"added"`
	Removed  []string         `json:"removed"`
}

var composeNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateCompose checks both the source and the fully interpolated document.
// File-loading and resource naming are deliberately owned by the platform.
func ValidateCompose(raw string, n model.Node) (ComposePreview, error) {
	return validateCompose(raw, n, false)
}
func validateCompose(raw string, n model.Node, resolved bool) (ComposePreview, error) {
	p := ComposePreview{Services: []ComposeService{}, Warnings: []string{}}
	if len(raw) == 0 || len(raw) > 512*1024 {
		return p, fmt.Errorf("YAML 不能为空且不能超过 512 KiB")
	}
	var doc map[string]any
	dec := yaml.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return p, fmt.Errorf("YAML 格式错误: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return p, fmt.Errorf("仅支持单个 Compose 文档")
	}
	for key := range doc {
		if !allowed(key, "services volumes networks name version") && !strings.HasPrefix(key, "x-") {
			return p, fmt.Errorf("暂不支持顶层配置 %s（不支持外部文件、configs、secrets）", key)
		}
	}
	services, ok := doc["services"].(map[string]any)
	if !ok || len(services) == 0 || len(services) > 100 {
		return p, fmt.Errorf("services 必须包含 1–100 个服务")
	}
	for name, value := range services {
		if !composeNameRE.MatchString(name) {
			return p, fmt.Errorf("无效服务名 %s", name)
		}
		svc, ok := value.(map[string]any)
		if !ok {
			return p, fmt.Errorf("服务 %s 配置必须为对象", name)
		}
		for key := range svc {
			if !allowed(key, "image command entrypoint environment ports expose volumes networks depends_on restart healthcheck labels hostname domainname user working_dir read_only tmpfs init stop_signal stop_grace_period cpu_count cpu_percent cpu_period cpu_quota cpu_shares cpus cpuset mem_limit mem_reservation memswap_limit mem_swappiness pids_limit ulimits shm_size cap_add cap_drop security_opt privileged network_mode ipc pid extra_hosts dns dns_search dns_opt sysctls stdin_open tty platform pull_policy deploy logging mac_address group_add attach oom_kill_disable oom_score_adj") && !strings.HasPrefix(key, "x-") {
				return p, fmt.Errorf("服务 %s 暂不支持 %s（仅支持镜像部署，不支持构建、本地文件或自定义容器名）", name, key)
			}
		}
		image, _ := svc["image"].(string)
		if strings.TrimSpace(image) == "" {
			return p, fmt.Errorf("服务 %s 必须指定 image", name)
		}
		if svc["privileged"] == true || svc["network_mode"] == "host" || svc["pid"] == "host" {
			p.Warnings = append(p.Warnings, name+" 使用宿主机权限或宿主机命名空间")
		}
		// Reject paths in less visible volume options as well as in short syntax.
		if vs, ok := svc["volumes"].([]any); ok {
			for _, item := range vs {
				var v model.Volume
				switch x := item.(type) {
				case string:
					parts := strings.Split(x, ":")
					if len(parts) < 2 {
						return p, fmt.Errorf("服务 %s 请使用命名卷或绝对路径挂载", name)
					}
					v = model.Volume{Type: "named", Source: parts[0], Target: parts[1]}
					if strings.HasPrefix(v.Source, "/") || strings.HasPrefix(v.Source, ".") || strings.HasPrefix(v.Source, "~") {
						v.Type = "bind"
					}
				case map[string]any:
					v.Type, _ = x["type"].(string)
					v.Source, _ = x["source"].(string)
					v.Target, _ = x["target"].(string)
					if v.Type == "volume" {
						v.Type = "named"
					}
					if v.Type == "tmpfs" {
						continue
					}
				default:
					return p, fmt.Errorf("服务 %s 挂载格式无效", name)
				}
				// Raw variables are checked again after Compose interpolation.
				if resolved || !strings.Contains(v.Source, "$") {
					if v.Type == "bind" && !filepath.IsAbs(v.Source) {
						return p, fmt.Errorf("挂载源必须为绝对路径")
					}
					if err := ValidateVolumes(n, []model.Volume{v}); err != nil {
						return p, err
					}
				}
			}
		}
		if labels, ok := svc["labels"].(map[string]any); ok {
			for k := range labels {
				if strings.HasPrefix(k, "com.docker.compose.") {
					return p, fmt.Errorf("不允许覆盖 Compose 归属标签")
				}
			}
		}
		if labels, ok := svc["labels"].([]any); ok {
			for _, label := range labels {
				if strings.HasPrefix(fmt.Sprint(label), "com.docker.compose.") {
					return p, fmt.Errorf("不允许覆盖 Compose 归属标签")
				}
			}
		}
		if logging, ok := svc["logging"].(map[string]any); ok {
			if driver, _ := logging["driver"].(string); driver != "" && driver != "json-file" && driver != "local" && driver != "journald" && driver != "none" {
				return p, fmt.Errorf("服务 %s 暂不支持此日志驱动", name)
			}
		}
		if d, ok := svc["deploy"].(map[string]any); ok {
			for k := range d {
				if !allowed(k, "resources replicas restart_policy") {
					return p, fmt.Errorf("deploy.%s 不属于单节点部署支持范围", k)
				}
			}
		}
		p.Services = append(p.Services, ComposeService{Name: name, Image: image, Ports: svc["ports"], Volumes: svc["volumes"]})
	}
	for _, kind := range []string{"volumes", "networks"} {
		resources, _ := doc[kind].(map[string]any)
		for name, value := range resources {
			if !composeNameRE.MatchString(name) {
				return p, fmt.Errorf("无效资源名")
			}
			options, _ := value.(map[string]any)
			if labels, ok := options["labels"].(map[string]any); ok {
				for key := range labels {
					if strings.HasPrefix(key, "com.docker.compose.") {
						return p, fmt.Errorf("不允许覆盖资源归属标签")
					}
				}
			}
			if labels, ok := options["labels"].([]any); ok {
				for _, label := range labels {
					if strings.HasPrefix(fmt.Sprint(label), "com.docker.compose.") {
						return p, fmt.Errorf("不允许覆盖资源归属标签")
					}
				}
			}
			external, _ := options["external"].(bool)
			if _, ok := options["driver_opts"]; ok {
				return p, fmt.Errorf("不支持 %s.driver_opts，请使用节点挂载白名单", kind)
			}
			if resourceName, _ := options["name"].(string); resourceName != "" && !external {
				return p, fmt.Errorf("自定义资源名称仅限 external 资源")
			}
			if driver, _ := options["driver"].(string); driver != "" && driver != "local" && driver != "bridge" {
				return p, fmt.Errorf("暂不支持资源驱动 %s", driver)
			}
		}
	}
	sort.Slice(p.Services, func(i, j int) bool { return p.Services[i].Name < p.Services[j].Name })
	return p, nil
}
func allowed(s, list string) bool {
	for _, v := range strings.Fields(list) {
		if s == v {
			return true
		}
	}
	return false
}
func ComposeProject(a model.App) string { return "paas-compose-" + a.ID }
func composeDir(a model.App, n model.Node) string {
	return filepath.Join(n.DeployRoot, ComposeProject(a))
}
func composeCLI(a model.App, n model.Node, file string, environment ...map[string]string) string {
	dir := composeDir(a, n)
	assignments := ""
	if len(environment) > 0 {
		keys := make([]string, 0, len(environment[0]))
		for k := range environment[0] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			assignments += " " + shell(k+"="+environment[0][k])
		}
	}
	return "exec env -i PATH=\"$PATH\" DOCKER_HOST=\"$(cat " + shell(filepath.Join(dir, "docker-host")) + ")\"" + assignments + " docker --config " + shell(filepath.Join(dir, "registry")) + " compose --project-directory " + shell(dir) + " --env-file " + shell(filepath.Join(dir, "variables.env")) + " -p " + shell(ComposeProject(a)) + " -f " + shell(filepath.Join(dir, file))
}
func prepareComposeDirectory(ctx context.Context, x commandRunner, a model.App, n model.Node) error {
	dir := composeDir(a, n)
	plugins := filepath.Join(dir, "registry", "cli-plugins")
	// Keep interpolation isolated while retaining the node's selected Docker endpoint
	// and support plugins installed in the user's directory (Docker Desktop).
	cmd := "umask 077 && mkdir -p " + shell(plugins) + " && chmod 700 " + shell(dir) + " " + shell(filepath.Join(dir, "registry")) + " && docker context inspect --format '{{.Endpoints.docker.Host}}' > " + shell(filepath.Join(dir, "docker-host")) + " && if test -x \"$HOME/.docker/cli-plugins/docker-compose\"; then ln -sf \"$HOME/.docker/cli-plugins/docker-compose\" " + shell(filepath.Join(plugins, "docker-compose")) + "; elif command -v docker-compose >/dev/null 2>&1 && docker-compose docker-cli-plugin-metadata >/dev/null 2>&1; then ln -sf \"$(command -v docker-compose)\" " + shell(filepath.Join(plugins, "docker-compose")) + "; fi"
	if _, e := x.Run(ctx, cmd, ""); e != nil {
		return fmt.Errorf("无法初始化节点 Docker 执行环境")
	}
	return writeComposeFile(ctx, x, filepath.Join(dir, "variables.env"), "")
}
func writeComposeFile(ctx context.Context, x commandRunner, path, contents string) error {
	temporary := path + ".tmp"
	_, err := x.Run(ctx, "umask 077 && mkdir -p "+shell(filepath.Dir(path))+" && cat > "+shell(temporary)+" && chmod 600 "+shell(temporary)+" && mv -f "+shell(temporary)+" "+shell(path), contents)
	return err
}
func ValidateComposeEnvironment(in ComposeInput) error {
	if len(in.Registries) > 20 {
		return fmt.Errorf("私有仓库凭证不能超过 20 组")
	}
	seen := map[string]bool{}
	for _, registry := range in.Registries {
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.:-]*$`).MatchString(registry.Server) || registry.Username == "" || len(registry.Username) > 255 || registry.Password == "" || strings.ContainsAny(registry.Password, "\x00\r\n") || len(registry.Password) > 64*1024 {
			return fmt.Errorf("请填写有效的镜像仓库地址、用户名和密码（地址不含协议或路径）")
		}
		if seen[registry.Server] {
			return fmt.Errorf("镜像仓库地址重复: %s", registry.Server)
		}
		seen[registry.Server] = true
	}

	if len(in.Environment) > 500 {
		return fmt.Errorf("环境变量不能超过 500 个")
	}
	for k, v := range in.Environment {
		if !envNameRE.MatchString(k) || strings.HasPrefix(k, "COMPOSE_") || strings.HasPrefix(k, "DOCKER_") || k == "PATH" || strings.ContainsRune(v, 0) || len(v) > 64*1024 {
			return fmt.Errorf("无效、超长或保留的环境变量名: %s", k)
		}
	}
	return nil
}
func (r *Runner) ComposeValidate(ctx context.Context, a model.App, n model.Node, in ComposeInput) (string, ComposePreview, error) {
	r.progress("校验 Compose 配置")
	p, e := ValidateCompose(in.YAML, n)
	if e != nil {
		return "", p, e
	}
	e = ValidateComposeEnvironment(in)
	if e != nil {
		return "", p, e
	}
	x, e := r.connect(n)
	if e != nil {
		return "", p, e
	}
	defer x.Close()
	// Validation uses an isolated directory and never changes the live project's files.
	validation := a
	validation.ID = a.ID + "-validation"
	dir := composeDir(validation, n)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = x.Run(cleanup, "rm -rf "+shell(dir), "")
	}()
	if e = prepareComposeDirectory(ctx, x, validation, n); e != nil {
		return "", p, e
	}
	if e = writeComposeFile(ctx, x, filepath.Join(dir, "source.yaml"), in.YAML); e != nil {
		return "", p, fmt.Errorf("无法写入校验配置")
	}
	raw, e := x.Run(ctx, composeCLI(validation, n, "source.yaml", in.Environment)+" config --format json 2>"+shell(filepath.Join(dir, "diagnostics.txt")), "")
	diagnostics, _ := x.Run(ctx, "cat "+shell(filepath.Join(dir, "diagnostics.txt")), "")
	if strings.Contains(diagnostics, "variable is not set") {
		return "", p, fmt.Errorf("Compose 配置引用了未提供的环境变量，请补充变量或设置默认值")
	}

	if e != nil {
		return "", p, fmt.Errorf("Compose 配置校验失败，请检查变量、端口、依赖及节点 Compose 版本")
	}
	// The validation project name must never escape into the deployment resource names.
	var doc map[string]any
	if e = json.Unmarshal([]byte(raw), &doc); e != nil {
		return "", p, fmt.Errorf("节点返回无效的 Compose 配置")
	}
	delete(doc, "name")
	for _, kind := range []string{"volumes", "networks"} {
		resources, _ := doc[kind].(map[string]any)
		for _, value := range resources {
			if options, ok := value.(map[string]any); ok {
				if external, _ := options["external"].(bool); !external {
					delete(options, "name")
				}
			}
		}
	}
	normalized, _ := json.Marshal(doc)
	canonicalPreview, e := validateCompose(string(normalized), n, true)

	return string(normalized), canonicalPreview, e
}

// ComposeApply pins every image by digest before starting containers. The pinned
// document is returned for version history and reproducible recovery.
func (r *Runner) ComposeApply(ctx context.Context, a model.App, n model.Node, config string, registries ...[]ComposeRegistry) (string, string, error) {
	x, e := r.connect(n)
	if e != nil {
		return "", "", e
	}
	defer x.Close()
	dir := composeDir(a, n)
	if e = prepareComposeDirectory(ctx, x, a, n); e != nil {
		return "", "", e
	}
	if e = writeComposeFile(ctx, x, filepath.Join(dir, "candidate.json"), config); e != nil {
		return "", "", e
	}
	if e = r.composeLogin(ctx, x, a, n, config, registries...); e != nil {
		return "", "", e
	}

	cli := composeCLI(a, n, "candidate.json")
	r.progress("拉取服务镜像")
	pull := " pull"
	if r.ComposePullAlways {
		pull += " --policy always"
	}
	out, e := x.Run(ctx, cli+pull, "")
	if e != nil {
		return out, "", fmt.Errorf("镜像拉取失败")
	}
	r.progress("解析并锁定镜像 digest")
	pinned, e := pinComposeImages(ctx, x, config)
	if e != nil {
		return out, "", fmt.Errorf("镜像 digest 解析失败")
	}
	if e = writeComposeFile(ctx, x, filepath.Join(dir, "current.json"), pinned); e != nil {
		return out, "", e
	}
	r.progress("应用编排，等待服务启动与健康检查")
	applied, e := x.Run(ctx, composeCLI(a, n, "current.json")+" up -d --pull never --remove-orphans --wait --wait-timeout 120", "")
	return out + applied, pinned, e
}
func (r *Runner) ComposeAction(ctx context.Context, a model.App, n model.Node, action, service string) (string, error) {
	flags := map[string]string{"start": "start --wait --wait-timeout 120", "stop": "stop --timeout 30", "restart": "restart --timeout 30", "rebuild": "up -d --pull never --force-recreate --wait --wait-timeout 120", "delete": ""}
	args, ok := flags[action]
	if !ok {
		return "", fmt.Errorf("不支持的操作")
	}
	if service != "" {
		if !composeNameRE.MatchString(service) || action == "delete" {
			return "", fmt.Errorf("无效服务操作")
		}
		if action == "rebuild" || action == "restart" {
			args += " --no-deps"
		}
		args += " " + shell(service)
	}
	x, e := r.connect(n)
	if e != nil {
		return "", e
	}
	defer x.Close()
	r.progress("执行编排操作: " + action)
	if action == "delete" {
		return removeComposeProject(ctx, x, a, n)
	}
	out, e := x.Run(ctx, composeCLI(a, n, "current.json")+" "+args, "")
	if e == nil && action == "restart" {
		wait := composeCLI(a, n, "current.json") + " start --wait --wait-timeout 120"
		if service != "" {
			wait += " " + shell(service)
		}
		more, err := x.Run(ctx, wait, "")
		out += more
		e = err
	}
	return out, e
}
func removeComposeProject(ctx context.Context, x commandRunner, a model.App, n model.Node) (string, error) {
	filter := shell("label=com.docker.compose.project=" + ComposeProject(a))
	ids, e := x.Run(ctx, "docker ps -aq --filter "+filter, "")
	if e != nil {
		return "", e
	}
	args, e := dockerIDArgs(ids)
	if e != nil {
		return "", e
	}
	logs := ""
	if args != "" {
		out, err := x.Run(ctx, "docker stop --time 30"+args, "")
		logs += out
		if err != nil {
			return logs, err
		}
		out, err = x.Run(ctx, "docker rm"+args, "")
		logs += out
		if err != nil {
			return logs, err
		}
	}
	// Include networks orphaned by prior YAML updates. Volumes are always retained.
	ids, e = x.Run(ctx, "docker network ls -q --filter "+filter, "")
	if e != nil {
		return logs, e
	}
	args, e = dockerIDArgs(ids)
	if e != nil {
		return logs, e
	}
	if args != "" {
		out, err := x.Run(ctx, "docker network rm"+args, "")
		logs += out
		if err != nil {
			return logs, err
		}
	}
	_, e = x.Run(ctx, "rm -rf "+shell(composeDir(a, n)), "")
	return logs + "\n编排容器、网络和配置文件已移除，数据卷已保留。\n", e
}

var dockerIDRE = regexp.MustCompile(`^[a-f0-9]+$`)

func dockerIDArgs(raw string) (string, error) {
	args := ""
	for _, id := range strings.Fields(raw) {
		if !dockerIDRE.MatchString(id) {
			return "", fmt.Errorf("节点返回无效 Docker 资源 ID")
		}
		args += " " + shell(id)
	}
	return args, nil
}

type ComposeContainer struct {
	ID           string         `json:"id"`
	Service      string         `json:"service"`
	Name         string         `json:"name"`
	Image        string         `json:"image"`
	State        dockerState    `json:"state"`
	Ports        any            `json:"ports"`
	Stats        map[string]any `json:"stats,omitempty"`
	RestartCount int            `json:"restart_count"`
}

func (r *Runner) ComposeContainers(ctx context.Context, a model.App, n model.Node) ([]ComposeContainer, error) {
	x, e := r.connect(n)
	if e != nil {
		return nil, e
	}
	defer x.Close()
	result := []ComposeContainer{}
	ids, e := x.Run(ctx, "docker ps -aq --filter "+shell("label=com.docker.compose.project="+ComposeProject(a)), "")
	if e != nil {
		return nil, e
	}
	args := ""
	for _, v := range strings.Fields(ids) {
		if !regexp.MustCompile(`^[a-f0-9]+$`).MatchString(v) {
			return nil, fmt.Errorf("无效容器 ID")
		}
		args += " " + shell(v)
	}
	if args == "" {
		return result, nil
	}
	raw, e := x.Run(ctx, "docker inspect"+args, "")
	if e != nil {
		return nil, e
	}
	var containers []struct {
		ID     string `json:"Id"`
		Name   string
		Config struct {
			Image  string
			Labels map[string]string
		}
		State           dockerState
		RestartCount    int
		NetworkSettings struct{ Ports any }
	}
	if e = json.Unmarshal([]byte(raw), &containers); e != nil {
		return nil, e
	}
	runningArgs := ""
	for _, c := range containers {
		if c.State.Running {
			runningArgs += " " + shell(c.ID)
		}
	}
	var statsRaw string
	var statsErr error
	if runningArgs != "" {
		statsRaw, statsErr = x.Run(ctx, "docker stats --no-stream --format '{{json .}}'"+runningArgs, "")
	}

	stats := map[string]map[string]any{}
	if statsErr == nil {
		for _, line := range strings.Split(strings.TrimSpace(statsRaw), "\n") {
			var v map[string]any
			if json.Unmarshal([]byte(line), &v) == nil {
				stats[fmt.Sprint(v["ID"])] = v
			}
		}
	}
	for _, c := range containers {
		if c.Config.Labels["com.docker.compose.project"] != ComposeProject(a) {
			continue
		}
		var st map[string]any
		for id, v := range stats {
			if strings.HasPrefix(c.ID, id) {
				st = v
				break
			}
		}
		result = append(result, ComposeContainer{ID: c.ID, Service: c.Config.Labels["com.docker.compose.service"], Name: strings.TrimPrefix(c.Name, "/"), Image: c.Config.Image, State: c.State, Ports: c.NetworkSettings.Ports, Stats: st, RestartCount: c.RestartCount})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
func (r *Runner) ComposeLogs(ctx context.Context, a model.App, n model.Node, service string, tail int, w io.Writer) error {
	if service != "" && !composeNameRE.MatchString(service) {
		return fmt.Errorf("无效服务名")
	}
	x, e := r.connect(n)
	if e != nil {
		return e
	}
	defer x.Close()
	cmd := composeCLI(a, n, "current.json") + fmt.Sprintf(" logs --follow --timestamps --no-color --tail %d", tail)
	if service != "" {
		cmd += " " + shell(service)
	}
	return x.Stream(ctx, cmd, "", w)
}

func (r *Runner) ComposeRestore(ctx context.Context, a model.App, n model.Node, config string, registries ...[]ComposeRegistry) (string, error) {
	x, e := r.connect(n)
	if e != nil {
		return "", e
	}
	defer x.Close()
	dir := composeDir(a, n)
	if e = prepareComposeDirectory(ctx, x, a, n); e != nil {
		return "", e
	}
	if e = writeComposeFile(ctx, x, filepath.Join(dir, "current.json"), config); e != nil {
		return "", e
	}
	if e = r.composeLogin(ctx, x, a, n, config, registries...); e != nil {
		return "", e
	}
	r.progress("恢复版本配置与镜像，等待服务健康检查")
	return x.Run(ctx, composeCLI(a, n, "current.json")+" up -d --pull missing --remove-orphans --wait --wait-timeout 120", "")
}

// Extract inline environment values to keep them out of operation logs.
func ComposeSecretValues(raw string) []string {
	values := []string{}
	var doc map[string]any
	if yaml.Unmarshal([]byte(raw), &doc) != nil {
		return values
	}
	services, _ := doc["services"].(map[string]any)
	for _, v := range services {
		svc, _ := v.(map[string]any)
		switch env := svc["environment"].(type) {
		case map[string]any:
			for _, v := range env {
				if v != nil {
					values = append(values, fmt.Sprint(v))
				}
			}
		case []any:
			for _, v := range env {
				if _, value, ok := strings.Cut(fmt.Sprint(v), "="); ok {
					values = append(values, value)
				}
			}
		}
	}
	return values
}

func (r *Runner) composeLogin(ctx context.Context, x commandRunner, a model.App, n model.Node, config string, registries ...[]ComposeRegistry) error {
	// Removing credentials from a configuration must also remove cached node auth.
	if _, e := x.Run(ctx, "rm -f "+shell(filepath.Join(composeDir(a, n), "registry", "config.json")), ""); e != nil {
		return fmt.Errorf("无法重置编排仓库凭证")
	}
	credentials := []ComposeRegistry{}
	if len(registries) > 0 {
		credentials = append(credentials, registries[0]...)
	}
	configured := false
	for _, c := range credentials {
		if c.Server == r.Registry {
			configured = true
		}
	}
	// Optional platform ACR credentials only apply when this project actually uses ACR.
	if !configured && r.Registry != "" && r.RegistryUser != "" && r.RegistryPassword != "" {
		var doc struct {
			Services map[string]struct {
				Image string `json:"image"`
			}
		}
		_ = json.Unmarshal([]byte(config), &doc)
		for _, service := range doc.Services {
			if strings.HasPrefix(service.Image, r.Registry+"/") {
				credentials = append(credentials, ComposeRegistry{Server: r.Registry, Username: r.RegistryUser, Password: r.RegistryPassword})
				break
			}
		}
	}
	for _, credential := range credentials {
		r.progress("登录镜像仓库: " + credential.Server)
		if _, e := x.Run(ctx, "env -i PATH=\"$PATH\" docker --config "+shell(filepath.Join(composeDir(a, n), "registry"))+" login "+shell(credential.Server)+" --username "+shell(credential.Username)+" --password-stdin", credential.Password+"\n"); e != nil {
			return fmt.Errorf("镜像仓库 %s 登录失败", credential.Server)
		}
	}
	return nil
}

// Pin the digest of the image that was actually pulled into the node's daemon.
// Resolving tags against the registry again can race a tag change and introduces
// a second network dependency after a successful pull.
func pinComposeImages(ctx context.Context, x commandRunner, config string) (string, error) {
	var doc map[string]any
	if e := json.Unmarshal([]byte(config), &doc); e != nil {
		return "", e
	}
	services, ok := doc["services"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("缺少服务")
	}
	cache := map[string]string{}
	for _, value := range services {
		svc, ok := value.(map[string]any)
		if !ok {
			return "", fmt.Errorf("无效服务配置")
		}
		image, _ := svc["image"].(string)
		digest, ok := cache[image]
		if !ok {
			raw, e := x.Run(ctx, "docker image inspect --format '{{json .RepoDigests}}' -- "+shell(image), "")
			if e != nil {
				return "", fmt.Errorf("无法读取已拉取镜像的 digest")
			}
			var digests []string
			if e = json.Unmarshal([]byte(raw), &digests); e != nil {
				return "", fmt.Errorf("无效镜像 digest")
			}
			digest, e = selectImageDigest(image, digests)
			if e != nil {
				return "", e
			}
			cache[image] = digest
		}
		svc["image"] = digest
	}
	raw, e := json.Marshal(doc)
	return string(raw), e
}
func normalizedRepository(image string) string {
	repository, _, _ := strings.Cut(image, "@")
	if colon := strings.LastIndex(repository, ":"); colon > strings.LastIndex(repository, "/") {
		repository = repository[:colon]
	}
	parts := strings.Split(repository, "/")
	if len(parts) == 1 {
		return "docker.io/library/" + repository
	}
	first := parts[0]
	if !strings.ContainsAny(first, ".:") && first != "localhost" {
		return "docker.io/" + repository
	}
	if first == "index.docker.io" {
		parts[0] = "docker.io"
	}
	if parts[0] == "docker.io" && len(parts) == 2 {
		return "docker.io/library/" + parts[1]
	}
	return strings.Join(parts, "/")
}
func selectImageDigest(image string, digests []string) (string, error) {
	for _, candidate := range digests {
		_, digest, ok := strings.Cut(candidate, "@")
		if ok && normalizedRepository(candidate) == normalizedRepository(image) && regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(digest) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("已拉取镜像缺少对应仓库的 digest，请使用镜像仓库中可拉取的镜像")
}
