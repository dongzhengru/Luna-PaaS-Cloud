package server

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"paas.local/backend/internal/model"
)

type userContextKey struct{}

func (s *Server) currentUser(r *http.Request) model.AllowedUser {
	if user, ok := r.Context().Value(userContextKey{}).(model.AllowedUser); ok {
		return user
	}
	session, ok := s.session(r)
	if !ok {
		return model.AllowedUser{}
	}
	var user model.AllowedUser
	s.db.Where("phone = ? AND enabled = ?", session.Phone, true).First(&user)
	return user
}

func (s *Server) canManage(user model.AllowedUser, appID string) bool {
	if user.Role == "admin" {
		return true
	}
	if user.ID == "" || appID == "" {
		return false
	}
	var count int64
	return s.db.Model(&model.AppPermission{}).Where("user_id = ? AND app_id = ?", user.ID, appID).Count(&count).Error == nil && count > 0
}

// userRoute identifies the small set of routes available to ordinary users.
func userRoute(method, path string) (bool, string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if path == "/api/apps" || path == "/api/nodes" {
		return method == "GET" || method == "HEAD", ""
	}
	if path == "/api/compose/validate" {
		return method == "POST", ""
	}
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "apps" {
		return method != "DELETE", parts[2]
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "tasks" {
		return method == "GET" || method == "HEAD", ""
	}
	return false, ""
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) bool {
	user := s.currentUser(r)
	*r = *r.WithContext(context.WithValue(r.Context(), userContextKey{}, user))
	if user.Role == "admin" {
		return true
	}
	allowed, appID := userRoute(r.Method, r.URL.Path)
	if strings.HasPrefix(r.URL.Path, "/api/tasks/") && allowed {
		var task model.Task
		if s.db.First(&task, "id = ?", strings.TrimPrefix(r.URL.Path, "/api/tasks/")).Error != nil {
			fail(w, 404, "task not found")
			return false
		}
		appID = task.ResourceID
	}
	if !allowed || (appID != "" && !s.canManage(user, appID)) {
		fail(w, 403, "没有操作权限，请联系管理员分配应用权限")
		return false
	}
	return true
}

func (s *Server) userRoutes() {
	s.mux.HandleFunc("GET /api/users", s.listUsers)
	s.mux.HandleFunc("POST /api/users", s.createUser)
	s.mux.HandleFunc("PUT /api/users/{id}", s.updateUser)
	s.mux.HandleFunc("GET /api/users/{id}/apps", s.userApps)
	s.mux.HandleFunc("PUT /api/users/{id}/apps", s.assignApps)
}
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users := []model.AllowedUser{}
	if err := s.db.Order("created_at asc, id asc").Find(&users).Error; err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, users)
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone string `json:"phone"`
		Role  string `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	in.Phone = strings.TrimSpace(in.Phone)
	if in.Role == "" {
		in.Role = "user"
	}
	if !regexp.MustCompile(`^\+?[0-9]{6,20}$`).MatchString(in.Phone) || (in.Role != "admin" && in.Role != "user") {
		fail(w, 400, "手机号或角色无效")
		return
	}
	user := model.AllowedUser{Base: model.Base{ID: id()}, Phone: in.Phone, Role: in.Role, Enabled: true}
	if err := s.db.Create(&user).Error; err != nil {
		fail(w, 409, "添加失败，请检查手机号是否已存在")
		return
	}
	jsonOut(w, 201, user)
}
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role    string `json:"role"`
		Enabled *bool  `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if (in.Role != "admin" && in.Role != "user") || in.Enabled == nil {
		fail(w, 400, "请指定有效角色和启用状态")
		return
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Lock administrators in stable order to serialize demotion/disable operations.
		var admins []model.AllowedUser
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("role = ? AND enabled = ?", "admin", true).Order("id").Find(&admins).Error; err != nil {
			return err
		}
		var user model.AllowedUser
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", r.PathValue("id")).Error; err != nil {
			return err
		}
		if user.Role == "admin" && user.Enabled && len(admins) <= 1 && (in.Role != "admin" || !*in.Enabled) {
			return fmt.Errorf("至少保留一位启用的管理员")
		}
		return tx.Model(&user).Updates(map[string]any{"role": in.Role, "enabled": *in.Enabled}).Error
	})
	if err != nil {
		fail(w, 409, err)
		return
	}
	jsonOut(w, 200, map[string]bool{"saved": true})
}
func (s *Server) userApps(w http.ResponseWriter, r *http.Request) {
	var user model.AllowedUser
	if s.db.First(&user, "id = ?", r.PathValue("id")).Error != nil {
		fail(w, 404, "用户不存在")
		return
	}
	ids := []string{}
	if err := s.db.Model(&model.AppPermission{}).Where("user_id = ?", user.ID).Pluck("app_id", &ids).Error; err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 200, ids)
}
func (s *Server) assignApps(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AppIDs []string `json:"app_ids"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var user model.AllowedUser
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", r.PathValue("id")).Error; err != nil {
			return err
		}
		unique := map[string]bool{}
		for _, appID := range in.AppIDs {
			unique[appID] = true
		}
		var count int64
		if len(unique) > 0 {
			ids := []string{}
			for appID := range unique {
				ids = append(ids, appID)
			}
			if err := tx.Model(&model.App{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
				return err
			}
			if count != int64(len(unique)) {
				return fmt.Errorf("包含不存在的应用")
			}
		}
		if err := tx.Where("user_id = ?", user.ID).Delete(&model.AppPermission{}).Error; err != nil {
			return err
		}
		for appID := range unique {
			if err := tx.Create(&model.AppPermission{UserID: user.ID, AppID: appID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		fail(w, 400, err)
		return
	}
	jsonOut(w, 200, map[string]bool{"saved": true})
}
