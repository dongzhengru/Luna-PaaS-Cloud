package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"paas.local/backend/internal/config"
	"paas.local/backend/internal/deploy"
	gh "paas.local/backend/internal/github"
	"paas.local/backend/internal/model"
	"paas.local/backend/internal/secure"
)

type Server struct {
	cfg   config.Config
	db    *gorm.DB
	box   *secure.Box
	mux   *http.ServeMux
	locks sync.Map
	http  *http.Client
}

func New(c config.Config, db *gorm.DB, b *secure.Box) http.Handler {
	s := &Server{
		cfg:  c,
		db:   db,
		box:  b,
		mux:  http.NewServeMux(),
		http: &http.Client{Timeout: 20 * time.Second},
	}
	s.routes()
	return s.middleware(s.mux)
}
func (s *Server) routes() {
	s.mux.HandleFunc(
		"GET /healthz",
		func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]string{"status": "ok"}) },
	)
	s.mux.HandleFunc("GET /api/auth/login", s.login)
	s.mux.HandleFunc("GET /api/auth/callback", s.oauthCallback)
	s.mux.HandleFunc("GET /api/auth/me", s.me)
	s.mux.HandleFunc("POST /api/auth/logout", s.logout)
	s.mux.HandleFunc("GET /api/settings", s.getSettings)
	s.mux.HandleFunc("PUT /api/settings", s.putSettings)
	s.mux.HandleFunc("GET /api/github/repos", s.listGitHubRepos)
	s.mux.HandleFunc("GET /api/nodes", s.listNodes)
	s.mux.HandleFunc("POST /api/nodes", s.createNode)
	s.mux.HandleFunc("POST /api/nodes/{id}/test", s.testNode)
	s.mux.HandleFunc("DELETE /api/nodes/{id}", s.deleteNode)
	s.mux.HandleFunc("GET /api/apps", s.listApps)
	s.mux.HandleFunc("POST /api/apps", s.createApp)
	s.mux.HandleFunc("GET /api/apps/{id}", s.getApp)
	s.mux.HandleFunc("GET /api/apps/{id}/stats", s.getAppStats)
	s.mux.HandleFunc("GET /api/apps/{id}/logs", s.streamAppLogs)
	s.mux.HandleFunc("PUT /api/apps/{id}", s.updateApp)
	s.mux.HandleFunc("DELETE /api/apps/{id}", s.deleteApp)
	s.mux.HandleFunc("POST /api/apps/{id}/initialize", s.retryInitialize)
	s.mux.HandleFunc("GET /api/apps/{id}/builds", s.listBuilds)
	s.mux.HandleFunc("GET /api/apps/{id}/releases", s.listReleases)
	s.mux.HandleFunc("POST /api/apps/{id}/releases", s.createRelease)
	s.mux.HandleFunc("POST /api/apps/{id}/releases/{release}/rollback", s.rollback)
	s.mux.HandleFunc("POST /api/apps/{id}/sync", s.syncBuilds)
	s.mux.HandleFunc("GET /api/tasks/{id}", s.getTask)
	s.mux.HandleFunc("POST /api/callbacks/github/actions/{appId}", s.buildCallback)
}
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if o := r.Header.Get("Origin"); o == s.cfg.FrontendURL {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		public := r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/api/auth/") ||
			strings.HasPrefix(r.URL.Path, "/api/callbacks/")
		if !public {
			if _, ok := s.session(r); !ok {
				fail(w, 401, "authentication required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, e any) {
	jsonOut(w, status, map[string]any{"error": fmt.Sprint(e)})
}
func decode(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(v)
}
func hash(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

func id() string {
	return uuid.NewString()
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.cfg.OAuthClientID == "" || s.cfg.OAuthRedirectURL == "" {
		fail(w, 503, "OAuth is not configured")
		return
	}
	token, _ := secure.Token()
	s.db.Create(
		&model.OAuthState{
			Base:      model.Base{ID: id()},
			StateHash: hash(token),
			ExpiresAt: time.Now().Add(10 * time.Minute),
		},
	)
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {s.cfg.OAuthClientID},
		"redirect_uri":  {s.cfg.OAuthRedirectURL},
		"scope":         {s.cfg.OAuthScope},
		"state":         {token},
	}
	http.Redirect(w, r, s.cfg.OAuthAuthURL+"?"+q.Encode(), http.StatusFound)
}
func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	var st model.OAuthState
	if state == "" ||
		s.db.Where("state_hash = ? AND expires_at > ?", hash(state), time.Now()).
			First(&st).
			Error != nil {
		fail(w, 400, "invalid or expired OAuth state")
		return
	}
	s.db.Delete(&st)
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {s.cfg.OAuthClientID},
		"client_secret": {s.cfg.OAuthClientSecret},
		"redirect_uri":  {s.cfg.OAuthRedirectURL},
	}
	req, _ := http.NewRequestWithContext(
		r.Context(),
		"POST",
		s.cfg.OAuthTokenURL,
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := s.http.Do(req)
	if e != nil || resp.StatusCode/100 != 2 {
		fail(w, 502, "OAuth token exchange failed")
		return
	}
	defer resp.Body.Close()
	var tok map[string]any
	tokenBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(tokenBody, &tok) != nil {
		values, e := url.ParseQuery(string(tokenBody))
		if e != nil || values.Get("access_token") == "" {
			fail(w, 502, "invalid token response")
			return
		}
		tok = map[string]any{"access_token": values.Get("access_token")}
	}
	access, _ := tok["access_token"].(string)
	req, _ = http.NewRequestWithContext(r.Context(), "GET", s.cfg.OAuthUserURL, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, e = s.http.Do(req)
	if e != nil || resp.StatusCode/100 != 2 {
		fail(w, 502, "OAuth user request failed")
		return
	}
	defer resp.Body.Close()
	var info any
	if json.NewDecoder(resp.Body).Decode(&info) != nil {
		fail(w, 502, "invalid user response")
		return
	}
	phone := jsonPath(info, s.cfg.OAuthPhonePath)
	var user model.AllowedUser
	if phone == "" ||
		s.db.Where("phone = ? AND enabled = ?", phone, true).First(&user).Error != nil {
		fail(w, 403, "phone is not allowed")
		return
	}
	raw, _ := secure.Token()
	sess := model.Session{
		Base:      model.Base{ID: id()},
		TokenHash: hash(raw),
		Phone:     phone,
		ExpiresAt: time.Now().Add(s.cfg.SessionTTL),
	}
	s.db.Create(&sess)
	http.SetCookie(
		w,
		&http.Cookie{
			Name:     "paas_session",
			Value:    raw,
			Path:     "/",
			HttpOnly: true,
			Secure:   s.cfg.SecureCookies,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(s.cfg.SessionTTL.Seconds()),
		},
	)
	http.Redirect(w, r, s.cfg.FrontendURL, http.StatusFound)
}
func jsonPath(v any, path string) string {
	cur := v
	for _, p := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[p]
	}
	x, _ := cur.(string)
	return x
}
func (s *Server) session(r *http.Request) (model.Session, bool) {
	c, e := r.Cookie("paas_session")
	if e != nil {
		return model.Session{}, false
	}
	var x model.Session
	e = s.db.Where("token_hash = ? AND expires_at > ?", hash(c.Value), time.Now()).First(&x).Error
	return x, e == nil
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	x, ok := s.session(r)
	if !ok {
		fail(w, 401, "not logged in")
		return
	}
	jsonOut(w, 200, map[string]string{"phone": x.Phone})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("paas_session"); e == nil {
		s.db.Where("token_hash = ?", hash(c.Value)).Delete(&model.Session{})
	}
	http.SetCookie(
		w,
		&http.Cookie{
			Name:     "paas_session",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   s.cfg.SecureCookies,
		},
	)
	w.WriteHeader(204)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	jsonOut(
		w,
		200,
		map[string]any{
			"github_configured":       s.hasSetting("github_token"),
			"acr_configured":          s.hasSetting("acr_username") && s.hasSetting("acr_password"),
			"notification_configured": s.hasSetting("dingtalk_webhook"),
			"registry":                s.cfg.Registry,
			"namespace":               s.cfg.RegistryNamespace,
			"public_url":              s.cfg.PublicURL,
		},
	)
}
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		GitHubToken     string `json:"github_token"`
		ACRUsername     string `json:"acr_username"`
		ACRPassword     string `json:"acr_password"`
		DingTalkWebhook string `json:"dingtalk_webhook"`
	}
	if e := decode(r, &in); e != nil {
		fail(w, 400, e)
		return
	}
	if in.DingTalkWebhook != "" {
		if e := validateDingTalkWebhook(in.DingTalkWebhook); e != nil {
			fail(w, 400, e)
			return
		}
	}
	settings := map[string]string{
		"github_token":     in.GitHubToken,
		"acr_username":     in.ACRUsername,
		"acr_password":     in.ACRPassword,
		"dingtalk_webhook": in.DingTalkWebhook,
	}
	for k, v := range settings {
		if v != "" {
			if e := s.setSetting(k, v); e != nil {
				fail(w, 500, e)
				return
			}
		}
	}
	s.getSettings(w, r)
}

