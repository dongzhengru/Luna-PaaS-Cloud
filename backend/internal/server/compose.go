package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"paas.local/backend/internal/deploy"
	"paas.local/backend/internal/model"
)

func (s *Server) composeRoutes() {
	s.mux.HandleFunc("POST /api/compose/validate", s.validateCompose)
	s.mux.HandleFunc("GET /api/apps/{id}/compose/tasks", s.composeTasks)
	s.mux.HandleFunc("POST /api/compose/apps", s.createCompose)
	s.mux.HandleFunc("GET /api/apps/{id}/compose", s.getCompose)
	s.mux.HandleFunc("PUT /api/apps/{id}/compose", s.saveCompose)
	s.mux.HandleFunc("POST /api/apps/{id}/compose/actions", s.composeAction)
	s.mux.HandleFunc("GET /api/apps/{id}/containers", s.composeContainers)
	s.mux.HandleFunc("GET /api/apps/{id}/compose/versions", s.composeVersions)
	s.mux.HandleFunc("GET /api/apps/{id}/compose/versions/{version}", s.composeVersion)
}

type composeRequest struct {
	Name   string `json:"name"`
	NodeID string `json:"node_id"`
	AppID  string `json:"app_id"`
	deploy.ComposeInput
}

func decodeCompose(w http.ResponseWriter, r *http.Request, in any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	w.Header().Set("Cache-Control", "no-store")
	return decode(r, in)
}
func (s *Server) composeRunner() *deploy.Runner {
	// Public registries require no platform-wide credentials.
	u, _ := s.setting("acr_username")
	p, _ := s.setting("acr_password")
	return &deploy.Runner{DB: s.db, Box: s.box, Registry: s.cfg.Registry, RegistryUser: u, RegistryPassword: p}
}
func (s *Server) composeResource(r *http.Request) (model.App, model.Node, error) {
	a, n, e := s.appAndNode(r.PathValue("id"))
	if e == nil && a.DeploymentSource != "compose" {
		e = fmt.Errorf("该应用不是容器编排部署")
	}
	return a, n, e
}
func (s *Server) sealCompose(in deploy.ComposeInput) (string, error) {
	b, e := json.Marshal(in)
	if e != nil {
		return "", e
	}
	return s.box.Seal(string(b))
}
func (s *Server) openCompose(raw string) (deploy.ComposeInput, error) {
	var in deploy.ComposeInput
	plain, e := s.box.Open(raw)
	if e != nil {
		return in, e
	}
	e = json.Unmarshal([]byte(plain), &in)
	return in, e
}
func (s *Server) validateCompose(w http.ResponseWriter, r *http.Request) {
	var in composeRequest
	if e := decodeCompose(w, r, &in); e != nil {
		fail(w, 400, e)
		return
	}
	var n model.Node
	if s.db.First(&n, "id = ?", in.NodeID).Error != nil {
		fail(w, 400, "请选择有效的部署节点")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_, p, e := s.composeRunner().ComposeValidate(ctx, model.App{Base: model.Base{ID: id()}}, n, in.ComposeInput)
	if e != nil {
		fail(w, 400, e)
		return
	}
	p.Added = []string{}
	p.Removed = []string{}
	previous := map[string]json.RawMessage{}
	if in.AppID != "" {
		var a model.App
		if s.db.First(&a, "id = ? AND deployment_source = ? AND node_id = ?", in.AppID, "compose", n.ID).Error != nil {
			fail(w, 404, "编排应用不存在或节点不匹配")
			return
		}
		var version model.ComposeVersion
		if a.ComposeActiveVersionID != "" && s.db.First(&version, "id = ? AND app_id = ?", a.ComposeActiveVersionID, a.ID).Error == nil {
			plain, err := s.box.Open(version.Resolved)
			if err != nil {
				fail(w, 500, "生效配置解密失败")
				return
			}
			var doc struct {
				Services map[string]json.RawMessage `json:"services"`
			}
			if json.Unmarshal([]byte(plain), &doc) == nil {
				previous = doc.Services
			}
		}
	}
	current := map[string]bool{}
	for _, service := range p.Services {
		current[service.Name] = true
		if _, exists := previous[service.Name]; !exists {
			p.Added = append(p.Added, service.Name)
		}
	}
	for service := range previous {
		if !current[service] {
			p.Removed = append(p.Removed, service)
		}
	}
	sort.Strings(p.Removed)
	jsonOut(w, 200, p)
}
func (s *Server) createCompose(w http.ResponseWriter, r *http.Request) {
	var in composeRequest
	if e := decodeCompose(w, r, &in); e != nil {
		fail(w, 400, e)
		return
	}
	if !appNameRE.MatchString(in.Name) || len(in.Name) > 50 {
		fail(w, 400, "应用名称必须以小写字母开头，只能包含小写字母、数字和连字符，最多 50 字符")
		return
	}
	var n model.Node
	if s.db.First(&n, "id = ?", in.NodeID).Error != nil {
		fail(w, 400, "请选择有效的部署节点")
		return
	}
	if e := deploy.ValidateComposeEnvironment(in.ComposeInput); e != nil {
		fail(w, 400, e)
		return
	}
	if _, e := deploy.ValidateCompose(in.YAML, n); e != nil {
		fail(w, 400, e)
		return
	}
	sealed, e := s.sealCompose(in.ComposeInput)
	if e != nil {
		fail(w, 500, e)
		return
	}
	appID := id()
	// A per-app URI preserves the existing repository uniqueness constraint.
	a := model.App{Base: model.Base{ID: appID}, Name: in.Name, Slug: in.Name, Type: "compose", DeploymentSource: "compose", NodeID: n.ID, RepoURL: "compose://" + appID, ComposeDraft: sealed, Status: "pending"}
	t := model.Task{Base: model.Base{ID: id()}, Kind: "compose:deploy", ResourceID: a.ID, Status: "queued"}
	v := model.ComposeVersion{Base: model.Base{ID: id()}, AppID: a.ID, Source: sealed, Action: "deploy", Status: "queued", TaskID: t.ID}
	e = s.db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Create(&a).Error; e != nil {
			return e
		}
		if e := tx.Create(&t).Error; e != nil {
			return e
		}
		return tx.Create(&v).Error
	})
	if e != nil {
		fail(w, 409, "创建失败，应用名称可能已存在")
		return
	}
	go s.executeCompose(t.ID, v.ID, "", "")
	jsonOut(w, 202, map[string]any{"app": s.safeApp(a), "task_id": t.ID})
}
func (s *Server) getCompose(w http.ResponseWriter, r *http.Request) {
	a, _, e := s.composeResource(r)
	w.Header().Set("Cache-Control", "no-store")
	if e != nil {
		fail(w, 404, e)
		return
	}
	in, e := s.openCompose(a.ComposeDraft)
	if e != nil {
		fail(w, 500, "配置解密失败")
		return
	}
	jsonOut(w, 200, in)
}
func (s *Server) saveCompose(w http.ResponseWriter, r *http.Request) {
	a, n, e := s.composeResource(r)
	if e != nil {
		fail(w, 404, e)
		return
	}
	var in deploy.ComposeInput
	if e = decodeCompose(w, r, &in); e != nil {
		fail(w, 400, e)
		return
	}
	if e = deploy.ValidateComposeEnvironment(in); e != nil {
		fail(w, 400, e)
		return
	}
	if _, e = deploy.ValidateCompose(in.YAML, n); e != nil {
		fail(w, 400, e)
		return
	}
	sealed, e := s.sealCompose(in)
	if e != nil {
		fail(w, 500, e)
		return
	}
	if e = s.db.Model(&a).Update("compose_draft", sealed).Error; e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 200, map[string]any{"saved": true})
}
func (s *Server) composeVersions(w http.ResponseWriter, r *http.Request) {
	a, _, e := s.composeResource(r)
	w.Header().Set("Cache-Control", "no-store")
	if e != nil {
		fail(w, 404, e)
		return
	}
	versions := []model.ComposeVersion{}
	if e = s.db.Where("app_id = ?", a.ID).Order("created_at desc").Limit(100).Find(&versions).Error; e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 200, versions)
}
func (s *Server) composeVersion(w http.ResponseWriter, r *http.Request) {
	a, _, e := s.composeResource(r)
	w.Header().Set("Cache-Control", "no-store")
	if e != nil {
		fail(w, 404, e)
		return
	}
	var v model.ComposeVersion
	if s.db.First(&v, "id = ? AND app_id = ?", r.PathValue("version"), a.ID).Error != nil {
		fail(w, 404, "版本不存在")
		return
	}
	in, e := s.openCompose(v.Source)
	if e != nil {
		fail(w, 500, "配置解密失败")
		return
	}
	jsonOut(w, 200, in)
}
func (s *Server) composeAction(w http.ResponseWriter, r *http.Request) {
	a, _, e := s.composeResource(r)
	w.Header().Set("Cache-Control", "no-store")
	if e != nil {
		fail(w, 404, e)
		return
	}
	var in struct {
		Action    string               `json:"action"`
		Service   string               `json:"service"`
		VersionID string               `json:"version_id"`
		Config    *deploy.ComposeInput `json:"config"`
	}
	if e = decodeCompose(w, r, &in); e != nil {
		fail(w, 400, e)
		return
	}
	if !map[string]bool{"deploy": true, "update_images": true, "rollback": true, "start": true, "stop": true, "restart": true, "rebuild": true, "delete": true}[in.Action] {
		fail(w, 400, "不支持的操作")
		return
	}
	if in.Service != "" && (in.Action == "deploy" || in.Action == "rollback" || in.Action == "update_images" || in.Action == "delete") {
		fail(w, 400, "此操作需要对整个编排执行")
		return
	}
	var target model.ComposeVersion
	source := a.ComposeDraft
	if in.Action == "rollback" {
		if s.db.First(&target, "id = ? AND app_id = ? AND status = ?", in.VersionID, a.ID, "succeeded").Error != nil || target.Resolved == "" {
			fail(w, 400, "请选择成功部署的版本")
			return
		}
		source = target.Source
	} else if in.Action == "update_images" {
		if a.ComposeActiveVersionID == "" || s.db.First(&target, "id = ? AND app_id = ?", a.ComposeActiveVersionID, a.ID).Error != nil {
			fail(w, 409, "尚无已生效的编排配置")
			return
		}
		source = target.Source
	} else if in.Action == "deploy" && in.Config != nil {
		_, n, _ := s.appAndNode(a.ID)
		if e = deploy.ValidateComposeEnvironment(*in.Config); e != nil {
			fail(w, 400, e)
			return
		}
		if _, e = deploy.ValidateCompose(in.Config.YAML, n); e != nil {
			fail(w, 400, e)
			return
		}
		source, e = s.sealCompose(*in.Config)
		if e != nil {
			fail(w, 500, e)
			return
		}
	}
	t := model.Task{Base: model.Base{ID: id()}, Kind: "compose:" + in.Action, ResourceID: a.ID, Status: "queued"}
	var v model.ComposeVersion
	if in.Action == "deploy" || in.Action == "update_images" || in.Action == "rollback" {
		v = model.ComposeVersion{Base: model.Base{ID: id()}, AppID: a.ID, Source: source, Action: in.Action, Status: "queued", TaskID: t.ID}
		if in.Action == "rollback" {
			v.Resolved = target.Resolved
		}
	}
	e = s.db.Transaction(func(tx *gorm.DB) error {
		// Serialize admission using the app row, including across concurrent HTTP requests.
		var locked model.App
		if e := tx.Raw("SELECT * FROM apps WHERE id = ? FOR UPDATE", a.ID).Scan(&locked).Error; e != nil {
			return e
		}
		if locked.ID == "" {
			return fmt.Errorf("应用已删除")
		}
		var count int64
		if e := tx.Model(&model.Task{}).Where("resource_id = ? AND status IN ?", a.ID, []string{"queued", "running"}).Count(&count).Error; e != nil {
			return e
		}
		if count > 0 {
			return fmt.Errorf("该应用有操作正在执行，请等待完成")
		}
		if e := tx.Create(&t).Error; e != nil {
			return e
		}
		if v.ID != "" {
			if e := tx.Create(&v).Error; e != nil {
				return e
			}
		}
		if in.Action == "deploy" && in.Config != nil {
			return tx.Model(&a).Update("compose_draft", source).Error
		}
		return nil
	})
	if e != nil {
		fail(w, 409, e)
		return
	}
	go s.executeCompose(t.ID, v.ID, in.Service, in.Action)
	jsonOut(w, 202, map[string]string{"task_id": t.ID})
}
func (s *Server) composeContainers(w http.ResponseWriter, r *http.Request) {
	a, n, e := s.composeResource(r)
	if e != nil {
		fail(w, 404, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	list, e := s.composeRunner().ComposeContainers(ctx, a, n)
	if e != nil {
		fail(w, 502, "无法读取节点容器，请检查节点连接")
		return
	}
	jsonOut(w, 200, list)
}
func (s *Server) executeCompose(taskID, versionID, service, action string) {
	var t model.Task
	if s.db.First(&t, "id = ?", taskID).Error != nil {
		return
	}
	lockAny, _ := s.locks.LoadOrStore(t.ResourceID, &sync.Mutex{})
	mu := lockAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	s.task(taskID, "running", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	a, n, e := s.appAndNode(t.ResourceID)
	var v model.ComposeVersion
	var logs, stages string
	var secrets []string
	runner := s.composeRunner()
	runner.OnProgress = func(message string) {
		stages += time.Now().UTC().Format(time.RFC3339) + " " + message + "\n"
		s.db.Model(&model.Task{}).Where("id = ?", taskID).Update("logs", stages)
	}
	// Finalize every error path so a missing node or malformed snapshot cannot leave a running task.
	defer func() {
		if e != nil {
			errorText := scrubCompose(e.Error(), secrets)
			s.db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": "failed", "error": errorText, "logs": scrubCompose(stages+logs, secrets)})
			if v.ID != "" {
				s.db.Model(&v).Update("status", "failed")
			}
			s.db.Model(&model.App{}).Where("id = ?", t.ResourceID).Updates(map[string]any{"last_error": errorText, "status": "operation_failed"})
			return
		}
		s.db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": "succeeded", "logs": scrubCompose(stages+logs, secrets), "error": ""})
	}()
	if e != nil {
		return
	}
	phase := map[string]string{"start": "starting", "stop": "stopping", "restart": "restarting", "rebuild": "rebuilding", "delete": "deleting"}[action]
	if versionID != "" {
		phase = "deploying"
	}
	if phase != "" {
		if err := s.db.Model(&a).Updates(map[string]any{"status": phase, "last_error": ""}).Error; err != nil {
			e = err
			return
		}
	}
	if versionID != "" {
		e = s.db.First(&v, "id = ? AND app_id = ?", versionID, a.ID).Error
		if e != nil {
			return
		}
		s.db.Model(&v).Update("status", "running")
		var input deploy.ComposeInput
		input, e = s.openCompose(v.Source)
		if e != nil {
			return
		}
		for _, value := range input.Environment {
			if value != "" {
				secrets = append(secrets, value)
			}
		}
		secrets = append(secrets, runner.RegistryPassword)
		for _, registry := range input.Registries {
			secrets = append(secrets, registry.Password)
		}
		// Inline environment values are also secrets, regardless of how they were entered.
		secrets = append(secrets, deploy.ComposeSecretValues(input.YAML)...)
		var resolved string
		if v.Action == "rollback" {
			resolved, e = s.box.Open(v.Resolved)
			if e != nil {
				return
			}
			logs, e = runner.ComposeRestore(ctx, a, n, resolved, input.Registries)
		} else {
			var canonical string
			canonical, _, e = runner.ComposeValidate(ctx, a, n, input)
			if e != nil {
				return
			}
			runner.ComposePullAlways = v.Action == "update_images"
			logs, resolved, e = runner.ComposeApply(ctx, a, n, canonical, input.Registries)
		}
		if resolved != "" {
			var sealed string
			sealed, e2 := s.box.Seal(resolved)
			if e2 != nil {
				e = e2
				return
			}
			v.Resolved = sealed
			if err := s.db.Model(&v).Update("resolved", sealed).Error; err != nil {
				e = err
				return
			}
		}
		if e != nil {
			return
		}
		e = s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&v).Update("status", "succeeded").Error; err != nil {
				return err
			}
			return tx.Model(&a).Updates(map[string]any{"compose_active_version_id": v.ID, "status": "running", "last_error": ""}).Error
		})
		return
	}
	if a.ComposeActiveVersionID != "" {
		var active model.ComposeVersion
		if s.db.First(&active, "id = ?", a.ComposeActiveVersionID).Error == nil {
			if input, err := s.openCompose(active.Source); err == nil {
				for _, value := range input.Environment {
					secrets = append(secrets, value)
				}
				secrets = append(secrets, deploy.ComposeSecretValues(input.YAML)...)
				for _, registry := range input.Registries {
					secrets = append(secrets, registry.Password)
				}
			}
		}
	}
	if service != "" {
		var list []deploy.ComposeContainer
		list, e = runner.ComposeContainers(ctx, a, n)
		if e != nil {
			return
		}
		found := false
		for _, c := range list {
			if c.Service == service {
				found = true
			}
		}
		if !found {
			e = fmt.Errorf("该服务不属于此编排应用")
			return
		}
	}
	logs, e = runner.ComposeAction(ctx, a, n, action, service)
	if e != nil {
		return
	}
	if action == "delete" {
		e = s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("app_id = ?", a.ID).Delete(&model.ComposeVersion{}).Error; err != nil {
				return err
			}
			if err := tx.Where("resource_id = ? AND id <> ?", a.ID, t.ID).Delete(&model.Task{}).Error; err != nil {
				return err
			}
			return tx.Delete(&a).Error
		})
		return
	}
	status := "running"
	if action == "stop" {
		status = "stopped"
	}
	if service != "" {
		status = "partial"
	}
	e = s.db.Model(&a).Updates(map[string]any{"status": status, "last_error": ""}).Error
}
func scrubCompose(s string, values []string) string {
	for _, v := range values {
		if v != "" {
			s = strings.ReplaceAll(s, v, "***")
		}
	}
	if len(s) > 256*1024 {
		s = s[len(s)-256*1024:]
	}
	return s
}

// Recover abandoned jobs after a platform restart; the UI reads actual Docker
// state separately and offers an explicit retry instead of replaying mutations.
func (s *Server) recoverComposeTasks() {
	var tasks []model.Task
	if s.db.Where("kind LIKE ? AND status IN ?", "compose:%", []string{"queued", "running"}).Find(&tasks).Error != nil {
		return
	}
	for _, t := range tasks {
		s.task(t.ID, "failed", "平台已重启，操作结果待核对，请检查容器状态后重试")
		s.db.Model(&model.ComposeVersion{}).Where("task_id = ?", t.ID).Update("status", "failed")
		s.db.Model(&model.App{}).Where("id = ?", t.ResourceID).Updates(map[string]any{"status": "unknown", "last_error": "平台重启中断操作，请核对容器状态"})
	}
}

func (s *Server) composeTasks(w http.ResponseWriter, r *http.Request) {
	a, _, e := s.composeResource(r)
	if e != nil {
		fail(w, 404, e)
		return
	}
	tasks := []model.Task{}
	if e = s.db.Where("resource_id = ? AND kind LIKE ?", a.ID, "compose:%").Order("created_at desc").Limit(100).Find(&tasks).Error; e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 200, tasks)
}
