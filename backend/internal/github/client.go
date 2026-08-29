package github

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/nacl/box"
)

const workflowPath = ".github/workflows/paas-build.yml"
const workflowFile = "paas-build.yml"
const marker = "# managed-by: luna-paas-cloud"
const legacyMarker = "# managed-by: zhengru-paas"

type Client struct {
	Token  string
	HTTP   *http.Client
	APIURL string
}
type Repo struct {
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}
type Content struct{ SHA, Content, Encoding string }
type PublicKey struct {
	KeyID string `json:"key_id"`
	Key   string `json:"key"`
}
type Run struct {
	ID           int64  `json:"id"`
	RunAttempt   int    `json:"run_attempt"`
	HeadSHA      string `json:"head_sha"`
	HeadBranch   string `json:"head_branch"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	HTMLURL      string `json:"html_url"`
	DisplayTitle string `json:"display_title"`
}

func New(token string) *Client {
	return &Client{Token: token, HTTP: &http.Client{Timeout: 25 * time.Second}, APIURL: "https://api.github.com"}
}
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		r = bytes.NewReader(b)
	}
	apiURL := strings.TrimRight(c.APIURL, "/")
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	req, e := http.NewRequestWithContext(ctx, method, apiURL+path, r)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github %s: %s", resp.Status, string(data))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
func (c *Client) Repo(ctx context.Context, owner, repo string) (Repo, error) {
	var x Repo
	e := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/%s", owner, repo), nil, &x)
	return x, e
}
func (c *Client) Repos(ctx context.Context) ([]Repo, error) {
	var repos []Repo
	for page := 1; page <= 10; page++ {
		var batch []Repo
		path := fmt.Sprintf("/user/repos?affiliation=owner,collaborator,organization&sort=full_name&direction=asc&per_page=100&page=%d", page)
		if e := c.do(ctx, "GET", path, nil, &batch); e != nil {
			return nil, e
		}
		repos = append(repos, batch...)
		if len(batch) < 100 {
			return repos, nil
		}
	}
	return repos, nil
}
func (c *Client) Content(ctx context.Context, owner, repo, path, ref string) (Content, error) {
	var x Content
	p := fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, path)
	if ref != "" {
		p += "?ref=" + url.QueryEscape(ref)
	}
	e := c.do(ctx, "GET", p, nil, &x)
	return x, e
}

func (c *Client) PutWorkflow(
	ctx context.Context,
	owner, repo, branch, content, oldSHA string,
) (string, error) {
	body := map[string]any{
		"message": "chore: configure Luna PaaS Cloud build [paas-skip]",
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":  branch,
	}
	if oldSHA != "" {
		body["sha"] = oldSHA
	}
	var x struct {
		Content struct {
			SHA string `json:"sha"`
		} `json:"content"`
	}
	e := c.do(
		ctx,
		"PUT",
		fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, workflowPath),
		body,
		&x,
	)
	return x.Content.SHA, e
}
func (c *Client) PutSecret(ctx context.Context, owner, repo, name, value string) error {
	var k PublicKey
	if e := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/%s/actions/secrets/public-key", owner, repo), nil, &k); e != nil {
		return e
	}
	raw, e := base64.StdEncoding.DecodeString(k.Key)
	if e != nil || len(raw) != 32 {
		return fmt.Errorf("invalid github public key")
	}
	var pub [32]byte
	copy(pub[:], raw)
	enc, e := box.SealAnonymous(nil, []byte(value), &pub, rand.Reader)
	if e != nil {
		return e
	}
	return c.do(
		ctx,
		"PUT",
		fmt.Sprintf("/repos/%s/%s/actions/secrets/%s", owner, repo, name),
		map[string]string{
			"encrypted_value": base64.StdEncoding.EncodeToString(enc),
			"key_id":          k.KeyID,
		},
		nil,
	)
}
func (c *Client) Dispatch(ctx context.Context, owner, repo, branch string) error {
	return c.do(
		ctx,
		"POST",
		workflowEndpoint(owner, repo, "/dispatches"),
		map[string]any{"ref": branch, "inputs": map[string]string{"initial_deploy": "true"}},
		nil,
	)
}
func (c *Client) Runs(ctx context.Context, owner, repo, branch string) ([]Run, error) {
	var x struct {
		WorkflowRuns []Run `json:"workflow_runs"`
	}
	p := workflowEndpoint(
		owner,
		repo,
		"/runs",
	) + "?branch=" + url.QueryEscape(
		branch,
	) + "&per_page=50"
	e := c.do(ctx, "GET", p, nil, &x)
	return x.WorkflowRuns, e
}

func (c *Client) Run(ctx context.Context, owner, repo string, runID int64) (Run, error) {
	var run Run
	if runID < 1 {
		return run, fmt.Errorf("invalid workflow run id")
	}
	err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/%s/actions/runs/%d", owner, repo, runID), nil, &run)
	return run, err
}

// Logs downloads a workflow run's archived logs and returns their text without
// retaining it. GitHub responds with a short-lived redirect to the archive.
func (c *Client) Logs(ctx context.Context, owner, repo string, runID int64) (string, bool, error) {
	if runID < 1 {
		return "", false, fmt.Errorf("invalid workflow run id")
	}
	client := *c.HTTP
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.APIURL, "/")+fmt.Sprintf("/repos/%s/%s/actions/runs/%d/logs", owner, repo, runID), nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		return "", false, fmt.Errorf("github %s: %s", resp.Status, string(body))
	}
	location, err := resp.Location()
	if err != nil {
		return "", false, fmt.Errorf("github logs redirect: %w", err)
	}
	downloadReq, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return "", false, err
	}
	downloadClient := *c.HTTP
	download, err := downloadClient.Do(downloadReq)
	if err != nil {
		return "", false, err
	}
	defer download.Body.Close()
	if download.StatusCode < 200 || download.StatusCode >= 300 {
		return "", false, fmt.Errorf("github log archive %s", download.Status)
	}
	const maxArchiveSize = 32 << 20
	archive, err := io.ReadAll(io.LimitReader(download.Body, maxArchiveSize+1))
	if err != nil {
		return "", false, err
	}
	if len(archive) > maxArchiveSize {
		return "", false, fmt.Errorf("github log archive exceeds 32 MiB")
	}
	return unzipLogs(archive)
}

func unzipLogs(archive []byte) (string, bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return "", false, fmt.Errorf("invalid github log archive: %w", err)
	}
	files := append([]*zip.File(nil), zr.File...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	const maxLogSize = 8 << 20
	var out strings.Builder
	truncated := false
	for _, file := range files {
		if file.FileInfo().IsDir() {
			continue
		}
		if out.Len() >= maxLogSize {
			truncated = true
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return "", false, err
		}
		remaining := maxLogSize - out.Len()
		contents, readErr := io.ReadAll(io.LimitReader(reader, int64(remaining)+1))
		closeErr := reader.Close()
		if readErr != nil {
			return "", false, readErr
		}
		if closeErr != nil {
			return "", false, closeErr
		}
		if len(files) > 1 {
			out.WriteString("\n===== " + file.Name + " =====\n")
		}
		if len(contents) > remaining {
			contents = contents[:remaining]
			truncated = true
		}
		out.Write(contents)
	}
	if truncated {
		out.WriteString("\n\n[日志过大，已截断为前 8 MiB]\n")
	}
	return out.String(), truncated, nil
}
func workflowEndpoint(owner, repo, suffix string) string {
	return fmt.Sprintf("/repos/%s/%s/actions/workflows/%s%s", owner, repo, workflowFile, suffix)
}

func Managed(content string) bool {
	return strings.Contains(content, marker) || strings.Contains(content, legacyMarker)
}
func DecodeContent(c Content) (string, error) {
	s := strings.ReplaceAll(c.Content, "\n", "")
	b, e := base64.StdEncoding.DecodeString(s)
	return string(b), e
}

type WorkflowInput struct{ AppID, AppType, RuntimeVersion, Branch, Dockerfile, Context, CallbackURL string }

func typeBuildSteps(appType, runtimeVersion, contextDir string) string {
	switch appType {
	case "vue":
		return fmt.Sprintf(`      - name: 配置 Node.js
        uses: actions/setup-node@v4
        with:
          node-version: %q
      - name: 构建 Vue 应用
        working-directory: %q
        run: |
          npm ci
          npm run build
          printf '\n!dist/\n!dist/**\n' >> .dockerignore
`, runtimeVersion, contextDir)
	case "python":
		return fmt.Sprintf(`      - name: 配置 Python
        uses: actions/setup-python@v5
        with:
          python-version: %q
      - name: 校验 Python 应用
        working-directory: %q
        run: |
          if [ -f requirements.txt ]; then pip install -r requirements.txt; fi
          python -m compileall .
`, runtimeVersion, contextDir)
	case "java":
		return fmt.Sprintf(`      - name: 配置 Java
        uses: actions/setup-java@v4
        with:
          distribution: temurin
          java-version: %q
          cache: maven
      - name: 构建 Java 应用
        working-directory: %q
        run: |
          if [ -x ./mvnw ]; then
            ./mvnw -B -DskipTests package
          else
            mvn -B -DskipTests package
          fi
          printf '\n!target/\n!target/*.jar\n' >> .dockerignore
`, runtimeVersion, contextDir)
	case "go":
		versionConfig := "          go-version: " + fmt.Sprintf("%q", runtimeVersion)
		if runtimeVersion == "go.mod" {
			versionConfig = "          go-version-file: " + fmt.Sprintf(
				"%q",
				strings.TrimSuffix(contextDir, "/")+"/go.mod",
			)
		}
		return fmt.Sprintf(`      - name: 配置 Go
        uses: actions/setup-go@v5
        with:
%s
          cache: false
      - name: 构建 Go 应用
        working-directory: %q
        run: |
          mapfile -t main_packages < <(go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./... | sed '/^$/d')
          if [ "${#main_packages[@]}" -ne 1 ]; then
            echo "::error::Expected exactly one Go main package, found ${#main_packages[@]}: ${main_packages[*]}"
            exit 1
          fi
          echo "Building Go main package: ${main_packages[0]}"
          go build -trimpath -o app "${main_packages[0]}"
          printf '\n!app\n' >> .dockerignore
`, versionConfig, contextDir)
	default:
		return ""
	}
}

func Workflow(i WorkflowInput) string {
	return fmt.Sprintf(`%s
name: Luna PaaS Cloud 应用构建
on:
  push:
    branches: [%q]
  workflow_dispatch:
    inputs:
      initial_deploy:
        type: boolean
        default: false
permissions:
  contents: read
jobs:
  prepare:
    name: 准备构建环境
    if: github.event_name == 'workflow_dispatch' || !contains(github.event.head_commit.message, '[paas-skip]')
    runs-on: ubuntu-latest
    steps:
      - name: 检出源代码
        uses: actions/checkout@v4
      - name: 校验构建配置
        env:
          DOCKERFILE: %s
          BUILD_CONTEXT: %s
        run: |
          test -f "$DOCKERFILE"
          test -d "$BUILD_CONTEXT"
  build:
    name: 构建并推送镜像
    needs: prepare
    runs-on: ubuntu-latest
    outputs:
      image: ${{ steps.image.outputs.value }}
    steps:
      - name: 检出源代码
        uses: actions/checkout@v4
%s      - name: 设置 Docker Buildx
        uses: docker/setup-buildx-action@v3
      - name: 登录容器镜像仓库
        uses: docker/login-action@v3
        with:
          registry: ${{ secrets.PAAS_ACR_REGISTRY }}
          username: ${{ secrets.PAAS_ACR_USERNAME }}
          password: ${{ secrets.PAAS_ACR_PASSWORD }}
      - name: 生成镜像标签
        id: image
        shell: bash
        run: echo "value=${{ secrets.PAAS_IMAGE_REPOSITORY }}:${GITHUB_SHA}-${GITHUB_RUN_ID}" >> "$GITHUB_OUTPUT"
      - name: 构建并推送镜像
        uses: docker/build-push-action@v6
        with:
          context: %s
          file: %s
          push: true
          provenance: false
          sbom: false
          tags: ${{ steps.image.outputs.value }}
  notify:
    name: 同步构建结果
    needs: [prepare, build]
    if: always()
    runs-on: ubuntu-latest
    steps:
      - name: 检出源代码
        uses: actions/checkout@v4
      - name: 通知 Luna PaaS Cloud
        env:
          CALLBACK_TOKEN: ${{ secrets.PAAS_CALLBACK_TOKEN }}
          JOB_STATUS: ${{ needs.prepare.result == 'failure' && 'failure' || needs.prepare.result == 'cancelled' && 'cancelled' || needs.build.result }}
          IMAGE: ${{ needs.build.outputs.image }}
          INITIAL: ${{ inputs.initial_deploy || 'false' }}
        shell: bash
        run: |
          title=$(git log -1 --format=%%s)
          payload=$(jq -n --arg repo "${GITHUB_REPOSITORY}" --arg ref "${GITHUB_REF_NAME}" --arg sha "${GITHUB_SHA}" --arg title "$title" --arg image "$IMAGE" --arg status "$JOB_STATUS" --arg initial "$INITIAL" --argjson run_id "$GITHUB_RUN_ID" --argjson attempt "$GITHUB_RUN_ATTEMPT" '{repository:$repo,ref:$ref,commit_sha:$sha,title:$title,image:$image,status:$status,initial:($initial == "true"),run_id:$run_id,run_attempt:$attempt,html_url:("https://github.com/"+$repo+"/actions/runs/"+($run_id|tostring))}')
          for delay in 0 2 5 10; do sleep "$delay"; curl --fail-with-body -sS -X POST -H "Authorization: Bearer $CALLBACK_TOKEN" -H "Content-Type: application/json" --data "$payload" %s && exit 0; done
          exit 1
`, marker, i.Branch, i.Dockerfile, i.Context, typeBuildSteps(i.AppType, i.RuntimeVersion, i.Context), i.Context, i.Dockerfile, i.CallbackURL)
}