func (s *Server) listGitHubRepos(w http.ResponseWriter, r *http.Request) {
	pat, e := s.setting("github_token")
	if e != nil || pat == "" {
		fail(w, http.StatusServiceUnavailable, "GitHub PAT is not configured")
		return
	}
	repos, e := gh.New(pat).Repos(r.Context())
	if e != nil {
		fail(w, http.StatusBadGateway, fmt.Sprintf("cannot fetch GitHub repositories: %v", e))
		return
	}
	jsonOut(w, http.StatusOK, repos)
}

func validateDingTalkWebhook(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "oapi.dingtalk.com") ||
		u.Path != "/robot/send" ||
		u.Query().Get("access_token") == "" {
		return fmt.Errorf("invalid DingTalk robot webhook URL")
	}
	return nil
}

func (s *Server) notifyDingTalk(title, markdown string) {
	hook, e := s.setting("dingtalk_webhook")
	if e != nil || validateDingTalkWebhook(hook) != nil {
		return
	}
	payload, _ := json.Marshal(
		map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]string{"title": title, "text": markdown},
		},
	)
	req, e := http.NewRequestWithContext(
		context.Background(),
		"POST",
		hook,
		strings.NewReader(string(payload)),
	)
	if e != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := s.http.Do(req)
	if e != nil {
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
}
func (s *Server) hasSetting(k string) bool {
	var n int64
	s.db.Model(&model.SecretSetting{}).Where("`key` = ?", k).Count(&n)
	return n > 0
}
func (s *Server) setSetting(k, v string) error {
	enc, e := s.box.Seal(v)
	if e != nil {
		return e
	}
	return s.db.Save(&model.SecretSetting{Key: k, EncryptedValue: enc}).Error
}
func (s *Server) setting(k string) (string, error) {
	var x model.SecretSetting
	if e := s.db.First(&x, "`key` = ?", k).Error; e != nil {
		return "", e
	}
	return s.box.Open(x.EncryptedValue)
}

