package deploy

import (
	"paas.local/backend/internal/model"
	"testing"
)

func TestValidateVolumes(t *testing.T) {
	n := model.Node{AllowedMountRoots: "/srv/paas,/data/apps"}
	ok := []model.Volume{
		{Type: "named", Source: "db-data", Target: "/var/lib/db"},
		{Type: "bind", Source: "/srv/paas/demo", Target: "/data"},
	}
	if e := ValidateVolumes(n, ok); e != nil {
		t.Fatal(e)
	}
	bad := []model.Volume{{Type: "bind", Source: "/var/run/docker.sock", Target: "/socket"}}
	if e := ValidateVolumes(n, bad); e == nil {
		t.Fatal("docker socket mount accepted")
	}
	outside := []model.Volume{{Type: "bind", Source: "/etc", Target: "/etc"}}
	if e := ValidateVolumes(n, outside); e == nil {
		t.Fatal("outside mount root accepted")
	}
}

func TestParseContainerStats(t *testing.T) {
	state := `{"Status":"running","Running":true,"OOMKilled":false,"ExitCode":0,"StartedAt":"2026-08-28T01:02:03Z","Health":{"Status":"healthy"}}`
	stats := `{"CPUPerc":"1.25%","MemUsage":"64MiB / 1GiB","MemPerc":"6.25%","NetIO":"1kB / 2kB","BlockIO":"3kB / 4kB","PIDs":"7"}`
	got, err := parseContainerStats("demo", state, "2\n", stats)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContainerName != "demo" || got.Status != "running" || got.Health != "healthy" ||
		got.RestartCount != 2 {
		t.Fatalf("unexpected state: %#v", got)
	}
	if got.CPUPercent != "1.25%" || got.MemoryUsage != "64MiB / 1GiB" || got.PIDs != "7" {
		t.Fatalf("unexpected resources: %#v", got)
	}
}

func TestParseStoppedContainerStats(t *testing.T) {
	state := `{"Status":"exited","Running":false,"OOMKilled":true,"ExitCode":137,"StartedAt":"2026-08-28T01:02:03Z"}`
	got, err := parseContainerStats("demo", state, "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Running || !got.OOMKilled || got.ExitCode != 137 || got.CPUPercent != "" {
		t.Fatalf("unexpected stopped state: %#v", got)
	}
}
