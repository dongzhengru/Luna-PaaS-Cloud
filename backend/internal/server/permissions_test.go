package server

import (
	"bytes"
	"context"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"os"
	"paas.local/backend/internal/config"
	"paas.local/backend/internal/secure"
	"strings"
	"testing"
	"time"

	"paas.local/backend/internal/model"
)

func TestUserRoutePermissions(t *testing.T) {
	tests := []struct {
		method, path string
		allowed      bool
		appID        string
	}{
		{"GET", "/api/apps", true, ""},
		{"HEAD", "/api/apps", true, ""},
		{"POST", "/api/apps", false, ""},
		{"GET", "/api/apps/app-a", true, "app-a"},
		{"PUT", "/api/apps/app-a", true, "app-a"},
		{"DELETE", "/api/apps/app-a", false, "app-a"},
		{"POST", "/api/apps/app-a/releases/release-a/rollback", true, "app-a"},
		{"GET", "/api/apps/app-a/builds/build-a/logs", true, "app-a"},
		{"POST", "/api/apps/app-a/compose/actions", true, "app-a"},
		{"GET", "/api/apps/app-a/compose/versions/version-a", true, "app-a"},
		{"GET", "/api/tasks/task-a", true, ""},
		{"POST", "/api/tasks/task-a", false, ""},
		{"GET", "/api/nodes", true, ""},
		{"POST", "/api/nodes", false, ""},
		{"POST", "/api/nodes/node-a/test", false, ""},
		{"GET", "/api/settings", false, ""},
		{"PUT", "/api/settings", false, ""},
		{"GET", "/api/users", false, ""},
		{"PUT", "/api/users/user-a/apps", false, ""},
		{"GET", "/api/github/repos", false, ""},
		{"POST", "/api/compose/apps", false, ""},
		{"POST", "/api/compose/validate", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			allowed, appID := userRoute(tt.method, tt.path)
			if allowed != tt.allowed || appID != tt.appID {
				t.Fatalf("got %v %q, want %v %q", allowed, appID, tt.allowed, tt.appID)
			}
		})
	}
}

func TestAuthorizeAdminAndDeniedRoutes(t *testing.T) {
	s := &Server{}
	for _, role := range []string{"admin", "user", ""} {
		for _, path := range []string{"/api/settings", "/api/users/user-a/apps", "/api/apps/app-a"} {
			req := httptest.NewRequest("DELETE", path, nil)
			req = req.WithContext(context.WithValue(req.Context(), userContextKey{}, model.AllowedUser{Role: role}))
			res := httptest.NewRecorder()
			allowed := s.authorize(res, req)
			if allowed != (role == "admin") {
				t.Fatalf("role %q path %s: allowed %v", role, path, allowed)
			}
			if !allowed && res.Code != 403 {
				t.Fatalf("got %d", res.Code)
			}
		}
	}
	if s.canManage(model.AllowedUser{}, "app-a") {
		t.Fatal("anonymous user was granted access")
	}
	if s.canManage(model.AllowedUser{Base: model.Base{ID: "user-a"}}, "") {
		t.Fatal("empty app granted access")
	}
}

// Runs against the same explicitly configured disposable database as Compose tests,
// but does not require Docker or perform any deployment operations.
func TestPermissionsAPIIntegration(t *testing.T) {
	dsn := os.Getenv("PAAS_COMPOSE_TEST_DSN")
	if dsn == "" {
		t.Skip("set PAAS_COMPOSE_TEST_DSN to an isolated MySQL test database")
	}
	parsed, err := mysqlDriver.ParseDSN(dsn)
	if err != nil || parsed.DBName != "paas_compose_smoke" {
		t.Fatal("requires database named paas_compose_smoke")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.AllowedUser{}, &model.AppPermission{}, &model.Session{}, &model.App{}, &model.Node{}, &model.Task{}, &model.ComposeVersion{}); err != nil {
		t.Fatal(err)
	}
	box, err := secure.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	admin := model.AllowedUser{Base: model.Base{ID: id()}, Phone: id()[:20], Role: "admin", Enabled: true}
	user := model.AllowedUser{Base: model.Base{ID: id()}, Phone: id()[:20], Role: "user", Enabled: true}
	node := model.Node{Base: model.Base{ID: id()}, Name: id()}
	draft, err := box.Seal(`{"yaml":"services: {web: {image: nginx}}"}`)
	if err != nil {
		t.Fatal(err)
	}
	app := model.App{Base: model.Base{ID: id()}, Name: id(), Slug: id(), RepoURL: id(), NodeID: node.ID, DeploymentSource: "compose", ComposeDraft: draft}
	task := model.Task{Base: model.Base{ID: id()}, ResourceID: app.ID, Status: "success"}
	adminToken, userToken := id(), id()
	adminSession := model.Session{Base: model.Base{ID: id()}, Phone: admin.Phone, TokenHash: hash(adminToken), ExpiresAt: time.Now().Add(time.Hour)}
	userSession := model.Session{Base: model.Base{ID: id()}, Phone: user.Phone, TokenHash: hash(userToken), ExpiresAt: time.Now().Add(time.Hour)}
	for _, record := range []any{&admin, &user, &node, &app, &task, &adminSession, &userSession} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
		defer db.Delete(record)
	}
	defer db.Where("user_id = ?", user.ID).Delete(&model.AppPermission{})
	handler := New(config.Config{}, db, box)
	request := func(token, method, path, body string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: "paas_session", Value: token})
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, res.Code, want, res.Body.String())
		}
		return res.Body.String()
	}
	if raw := request(userToken, "GET", "/api/apps", "", 200); strings.Contains(raw, app.ID) {
		t.Fatal("unassigned application exposed")
	}
	request(userToken, "GET", "/api/apps/"+app.ID, "", 403)
	request(userToken, "GET", "/api/tasks/"+task.ID, "", 403)
	request(userToken, "PUT", "/api/users/"+user.ID+"/apps", `{"app_ids":[]}`, 403)
	body := `{"app_ids":["` + app.ID + `"]}`
	request(adminToken, "PUT", "/api/users/"+user.ID+"/apps", body, 200)
	if raw := request(userToken, "GET", "/api/apps", "", 200); !strings.Contains(raw, app.ID) {
		t.Fatal("assigned application missing")
	}
	request(userToken, "GET", "/api/apps/"+app.ID+"/compose", "", 200)
	request(userToken, "GET", "/api/tasks/"+task.ID, "", 200)
	request(userToken, "DELETE", "/api/apps/"+app.ID, "", 403)
	request(userToken, "POST", "/api/apps/"+app.ID+"/compose/actions", `{"action":"delete"}`, 403)
	request(userToken, "POST", "/api/compose/validate", `{"app_id":"unassigned","node_id":"`+node.ID+`"}`, 403)
	request(userToken, "GET", "/api/settings", "", 403)
	request(userToken, "POST", "/api/apps", `{}`, 403)
	request(adminToken, "PUT", "/api/users/"+user.ID+"/apps", `{"app_ids":[]}`, 200)
	request(userToken, "GET", "/api/apps/"+app.ID+"/compose", "", 403)
	request(userToken, "GET", "/api/tasks/"+task.ID, "", 403)
	request(adminToken, "PUT", "/api/users/"+user.ID, `{"role":"user","enabled":false}`, 200)
	request(userToken, "GET", "/api/apps", "", 401)
	request(userToken, "GET", "/api/auth/me", "", 401)
}
