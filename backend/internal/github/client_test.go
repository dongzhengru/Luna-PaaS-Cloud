package github

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestUnzipLogs(t *testing.T) {
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	for name, text := range map[string]string{"2_build.txt": "build output", "1_setup.txt": "setup output"} {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	logs, truncated, err := unzipLogs(archive.Bytes())
	if err != nil || truncated {
		t.Fatalf("got truncated=%v, err=%v", truncated, err)
	}
	if !strings.Contains(logs, "===== 1_setup.txt =====\nsetup output") || !strings.Contains(logs, "===== 2_build.txt =====\nbuild output") {
		t.Fatalf("unexpected logs: %q", logs)
	}
}

func TestRepos(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" || r.URL.Query().Get("per_page") != "100" {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatal("GitHub token was not sent")
		}
		_, _ = w.Write([]byte(`[{"name":"api","full_name":"octo/api","html_url":"https://github.com/octo/api","default_branch":"main","private":true}]`))
	}))
	defer server.Close()

	client := New("token")
	client.APIURL = server.URL
	repos, err := client.Repos(context.Background())
	if err != nil || len(repos) != 1 || repos[0].FullName != "octo/api" || !repos[0].Private {
		t.Fatalf("got %#v, %v", repos, err)
	}
}

func TestWorkflowContract(t *testing.T) {
	w := Workflow(
		WorkflowInput{
			AppID:          "a",
			AppType:        "python",
			RuntimeVersion: "3.13",
			Branch:         "main",
			Dockerfile:     "Dockerfile",
			Context:        ".",
			CallbackURL:    "https://paas.example/cb",
		},
	)
	wants := []string{
		marker,
		"name: Luna PaaS Cloud 应用构建",
		"workflow_dispatch",
		"[paas-skip]",
		"secrets.PAAS_ACR_REGISTRY",
		"secrets.PAAS_IMAGE_REPOSITORY",
		"PAAS_ACR_PASSWORD",
		"PAAS_CALLBACK_TOKEN",
		"provenance: false",
		"sbom: false",
		"if: always()",
		"name: 准备构建环境",
		"name: 构建并推送镜像",
		"name: 同步构建结果",
		"name: 检出源代码",
		"JOB_STATUS: ${{ needs.prepare.result",
		"https://paas.example/cb",
	}
	for _, want := range wants {
		if !strings.Contains(w, want) {
			t.Errorf("workflow missing %q", want)
		}
	}
	if strings.Contains(w, "registry.example") {
		t.Fatal("workflow must not contain a plaintext registry address")
	}
	var document any
	if err := yaml.Unmarshal([]byte(w), &document); err != nil {
		t.Fatalf("invalid workflow YAML: %v", err)
	}
}

func TestWorkflowUsesTypeSpecificBuild(t *testing.T) {
	tests := []struct {
		appType, runtimeVersion string
		wants                   []string
	}{
		{
			"vue",
			"24",
			[]string{
				"actions/setup-node@v4",
				`node-version: "24"`,
				"npm ci",
				"npm run build",
				"!dist/**",
			},
		},
		{
			"python",
			"3.12",
			[]string{
				"actions/setup-python@v5",
				`python-version: "3.12"`,
				"pip install -r requirements.txt",
				"python -m compileall .",
			},
		},
		{
			"java",
			"21",
			[]string{
				"actions/setup-java@v4",
				`java-version: "21"`,
				"./mvnw -B -DskipTests package",
				"mvn -B -DskipTests package",
				"!target/*.jar",
			},
		},
		{
			"go",
			"1.24",
			[]string{
				"actions/setup-go@v5",
				`go-version: "1.24"`,
				`go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./...`,
				`go build -trimpath -o app "${main_packages[0]}"`,
				"Expected exactly one Go main package",
				"!app",
			},
		},
		{"go", "go.mod", []string{`go-version-file: "./go.mod"`}},
	}
	for _, tt := range tests {
		t.Run(tt.appType, func(t *testing.T) {
			w := Workflow(
				WorkflowInput{
					AppType:        tt.appType,
					RuntimeVersion: tt.runtimeVersion,
					Branch:         "main",
					Dockerfile:     "Dockerfile",
					Context:        ".",
					CallbackURL:    "https://paas.example/cb",
				},
			)
			for _, want := range tt.wants {
				if !strings.Contains(w, want) {
					t.Errorf("workflow missing %q", want)
				}
			}
			var document any
			if err := yaml.Unmarshal([]byte(w), &document); err != nil {
				t.Fatalf("invalid workflow YAML: %v", err)
			}
		})
	}
}

func TestWorkflowAPIUsesFileNameNotContentPath(t *testing.T) {
	got := workflowEndpoint("owner", "repo", "/dispatches")
	want := "/repos/owner/repo/actions/workflows/paas-build.yml/dispatches"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.Contains(got, ".github/workflows") {
		t.Fatal("content path must not be used as workflow_id")
	}
}