type nodeInput struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	Host              string `json:"host"`
	Username          string `json:"username"`
	AuthType          string `json:"auth_type"`
	Password          string `json:"password"`
	PrivateKey        string `json:"private_key"`
	Passphrase        string `json:"passphrase"`
	HostKey           string `json:"host_key"`
	DeployRoot        string `json:"deploy_root"`
	AllowedMountRoots string `json:"allowed_mount_roots"`
	Port              int    `json:"port"`
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	var x []model.Node
	s.db.Order("created_at desc").Find(&x)
	jsonOut(w, 200, x)
}
func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	var in nodeInput
	if e := decode(r, &in); e != nil {
		fail(w, 400, e)
		return
	}
	if in.Type != "local" && in.Type != "ssh" {
		fail(w, 400, "type must be local or ssh")
		return
	}
	if in.Port == 0 {
		in.Port = 22
	}
	if in.DeployRoot == "" {
		in.DeployRoot = "/opt/paas/apps"
	}
	n := model.Node{
		Base:              model.Base{ID: id()},
		Name:              in.Name,
		Type:              in.Type,
		Host:              in.Host,
		Port:              in.Port,
		Username:          in.Username,
		AuthType:          in.AuthType,
		HostKey:           in.HostKey,
		DeployRoot:        in.DeployRoot,
		AllowedMountRoots: in.AllowedMountRoots,
		ExecutorType:      "compose",
		Status:            "checking",
	}
	var e error
	if in.Password != "" {
		n.EncryptedPassword, e = s.box.Seal(in.Password)
	}
	if e == nil && in.PrivateKey != "" {
		n.EncryptedPrivateKey, e = s.box.Seal(in.PrivateKey)
	}
	if e == nil && in.Passphrase != "" {
		n.EncryptedPassphrase, e = s.box.Seal(in.Passphrase)
	}
	if e != nil {
		fail(w, 500, e)
		return
	}
	runner := &deploy.Runner{DB: s.db, Box: s.box}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	logs, e := runner.Test(ctx, n)
	if e != nil {
		fail(w, 400, e)
		return
	}
	n.Status = "ready"
	if e = s.db.Create(&n).Error; e != nil {
		fail(w, 409, e)
		return
	}
	jsonOut(w, 201, map[string]any{"node": n, "logs": logs})
}
func (s *Server) testNode(w http.ResponseWriter, r *http.Request) {
	var n model.Node
	if s.db.First(&n, "id = ?", r.PathValue("id")).Error != nil {
		fail(w, 404, "node not found")
		return
	}
	runner := &deploy.Runner{DB: s.db, Box: s.box}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	logs, e := runner.Test(ctx, n)
	if e != nil {
		n.Status = "error"
		n.LastError = e.Error()
		s.db.Save(&n)
		fail(w, 400, e)
		return
	}
	n.Status = "ready"
	n.LastError = ""
	s.db.Save(&n)
	jsonOut(w, 200, map[string]string{"logs": logs})
}
func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	var n int64
	s.db.Model(&model.App{}).Where("node_id = ?", r.PathValue("id")).Count(&n)
	if n > 0 {
		fail(w, 409, "node is used by applications")
		return
	}
	s.db.Delete(&model.Node{}, "id = ?", r.PathValue("id"))
	w.WriteHeader(204)
}

