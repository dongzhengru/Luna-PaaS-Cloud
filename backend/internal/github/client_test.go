package github

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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
		"name: Luna PaaS Cloud Build",
		"workflow_dispatch",
		"[paas-skip]",
		"secrets.PAAS_ACR_REGISTRY",
		"secrets.PAAS_IMAGE_REPOSITORY",
		"PAAS_ACR_PASSWORD",
		"PAAS_CALLBACK_TOKEN",
		"provenance: false",
		"sbom: false",
		"if: always()",
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
