package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"paas.local/backend/internal/config"
	"paas.local/backend/internal/model"
	"paas.local/backend/internal/secure"
	"paas.local/backend/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	db, err := gorm.Open(mysql.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	err = db.AutoMigrate(
		&model.AllowedUser{},
		&model.Session{},
		&model.OAuthState{},
		&model.SecretSetting{},
		&model.Node{},
		&model.App{},
		&model.Build{},
		&model.Release{},
		&model.Task{},
	)
	if err != nil {
		log.Fatal(err)
	}
	defaultRuntimeVersions := map[string]string{
		"vue":    "22",
		"python": "3.13",
		"java":   "8",
		"go":     "go.mod",
	}
	for appType, version := range defaultRuntimeVersions {
		err := db.Model(&model.App{}).
			Where("type = ? AND (runtime_version = '' OR runtime_version IS NULL)", appType).
			Update("runtime_version", version).
			Error
		if err != nil {
			log.Fatal(err)
		}
	}
	box, err := secure.New(cfg.MasterKey)
	if err != nil {
		log.Fatal(err)
	}
	h := server.New(cfg, db, box)
	s := &http.Server{
		Addr:              cfg.Addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Printf("Luna PaaS Cloud listening on %s", cfg.Addr)
		if e := s.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			log.Fatal(e)
		}
	}()
	<-ctx.Done()
	_ = s.Close()
}