type appInput struct {
	Name              string                 `json:"name"`
	Type              string                 `json:"type"`
	RuntimeVersion    string                 `json:"runtime_version"`
	RepoURL           string                 `json:"repo_url"`
	Branch            string                 `json:"branch"`
	DockerfilePath    string                 `json:"dockerfile_path"`
	BuildContext      string                 `json:"build_context"`
	NodeID            string                 `json:"node_id"`
	RestartPolicy     string                 `json:"restart_policy"`
	HostPort          int                    `json:"host_port"`
	ContainerPort     int                    `json:"container_port"`
	HostAccessEnabled bool                   `json:"host_access_enabled"`
	Environment       []model.EnvironmentVar `json:"environment"`
	Volumes           []model.Volume         `json:"volumes"`
	Health            model.HealthCheck      `json:"health"`
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)
var appNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,49}$`)

func repoParts(raw string) (string, string, error) {
	u, e := url.Parse(raw)
	if e != nil || !strings.EqualFold(u.Host, "github.com") {
		return "", "", fmt.Errorf("only github.com repositories are supported")
	}
	p := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	x := strings.Split(p, "/")
	if len(x) != 2 || x[0] == "" || x[1] == "" {
		return "", "", fmt.Errorf("invalid GitHub repository URL")
	}
	return x[0], x[1], nil
}
func slug(v string) string {
	x := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(v), "-"), "-")
	if len(x) > 50 {
		x = x[:50]
	}
	return x
}
func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
	var x []model.App
	s.db.Order("created_at desc").Find(&x)
	jsonOut(w, 200, x)
}
func (s *Server) getApp(w http.ResponseWriter, r *http.Request) {
	var x model.App
	if s.db.First(&x, "id = ?", r.PathValue("id")).Error != nil {
		fail(w, 404, "app not found")
		return
	}
	jsonOut(w, 200, s.safeApp(x))
}

func (s *Server) appAndNode(appID string) (model.App, model.Node, error) {
	var a model.App
	if e := s.db.First(&a, "id = ?", appID).Error; e != nil {
		return model.App{}, model.Node{}, fmt.Errorf("app not found")
	}
	var n model.Node
	if e := s.db.First(&n, "id = ?", a.NodeID).Error; e != nil {
		return model.App{}, model.Node{}, fmt.Errorf("deployment node not found")
	}
	return a, n, nil
}

func (s *Server) getAppStats(w http.ResponseWriter, r *http.Request) {
	a, n, e := s.appAndNode(r.PathValue("id"))
	if e != nil {
		fail(w, http.StatusNotFound, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	stats, e := (&deploy.Runner{DB: s.db, Box: s.box}).Stats(ctx, a, n)
	if e != nil {
		fail(w, http.StatusBadGateway, fmt.Sprintf("cannot read container stats: %v", e))
		return
	}
	jsonOut(w, http.StatusOK, stats)
}

type sseLogWriter struct {
	mu      sync.Mutex
	w       io.Writer
	flusher http.Flusher
}

func (w *sseLogWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, e := fmt.Fprintf(w.w, "data: %s\n\n", base64.StdEncoding.EncodeToString(p)); e != nil {
		return 0, e
	}
	w.flusher.Flush()
	return len(p), nil
}

func (w *sseLogWriter) event(name, value string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	b, _ := json.Marshal(value)
	if _, e := fmt.Fprintf(w.w, "event: %s\ndata: %s\n\n", name, b); e != nil {
		return e
	}
	w.flusher.Flush()
	return nil
}

func (w *sseLogWriter) ping() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, e := io.WriteString(w.w, ": keepalive\n\n"); e != nil {
		return e
	}
	w.flusher.Flush()
	return nil
}

func (s *Server) streamAppLogs(w http.ResponseWriter, r *http.Request) {
	a, n, e := s.appAndNode(r.PathValue("id"))
	if e != nil {
		fail(w, http.StatusNotFound, e)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail < 1 {
		tail = 300
	}
	if tail > 2000 {
		tail = 2000
	}
	// This endpoint intentionally outlives the server's normal WriteTimeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := &sseLogWriter{w: w, flusher: flusher}
	_ = stream.event("ready", "connected")
	errCh := make(chan error, 1)
	go func() {
		errCh <- (&deploy.Runner{DB: s.db, Box: s.box}).StreamLogs(r.Context(), a, n, tail, stream)
	}()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-errCh:
			if e != nil && r.Context().Err() == nil {
				_ = stream.event("stream-error", e.Error())
			}
			return
		case <-ticker.C:
			if stream.ping() != nil {
				return
			}
		}
	}
}
func (s *Server) safeApp(a model.App) map[string]any {
	b, _ := json.Marshal(a)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	var env []model.EnvironmentVar
	_ = json.Unmarshal([]byte(a.EnvironmentJSON), &env)
	for i := range env {
		if env[i].Secret {
			env[i].Value = ""
		}
	}
	m["environment"] = env
	return m
}

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) {
	var a model.App
	if s.db.First(&a, "id = ?", r.PathValue("id")).Error != nil {
		fail(w, 404, "app not found")
		return
	}
	lockAny, _ := s.locks.LoadOrStore(a.ID, &sync.Mutex{})
	mu := lockAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	var n model.Node
	if s.db.First(&n, "id = ?", a.NodeID).Error != nil {
		fail(w, 409, "deployment node not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	logs, e := (&deploy.Runner{DB: s.db, Box: s.box}).Remove(ctx, a, n)
	if e != nil {
		fail(w, 502, fmt.Sprintf("cannot remove application from node: %v; %s", e, logs))
		return
	}
	e = s.db.Transaction(func(tx *gorm.DB) error {
		if x := tx.Where("app_id = ?", a.ID).Delete(&model.Release{}); x.Error != nil {
			return x.Error
		}
		if x := tx.Where("app_id = ?", a.ID).Delete(&model.Build{}); x.Error != nil {
			return x.Error
		}
		if x := tx.Where("resource_id = ?", a.ID).Delete(&model.Task{}); x.Error != nil {
			return x.Error
		}
		return tx.Delete(&a).Error
	})
	if e != nil {
		fail(w, 500, e)
		return
	}
	s.locks.Delete(a.ID)
	w.WriteHeader(http.StatusNoContent)
}

type appUpdateInput struct {
	NodeID            string                 `json:"node_id"`
	RuntimeVersion    string                 `json:"runtime_version"`
	HostPort          int                    `json:"host_port"`
	ContainerPort     int                    `json:"container_port"`
	RestartPolicy     string                 `json:"restart_policy"`
	HostAccessEnabled *bool                  `json:"host_access_enabled"`
	Environment       []model.EnvironmentVar `json:"environment"`
	Volumes           []model.Volume         `json:"volumes"`
	Health            *model.HealthCheck     `json:"health"`
}

var runtimeVersions = map[string]map[string]bool{
	"vue":    {"16": true, "18": true, "20": true, "22": true, "24": true},
	"python": {"3.9": true, "3.10": true, "3.11": true, "3.12": true, "3.13": true},
	"java":   {"8": true, "11": true, "17": true, "21": true, "22": true},
	"go": {
		"go.mod": true,
		"1.20":   true,
		"1.21":   true,
		"1.22":   true,
		"1.23":   true,
		"1.24":   true,
	},
}

var defaultRuntimeVersions = map[string]string{
	"vue":    "22",
	"python": "3.13",
	"java":   "8",
	"go":     "go.mod",
}

func validateRuntime(appType, version string) (string, error) {
	if version == "" {
		version = defaultRuntimeVersions[appType]
	}
	if !runtimeVersions[appType][version] {
		return "", fmt.Errorf("unsupported %s runtime version %q", appType, version)
	}
	return version, nil
}

func (s *Server) updateApp(w http.ResponseWriter, r *http.Request) {
	var a model.App
	if s.db.First(&a, "id = ?", r.PathValue("id")).Error != nil {
		fail(w, 404, "app not found")
		return
	}
	var in appUpdateInput
	if e := decode(r, &in); e != nil {
		fail(w, 400, e)
		return
	}
	if in.NodeID == "" {
		in.NodeID = a.NodeID
	}
	if in.RuntimeVersion == "" {
		in.RuntimeVersion = a.RuntimeVersion
	}
	runtimeVersion, e := validateRuntime(a.Type, in.RuntimeVersion)
	if e != nil {
		fail(w, 400, e)
		return
	}
	var n model.Node
	if s.db.First(&n, "id = ?", in.NodeID).Error != nil {
		fail(w, 400, "node not found")
		return
	}
	if in.HostPort < 1 || in.ContainerPort < 1 {
		fail(w, 400, "ports are required")
		return
	}
	var count int64
	s.db.Model(&model.App{}).
		Where("node_id = ? AND host_port = ? AND id <> ?", in.NodeID, in.HostPort, a.ID).
		Count(&count)
	if count > 0 {
		fail(w, 409, "host port already used on node")
		return
	}
	if in.Volumes != nil {
		if e := deploy.ValidateVolumes(n, in.Volumes); e != nil {
			fail(w, 400, e)
			return
		}
		raw, _ := json.Marshal(in.Volumes)
		a.VolumesJSON = string(raw)
	}
	if in.Environment != nil {
		var old []model.EnvironmentVar
		_ = json.Unmarshal([]byte(a.EnvironmentJSON), &old)
		oldByKey := map[string]model.EnvironmentVar{}
		for _, v := range old {
			oldByKey[v.Key] = v
		}
		for i := range in.Environment {
			if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(in.Environment[i].Key) {
				fail(w, 400, "invalid environment variable key")
				return
			}
			if in.Environment[i].Secret {
				if in.Environment[i].Value == "" && oldByKey[in.Environment[i].Key].Secret {
					in.Environment[i].Value = oldByKey[in.Environment[i].Key].Value
				} else {
					v, e := s.box.Seal(in.Environment[i].Value)
					if e != nil {
						fail(w, 500, e)
						return
					}
					in.Environment[i].Value = "enc:" + v
				}
			}
		}
		raw, _ := json.Marshal(in.Environment)
		a.EnvironmentJSON = string(raw)
	}
	if in.Health != nil {
		raw, _ := json.Marshal(in.Health)
		a.HealthJSON = string(raw)
	}
	a.NodeID = in.NodeID
	a.RuntimeVersion = runtimeVersion
	a.HostPort = in.HostPort
	a.ContainerPort = in.ContainerPort
	if in.HostAccessEnabled != nil {
		a.HostAccessEnabled = *in.HostAccessEnabled
	}
	if map[string]bool{"always": true, "unless-stopped": true, "on-failure": true, "no": true}[in.RestartPolicy] {
		a.RestartPolicy = in.RestartPolicy
	}
	if e := s.db.Save(&a).Error; e != nil {
		fail(w, 409, e)
		return
	}
	jsonOut(w, 200, s.safeApp(a))
}
func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	var in appInput
	if e := decode(r, &in); e != nil {
		fail(w, 400, e)
		return
	}
	if !appNameRE.MatchString(in.Name) {
		fail(
			w,
			400,
			"application name must start with a lowercase letter and contain only lowercase letters, numbers, and hyphens (max 50 characters)",
		)
		return
	}
	owner, repo, e := repoParts(in.RepoURL)
	if e != nil {
		fail(w, 400, e)
		return
	}
	if !map[string]bool{"vue": true, "python": true, "java": true, "go": true}[in.Type] {
		fail(w, 400, "invalid app type")
		return
	}
	runtimeVersion, e := validateRuntime(in.Type, in.RuntimeVersion)
	if e != nil {
		fail(w, 400, e)
		return
	}
	var n model.Node
	if s.db.First(&n, "id = ?", in.NodeID).Error != nil {
		fail(w, 400, "node not found")
		return
	}
	if e = deploy.ValidateVolumes(n, in.Volumes); e != nil {
		fail(w, 400, e)
		return
	}
	if in.HostPort < 1 || in.ContainerPort < 1 {
		fail(w, 400, "ports are required")
		return
	}
	var count int64
	s.db.Model(&model.App{}).
		Where("node_id = ? AND host_port = ?", in.NodeID, in.HostPort).
		Count(&count)
	if count > 0 {
		fail(w, 409, "host port already used on node")
		return
	}
	token, e := secure.Token()
	if e != nil {
		fail(w, 500, e)
		return
	}
	encToken, e := s.box.Seal(token)
	if e != nil {
		fail(w, 500, e)
		return
	}
	for i := range in.Environment {
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(in.Environment[i].Key) {
			fail(w, 400, "invalid environment variable key")
			return
		}
		if in.Environment[i].Secret {
			v, e := s.box.Seal(in.Environment[i].Value)
			if e != nil {
				fail(w, 500, e)
				return
			}
			in.Environment[i].Value = "enc:" + v
		}
	}
	ej, _ := json.Marshal(in.Environment)
	vj, _ := json.Marshal(in.Volumes)
	hj, _ := json.Marshal(in.Health)
	if in.DockerfilePath == "" {
		in.DockerfilePath = "Dockerfile"
	}
	if in.BuildContext == "" {
		in.BuildContext = "."
	}
	if in.RestartPolicy == "" {
		in.RestartPolicy = "unless-stopped"
	}
	a := model.App{
		Base:                 model.Base{ID: id()},
		Name:                 in.Name,
		Slug:                 slug(in.Name),
		Type:                 in.Type,
		RuntimeVersion:       runtimeVersion,
		RepoURL:              in.RepoURL,
		RepoOwner:            owner,
		RepoName:             repo,
		Branch:               in.Branch,
		DockerfilePath:       in.DockerfilePath,
		BuildContext:         in.BuildContext,
		NodeID:               in.NodeID,
		HostPort:             in.HostPort,
		ContainerPort:        in.ContainerPort,
		RestartPolicy:        in.RestartPolicy,
		HostAccessEnabled:    in.HostAccessEnabled,
		EnvironmentJSON:      string(ej),
		VolumesJSON:          string(vj),
		HealthJSON:           string(hj),
		CallbackSecret:       encToken,
		InitialDeployPending: true,
		Status:               "initializing",
	}
	if e = s.db.Create(&a).Error; e != nil {
		fail(w, 409, e)
		return
	}
	t := model.Task{
		Base:       model.Base{ID: id()},
		Kind:       "initialize_app",
		ResourceID: a.ID,
		Status:     "queued",
	}
	s.db.Create(&t)
	branchLabel := a.Branch
	if branchLabel == "" {
		branchLabel = "默认分支"
	}
	go s.notifyDingTalk(
		"Luna PaaS Cloud · 新建部署",
		fmt.Sprintf(
			"### 🚀 新建部署\n\n> **应用：** %s\n\n- 类型：`%s %s`\n- 仓库：`%s/%s`\n- 分支：`%s`\n- 状态：正在初始化构建工作流\n\n[打开 Luna PaaS Cloud](%s/apps/%s)",
			a.Name,
			a.Type,
			a.RuntimeVersion,
			a.RepoOwner,
			a.RepoName,
			branchLabel,
			strings.TrimRight(s.cfg.FrontendURL, "/"),
			a.ID,
		),
	)
	go s.initializeApp(a.ID, t.ID, token)
	jsonOut(w, 202, map[string]any{"app": s.safeApp(a), "task_id": t.ID})
}

func (s *Server) initializeApp(appID, taskID, callbackToken string) {
	s.task(taskID, "running", "")
	var a model.App
	if e := s.db.First(&a, "id = ?", appID).Error; e != nil {
		s.task(taskID, "failed", e.Error())
		return
	}
	pat, e := s.setting("github_token")
	if e != nil {
		s.initFailed(a.ID, taskID, "GitHub token is not configured")
		return
	}
	user, e := s.setting("acr_username")
	if e != nil {
		s.initFailed(a.ID, taskID, "ACR username is not configured")
		return
	}
	pass, e := s.setting("acr_password")
	if e != nil {
		s.initFailed(a.ID, taskID, "ACR password is not configured")
		return
	}
	g := gh.New(pat)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	repo, e := g.Repo(ctx, a.RepoOwner, a.RepoName)
	if e != nil {
		s.initFailed(
			a.ID,
			taskID,
			fmt.Sprintf(
				"cannot access GitHub repository %s/%s; verify the URL and that the fine-grained PAT includes this repository: %v",
				a.RepoOwner,
				a.RepoName,
				e,
			),
		)
		return
	}
	if a.Branch == "" {
		a.Branch = repo.DefaultBranch
	}
	if _, e = g.Content(ctx, a.RepoOwner, a.RepoName, a.DockerfilePath, a.Branch); e != nil {
		s.initFailed(a.ID, taskID, "Dockerfile not found or inaccessible: "+e.Error())
		return
	}
	oldSHA := ""
	if old, e := g.Content(ctx, a.RepoOwner, a.RepoName, ".github/workflows/paas-build.yml", a.Branch); e == nil {
		txt, _ := gh.DecodeContent(old)
		if !gh.Managed(txt) {
			s.initFailed(a.ID, taskID, "existing paas-build.yml is not managed by PaaS")
			return
		}
		oldSHA = old.SHA
	}
	if s.cfg.PublicURL == "" {
		s.initFailed(a.ID, taskID, "PAAS_PUBLIC_URL is required")
		return
	}
	imageRepository := fmt.Sprintf(
		"%s/%s/%s",
		s.cfg.Registry,
		s.cfg.RegistryNamespace,
		strings.ToLower(a.RepoName),
	)
	secrets := map[string]string{
		"PAAS_ACR_REGISTRY":     s.cfg.Registry,
		"PAAS_IMAGE_REPOSITORY": imageRepository,
		"PAAS_ACR_USERNAME":     user,
		"PAAS_ACR_PASSWORD":     pass,
		"PAAS_CALLBACK_TOKEN":   callbackToken,
	}
	for k, v := range secrets {
		if e = g.PutSecret(ctx, a.RepoOwner, a.RepoName, k, v); e != nil {
			s.initFailed(
				a.ID,
				taskID,
				fmt.Sprintf(
					"cannot write GitHub Actions secret %s; grant Actions secrets write permission to the PAT: %v",
					k,
					e,
				),
			)
			return
		}
	}
	runtimeVersion, e := validateRuntime(a.Type, a.RuntimeVersion)
	if e != nil {
		s.initFailed(a.ID, taskID, e.Error())
		return
	}
	if a.RuntimeVersion == "" {
		a.RuntimeVersion = runtimeVersion
		s.db.Save(&a)
	}
	wf := gh.Workflow(
		gh.WorkflowInput{
			AppID:          a.ID,
			AppType:        a.Type,
			RuntimeVersion: runtimeVersion,
			Branch:         a.Branch,
			Dockerfile:     a.DockerfilePath,
			Context:        a.BuildContext,
			CallbackURL:    s.cfg.PublicURL + "/api/callbacks/github/actions/" + a.ID,
		},
	)
	sha, e := g.PutWorkflow(ctx, a.RepoOwner, a.RepoName, a.Branch, wf, oldSHA)
	if e != nil {
		s.initFailed(
			a.ID,
			taskID,
			fmt.Sprintf(
				"cannot write .github/workflows/paas-build.yml; grant Contents and Workflows write permissions to the PAT: %v",
				e,
			),
		)
		return
	}
	a.WorkflowSHA = sha
	s.db.Save(&a)
	for i := 0; i < 5; i++ {
		if e = g.Dispatch(ctx, a.RepoOwner, a.RepoName, a.Branch); e == nil {
			break
		}
		time.Sleep(time.Duration(i+1) * time.Second)
	}
	if e != nil {
		s.initFailed(
			a.ID,
			taskID,
			fmt.Sprintf(
				"workflow was created but the initial workflow_dispatch failed; grant Actions write permission and verify Actions is enabled: %v",
				e,
			),
		)
		return
	}
	s.db.Model(&model.App{}).
		Where("id = ?", a.ID).
		Updates(map[string]any{"status": "awaiting_build", "last_error": ""})
	s.task(taskID, "succeeded", "")
}
func (s *Server) initFailed(appID, taskID, msg string) {
	s.db.Model(&model.App{}).
		Where("id = ?", appID).
		Updates(map[string]any{"status": "error", "last_error": msg})
	s.task(taskID, "failed", msg)
}
func (s *Server) retryInitialize(w http.ResponseWriter, r *http.Request) {
	var a model.App
	if s.db.First(&a, "id = ?", r.PathValue("id")).Error != nil {
		fail(w, 404, "app not found")
		return
	}
	token, e := s.box.Open(a.CallbackSecret)
	if e != nil {
		fail(w, 500, e)
		return
	}
	a.Status = "initializing"
	a.LastError = ""
	s.db.Save(&a)
	t := model.Task{
		Base:       model.Base{ID: id()},
		Kind:       "initialize_app",
		ResourceID: a.ID,
		Status:     "queued",
	}
	s.db.Create(&t)
	go s.initializeApp(a.ID, t.ID, token)
	jsonOut(w, 202, map[string]string{"task_id": t.ID})
}
func (s *Server) task(taskID, status, errText string) {
	s.db.Model(&model.Task{}).
		Where("id = ?", taskID).
		Updates(map[string]any{"status": status, "error": errText})
}

func (s *Server) listBuilds(w http.ResponseWriter, r *http.Request) {
	var x []model.Build
	s.db.Where("app_id = ?", r.PathValue("id")).Order("created_at desc").Find(&x)
	jsonOut(w, 200, x)
}
func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) {
	var x []model.Release
	s.db.Where("app_id = ?", r.PathValue("id")).Order("created_at desc").Find(&x)
	jsonOut(w, 200, x)
}
func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	var x model.Task
	if s.db.First(&x, "id = ?", r.PathValue("id")).Error != nil {
		fail(w, 404, "task not found")
		return
	}
	jsonOut(w, 200, x)
}

type callbackInput struct {
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
	CommitSHA  string `json:"commit_sha"`
	Image      string `json:"image"`
	Status     string `json:"status"`
	HTMLURL    string `json:"html_url"`
	Initial    bool   `json:"initial"`
	RunID      int64  `json:"run_id"`
	RunAttempt int    `json:"run_attempt"`
}

func (s *Server) buildCallback(w http.ResponseWriter, r *http.Request) {
	var a model.App
	if s.db.First(&a, "id = ?", r.PathValue("appId")).Error != nil {
		fail(w, 404, "app not found")
		return
	}
	expected, e := s.box.Open(a.CallbackSecret)
	if e != nil {
		fail(w, 500, "callback secret unavailable")
		return
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
		fail(w, 401, "invalid callback token")
		return
	}
	var in callbackInput
	if e = decode(r, &in); e != nil {
		fail(w, 400, e)
		return
	}
	if !strings.EqualFold(in.Repository, a.RepoOwner+"/"+a.RepoName) || in.Ref != a.Branch ||
		in.RunID < 1 {
		fail(w, 400, "callback metadata mismatch")
		return
	}
	status := map[string]string{"success": "succeeded", "failure": "failed", "cancelled": "failed"}[in.Status]
	if status == "" {
		status = "failed"
	}
	b := model.Build{
		Base:       model.Base{ID: id()},
		AppID:      a.ID,
		RunID:      in.RunID,
		RunAttempt: in.RunAttempt,
		CommitSHA:  in.CommitSHA,
		Ref:        in.Ref,
		Image:      in.Image,
		Status:     status,
		Initial:    in.Initial,
		HTMLURL:    in.HTMLURL,
	}
	res := s.db.Where("app_id = ? AND run_id = ? AND run_attempt = ?", a.ID, in.RunID, in.RunAttempt).
		FirstOrCreate(&b)
	if res.Error != nil {
		fail(w, 500, res.Error)
		return
	}
	if b.Status == "succeeded" && in.Initial && a.InitialDeployPending {
		a.InitialDeployPending = false
		s.db.Save(&a)
		t := s.newRelease(a, b, "")
		go s.executeRelease(t.ID)
	}
	if b.Status == "failed" {
		s.db.Model(&model.App{}).
			Where("id = ?", a.ID).
			Updates(map[string]any{"status": "build_failed", "last_error": "GitHub Actions build failed"})
	} else {
		s.db.Model(&model.App{}).Where("id = ?", a.ID).Update("status", "ready")
	}
	jsonOut(w, 200, map[string]any{"build_id": b.ID, "status": b.Status})
}
func (s *Server) createRelease(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BuildID string `json:"build_id"`
	}
	if e := decode(r, &in); e != nil {
		fail(w, 400, e)
		return
	}
	var a model.App
	if s.db.First(&a, "id = ?", r.PathValue("id")).Error != nil {
		fail(w, 404, "app not found")
		return
	}
	var b model.Build
	if s.db.Where("id = ? AND app_id = ? AND status = ?", in.BuildID, a.ID, "succeeded").
		First(&b).
		Error != nil {
		fail(w, 400, "successful build not found")
		return
	}
	rel := s.newRelease(a, b, "")
	go s.executeRelease(rel.ID)
	jsonOut(w, 202, rel)
}
func (s *Server) newRelease(a model.App, b model.Build, rollbackOf string) model.Release {
	rel := model.Release{
		Base:              model.Base{ID: id()},
		AppID:             a.ID,
		BuildID:           b.ID,
		Image:             b.Image,
		Status:            "queued",
		PreviousReleaseID: a.ActiveReleaseID,
		RollbackOf:        rollbackOf,
	}
	s.db.Create(&rel)
	action := "发布版本"
	if rollbackOf != "" {
		action = "回滚版本"
	}
	go s.notifyDingTalk(
		"Luna PaaS Cloud · "+action,
		fmt.Sprintf(
			"### 📦 %s\n\n> **应用：** %s\n\n- 版本：`%s`\n- 状态：已进入发布队列\n\n[查看发布进度](%s/apps/%s)",
			action,
			a.Name,
			shortSHA(b.CommitSHA),
			strings.TrimRight(s.cfg.FrontendURL, "/"),
			a.ID,
		),
	)
	return rel
}

func shortSHA(v string) string {
	if len(v) > 8 {
		return v[:8]
	}
	return v
}
func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	var target model.Release
	if s.db.Where("id = ? AND app_id = ? AND status = ?", r.PathValue("release"), r.PathValue("id"), "succeeded").
		First(&target).
		Error != nil {
		fail(w, 404, "successful release not found")
		return
	}
	var a model.App
	s.db.First(&a, "id = ?", r.PathValue("id"))
	var b model.Build
	s.db.First(&b, "id = ?", target.BuildID)
	rel := s.newRelease(a, b, target.ID)
	rel.ConfigSnapshot = target.ConfigSnapshot
	s.db.Save(&rel)
	go s.executeRelease(rel.ID)
	jsonOut(w, 202, rel)
}
func (s *Server) executeRelease(releaseID string) {
	var rel model.Release
	if s.db.First(&rel, "id = ?", releaseID).Error != nil {
		return
	}
	lockAny, _ := s.locks.LoadOrStore(rel.AppID, &sync.Mutex{})
	mu := lockAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if s.db.First(&rel, "id = ?", releaseID).Error != nil {
		return
	}
	s.db.Model(&rel).Update("status", "running")
	var a model.App
	var n model.Node
	if s.db.First(&a, "id = ?", rel.AppID).Error != nil ||
		s.db.First(&n, "id = ?", a.NodeID).Error != nil {
		return
	}
	runner, e := s.runner()
	if e != nil {
		s.failRelease(&rel, e.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var logs, snap string
	if rel.ConfigSnapshot != "" {
		logs, e = runner.Restore(ctx, a, n, rel.ConfigSnapshot)
		snap = rel.ConfigSnapshot
	} else {
		logs, snap, e = runner.Deploy(ctx, a, n, rel.Image)
	}
	rel.Logs = logs
	rel.ConfigSnapshot = snap
	if e == nil {
		rel.Status = "succeeded"
		s.db.Save(&rel)
		a.ActiveReleaseID = rel.ID
		a.Status = "running"
		a.LastError = ""
		s.db.Save(&a)
		go s.notifyDingTalk(
			"Luna PaaS Cloud · 发布成功",
			fmt.Sprintf(
				"### ✅ 发布成功\n\n> **应用：** %s\n\n- 镜像版本：`%s`\n- 状态：运行中\n\n[查看应用](%s/apps/%s)",
				a.Name,
				shortSHA(imageTag(rel.Image)),
				strings.TrimRight(s.cfg.FrontendURL, "/"),
				a.ID,
			),
		)
		return
	}
	rel.Status = "failed"
	rel.Logs += "\nERROR: " + e.Error()
	s.db.Save(&rel)
	s.db.Model(&model.App{}).
		Where("id = ?", a.ID).
		Updates(map[string]any{"status": "release_failed", "last_error": e.Error()})
	go s.notifyDingTalk(
		"Luna PaaS Cloud · 发布失败",
		fmt.Sprintf(
			"### ❌ 发布失败\n\n> **应用：** %s\n\n- 状态：发布失败，正在检查回滚\n\n[查看部署日志](%s/apps/%s)",
			a.Name,
			strings.TrimRight(s.cfg.FrontendURL, "/"),
			a.ID,
		),
	)
	if rel.PreviousReleaseID != "" {
		var prev model.Release
		if s.db.First(&prev, "id = ?", rel.PreviousReleaseID).Error == nil {
			rollbackLogs, re := runner.Restore(ctx, a, n, prev.ConfigSnapshot)
			rel.Logs += "\nAUTO ROLLBACK:\n" + rollbackLogs
			if re != nil {
				rel.Logs += "\nROLLBACK FAILED: " + re.Error()
			}
			s.db.Save(&rel)
		}
	}
}

func imageTag(image string) string {
	if i := strings.LastIndex(image, ":"); i >= 0 {
		return image[i+1:]
	}
	return image
}
func (s *Server) failRelease(r *model.Release, msg string) {
	r.Status = "failed"
	r.Logs = msg
	s.db.Save(r)
}
func (s *Server) runner() (*deploy.Runner, error) {
	u, e := s.setting("acr_username")
	if e != nil {
		return nil, e
	}
	p, e := s.setting("acr_password")
	if e != nil {
		return nil, e
	}
	return &deploy.Runner{
		DB:               s.db,
		Box:              s.box,
		Registry:         s.cfg.Registry,
		RegistryUser:     u,
		RegistryPassword: p,
	}, nil
}
func (s *Server) syncBuilds(w http.ResponseWriter, r *http.Request) {
	var a model.App
	if s.db.First(&a, "id = ?", r.PathValue("id")).Error != nil {
		fail(w, 404, "app not found")
		return
	}
	pat, e := s.setting("github_token")
	if e != nil {
		fail(w, 400, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	runs, e := gh.New(pat).Runs(ctx, a.RepoOwner, a.RepoName, a.Branch)
	if e != nil {
		fail(w, 502, e)
		return
	}
	created := 0
	for _, run := range runs {
		if run.Status != "completed" {
			continue
		}
		status := "failed"
		if run.Conclusion == "success" {
			status = "succeeded"
		}
		image := fmt.Sprintf(
			"%s/%s/%s:%s-%d",
			s.cfg.Registry,
			s.cfg.RegistryNamespace,
			strings.ToLower(a.RepoName),
			run.HeadSHA,
			run.ID,
		)
		b := model.Build{
			Base:       model.Base{ID: id()},
			AppID:      a.ID,
			RunID:      run.ID,
			RunAttempt: run.RunAttempt,
			CommitSHA:  run.HeadSHA,
			Ref:        run.HeadBranch,
			Image:      image,
			Status:     status,
			HTMLURL:    run.HTMLURL,
		}
		res := s.db.Where("app_id = ? AND run_id = ? AND run_attempt = ?", a.ID, run.ID, run.RunAttempt).
			FirstOrCreate(&b)
		if res.Error == nil && res.RowsAffected > 0 {
			created++
		}
	}
	jsonOut(w, 200, map[string]int{"created": created})
}
