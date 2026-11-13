package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const appVersion = "0.1.0"

type Docker struct {
	client *http.Client
	base   string
}
type Container struct {
	ID      string   `json:"Id"`
	Names   []string `json:"Names"`
	Image   string   `json:"Image"`
	ImageID string   `json:"ImageID"`
	Command string   `json:"Command"`
	Created int64    `json:"Created"`
	State   string   `json:"State"`
	Status  string   `json:"Status"`
	Ports   []struct {
		IP          string `json:"IP"`
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
	Labels map[string]string `json:"Labels"`
}
type Image struct {
	ID          string   `json:"Id"`
	RepoTags    []string `json:"RepoTags"`
	RepoDigests []string `json:"RepoDigests"`
	Created     string   `json:"Created"`
	Size        int64    `json:"Size"`
	Config      struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}
type ContainerView struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Image        string   `json:"image"`
	ImageID      string   `json:"imageId"`
	State        string   `json:"state"`
	Status       string   `json:"status"`
	Created      int64    `json:"created"`
	Ports        []string `json:"ports"`
	Project      string   `json:"project,omitempty"`
	Service      string   `json:"service,omitempty"`
	Version      string   `json:"version,omitempty"`
	Revision     string   `json:"revision,omitempty"`
	ImageCreated string   `json:"imageCreated,omitempty"`
	ImageDigest  string   `json:"imageDigest,omitempty"`
	CanUpdate    bool     `json:"canUpdate"`
	UpdateReason string   `json:"updateReason,omitempty"`
}
type apiResponse map[string]any

func main() {
	user, password := os.Getenv("PANEL_USER"), os.Getenv("PANEL_PASSWORD")
	if user == "" || password == "" {
		log.Fatal("PANEL_USER and PANEL_PASSWORD are required")
	}
	if len(password) < 16 {
		log.Fatal("PANEL_PASSWORD must be at least 16 characters")
	}
	addr := env("LISTEN_ADDR", "127.0.0.1:8744")
	d, err := newDocker()
	if err != nil {
		log.Fatal(err)
	}
	app := &Server{docker: d, user: user, password: password}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, apiResponse{"status": "ok", "version": appVersion})
	})
	mux.Handle("/api/", app.auth(http.HandlerFunc(app.api)))
	mux.Handle("/", http.FileServer(http.Dir(env("WEB_DIR", "/opt/container-watch/web"))))
	srv := &http.Server{Addr: addr, Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("container-watch %s listening on %s", appVersion, addr)
	log.Fatal(srv.ListenAndServe())
}

type Server struct {
	docker         *Docker
	user, password string
	updates        sync.Map
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func newDocker() (*Docker, error) {
	endpoint := os.Getenv("DOCKER_HOST")
	if endpoint == "" {
		endpoint = "unix:///var/run/docker.sock"
	}
	if strings.HasPrefix(endpoint, "unix://") {
		p := strings.TrimPrefix(endpoint, "unix://")
		tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", p)
		}}
		return &Docker{client: &http.Client{Transport: tr, Timeout: 20 * time.Second}, base: "http://docker"}, nil
	}
	if strings.HasPrefix(endpoint, "tcp://") {
		endpoint = "http://" + strings.TrimPrefix(endpoint, "tcp://")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported DOCKER_HOST scheme %q", u.Scheme)
	}
	return &Docker{client: &http.Client{Timeout: 20 * time.Second}, base: strings.TrimRight(endpoint, "/")}, nil
}
func (d *Docker) request(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, d.base+"/v1.41"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "fnos-container-watch/"+appVersion)
	return d.client.Do(req)
}
func (d *Docker) containers(ctx context.Context) ([]Container, error) {
	res, err := d.request(ctx, "GET", "/containers/json?all=true", nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return nil, fmt.Errorf("Docker API %s: %s", res.Status, strings.TrimSpace(string(b)))
	}
	var out []Container
	err = json.NewDecoder(res.Body).Decode(&out)
	return out, err
}
func (d *Docker) image(ctx context.Context, id string) (Image, error) {
	res, err := d.request(ctx, "GET", "/images/"+url.PathEscape(id)+"/json", nil)
	if err != nil {
		return Image{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return Image{}, fmt.Errorf("image inspect returned %s", res.Status)
	}
	var out Image
	err = json.NewDecoder(res.Body).Decode(&out)
	return out, err
}
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(s.user)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(s.password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Container Watch", charset="UTF-8"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Header.Get("X-Requested-With") != "ContainerWatch" {
			writeError(w, http.StatusForbidden, errors.New("missing request token"))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			if e != nil || u.Host != r.Host {
				writeError(w, http.StatusForbidden, errors.New("cross-origin request rejected"))
				return
			}
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	switch {
	case r.URL.Path == "/api/summary" && r.Method == "GET":
		cs, err := s.docker.containers(ctx)
		if err != nil {
			writeError(w, 502, err)
			return
		}
		running := 0
		for _, c := range cs {
			if c.State == "running" {
				running++
			}
		}
		writeJSON(w, 200, apiResponse{"containers": len(cs), "running": running, "stopped": len(cs) - running, "serverTime": time.Now().UTC().Format(time.RFC3339), "version": appVersion})
	case r.URL.Path == "/api/containers" && r.Method == "GET":
		views, err := s.list(ctx)
		if err != nil {
			writeError(w, 502, err)
			return
		}
		writeJSON(w, 200, views)
	case r.URL.Path == "/api/updates/check" && r.Method == "POST":
		views, err := s.list(ctx)
		if err != nil {
			writeError(w, 502, err)
			return
		}
		unique := make(map[string]string)
		for _, v := range views {
			if _, ok := unique[v.Image]; !ok {
				unique[v.Image] = v.ImageDigest
			}
		}
		type imageCheck struct {
			image  string
			result UpdateResult
		}
		results := make(chan imageCheck, len(unique))
		sem := make(chan struct{}, 6)
		var wg sync.WaitGroup
		for image, digest := range unique {
			wg.Add(1)
			go func(image, digest string) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					results <- imageCheck{image: image, result: UpdateResult{Reason: "检查超时"}}
					return
				}
				defer func() { <-sem }()
				results <- imageCheck{image: image, result: checkRemote(ctx, s.docker, image, digest)}
			}(image, digest)
		}
		wg.Wait()
		close(results)
		checked := make(map[string]UpdateResult, len(unique))
		for item := range results {
			checked[item.image] = item.result
			s.updates.Store(item.image, item.result)
		}
		for i := range views {
			result := checked[views[i].Image]
			views[i].CanUpdate = result.Available
			views[i].UpdateReason = result.Reason
			if result.Available {
				views[i].UpdateReason = "有新镜像版本可用"
			}
		}
		writeJSON(w, 200, apiResponse{"containers": views, "checkedAt": time.Now().UTC().Format(time.RFC3339)})
	case strings.HasPrefix(r.URL.Path, "/api/containers/") && strings.HasSuffix(r.URL.Path, "/update") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/containers/"), "/update")
		id = strings.Trim(id, "/")
		if len(id) < 12 {
			writeError(w, 400, errors.New("invalid container id"))
			return
		}
		if err := s.updateCompose(ctx, id); err != nil {
			writeError(w, 400, err)
			return
		}
		writeJSON(w, 200, apiResponse{"status": "updated", "message": "Compose 服务更新命令已完成；请刷新确认容器状态"})
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) list(ctx context.Context) ([]ContainerView, error) {
	cs, err := s.docker.containers(ctx)
	if err != nil {
		return nil, err
	}
	cache := map[string]Image{}
	has := map[string]bool{}
	out := make([]ContainerView, 0, len(cs))
	for _, c := range cs {
		img, ok := cache[c.ImageID]
		if !ok {
			img, err = s.docker.image(ctx, c.ImageID)
			if err == nil {
				cache[c.ImageID] = img
				has[c.ImageID] = true
			}
		}
		name := strings.TrimPrefix(strings.Join(c.Names, ","), "/")
		v := ContainerView{ID: c.ID, Name: name, Image: c.Image, ImageID: c.ImageID, State: c.State, Status: c.Status, Created: c.Created, Project: c.Labels["com.docker.compose.project"], Service: c.Labels["com.docker.compose.service"], Version: imageTag(c.Image)}
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				v.Ports = append(v.Ports, fmt.Sprintf("%s:%d→%d/%s", p.IP, p.PublicPort, p.PrivatePort, p.Type))
			}
		}
		if has[c.ImageID] {
			if version := strings.TrimSpace(img.Config.Labels["org.opencontainers.image.version"]); version != "" {
				v.Version = version
			} else if version := strings.TrimSpace(img.Config.Labels["version"]); version != "" {
				v.Version = version
			}
			v.Revision = img.Config.Labels["org.opencontainers.image.revision"]
			v.ImageCreated = img.Created
			if len(img.RepoDigests) > 0 {
				v.ImageDigest = img.RepoDigests[0]
			}
		}
		if v.Project != "" && v.Service != "" && c.Labels["com.docker.compose.project.working_dir"] != "" && c.Labels["com.docker.compose.project.config_files"] != "" {
			v.UpdateReason = "Compose 管理：可手动更新"
		} else {
			v.UpdateReason = "非 Compose 容器：支持检查版本，不提供自动重建"
		}
		if cached, ok := s.updates.Load(v.Image); ok {
			result := cached.(UpdateResult)
			v.CanUpdate = result.Available
			if result.Available {
				v.UpdateReason = "有新镜像版本可用"
			} else {
				v.UpdateReason = result.Reason
			}
		}
		out = append(out, v)
	}
	return out, nil
}
func imageTag(image string) string {
	if at := strings.LastIndex(image, "@"); at >= 0 {
		return image[at+1:]
	}
	last := image[strings.LastIndex(image, "/")+1:]
	if i := strings.LastIndex(last, ":"); i >= 0 {
		return last[i+1:]
	}
	return "latest"
}
func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return "—"
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, apiResponse{"error": err.Error()})
}

// Compose-only updates avoid guessing how an arbitrary container should be recreated.
func (s *Server) updateCompose(ctx context.Context, id string) error {
	res, err := s.docker.request(ctx, "GET", "/containers/"+url.PathEscape(id)+"/json", nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("container inspect returned %s", res.Status)
	}
	var inspect struct {
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err = json.NewDecoder(res.Body).Decode(&inspect); err != nil {
		return err
	}
	l := inspect.Config.Labels
	image := inspect.Config.Image
	cached, ok := s.updates.Load(image)
	if !ok || !cached.(UpdateResult).Available {
		return errors.New("请先检查镜像更新；仅在确认发现新版本后才允许手动更新")
	}
	project, service, dir, files := l["com.docker.compose.project"], l["com.docker.compose.service"], l["com.docker.compose.project.working_dir"], l["com.docker.compose.project.config_files"]
	if project == "" || service == "" || dir == "" || files == "" {
		return errors.New("该容器不是可识别的 Compose 服务；为避免破坏配置，未执行更新")
	}
	if !composeNameRE.MatchString(project) || !composeNameRE.MatchString(service) {
		return errors.New("Compose 项目或服务标签格式异常，拒绝执行更新")
	}
	if !filepath.IsAbs(dir) {
		return errors.New("Compose 工作目录不是绝对路径")
	}
	if st, e := os.Stat(dir); e != nil || !st.IsDir() {
		return errors.New("Compose 工作目录在本机上不可访问")
	}
	args := []string{"compose", "-p", project, "--project-directory", dir}
	for _, f := range strings.Split(files, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !filepath.IsAbs(f) {
			f = filepath.Join(dir, f)
		}
		if _, e := os.Stat(f); e != nil {
			return fmt.Errorf("Compose 配置文件不可访问: %s", f)
		}
		args = append(args, "-f", f)
	}
	if len(args) < 6 {
		return errors.New("Compose 配置文件标签缺失")
	}
	if err = runCommand(ctx, dir, append(append([]string{}, args...), "pull", service)...); err != nil {
		return fmt.Errorf("拉取镜像失败：%w", err)
	}
	if err = runCommand(ctx, dir, append(append([]string{}, args...), "up", "-d", "--no-deps", service)...); err != nil {
		return fmt.Errorf("拉取完成但重建服务失败：%w", err)
	}
	return nil
}
func runCommand(ctx context.Context, dir string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, env("HOST_DOCKER_BIN", "docker"), args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Registry v2 check, supporting anonymous Bearer challenges (Docker Hub and GHCR public images).
type UpdateResult struct {
	Checked      bool
	Available    bool
	RemoteDigest string
	Reason       string
}

var bearerRE = regexp.MustCompile(`(?i)Bearer\s+realm="([^"]+)"(?:,\s*service="([^"]*)")?(?:,\s*scope="([^"]*)")?`)
var composeNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func checkRemote(ctx context.Context, d *Docker, image, localDigest string) UpdateResult {
	ref, ok := parseReference(image)
	if !ok {
		return UpdateResult{Reason: "无法解析镜像名"}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	endpoint := "https://" + ref.registry + "/v2/" + ref.repo + "/manifests/" + url.PathEscape(ref.tag)
	accept := "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"
	get := func(token string) (*http.Response, error) {
		req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if e != nil {
			return nil, e
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", "fnos-container-watch/"+appVersion)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return client.Do(req)
	}
	res, err := get("")
	if err != nil {
		return UpdateResult{Reason: "检查失败：" + err.Error()}
	}
	if res.StatusCode == http.StatusUnauthorized {
		challenge := res.Header.Get("WWW-Authenticate")
		res.Body.Close()
		m := bearerRE.FindStringSubmatch(challenge)
		if len(m) < 2 {
			return UpdateResult{Reason: "镜像仓库需要认证或不支持匿名检查"}
		}
		u, e := url.Parse(m[1])
		if e != nil {
			return UpdateResult{Reason: "仓库认证地址无效"}
		}
		q := u.Query()
		if m[2] != "" {
			q.Set("service", m[2])
		}
		scope := first(m[3], "repository:"+ref.repo+":pull")
		q.Set("scope", scope)
		u.RawQuery = q.Encode()
		tokenReq, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if e != nil {
			return UpdateResult{Reason: e.Error()}
		}
		tokenRes, e := client.Do(tokenReq)
		if e != nil {
			return UpdateResult{Reason: "获取匿名仓库令牌失败"}
		}
		defer tokenRes.Body.Close()
		if tokenRes.StatusCode != 200 {
			return UpdateResult{Reason: "私有镜像暂不支持更新检测"}
		}
		var tok struct {
			Token       string `json:"token"`
			AccessToken string `json:"access_token"`
		}
		if json.NewDecoder(tokenRes.Body).Decode(&tok) != nil {
			return UpdateResult{Reason: "仓库令牌解析失败"}
		}
		token := first(tok.Token, tok.AccessToken)
		res, err = get(token)
		if err != nil {
			return UpdateResult{Reason: "检查失败：" + err.Error()}
		}
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return UpdateResult{Checked: true, Reason: "远端标签不存在"}
	}
	if res.StatusCode != 200 {
		return UpdateResult{Reason: fmt.Sprintf("仓库返回 HTTP %d", res.StatusCode)}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	remote := res.Header.Get("Docker-Content-Digest")
	if remote == "" {
		return UpdateResult{Checked: true, Reason: "仓库未提供摘要"}
	}
	local := localDigest
	if i := strings.LastIndex(local, "@"); i >= 0 {
		local = local[i+1:]
	}
	return UpdateResult{Checked: true, Available: local != "" && remote != local, RemoteDigest: remote, Reason: func() string {
		if local == "" {
			return "本地镜像无仓库摘要，无法精确比较"
		}
		if remote == local {
			return "镜像已是最新"
		}
		return "发现新镜像"
	}()}
}

type Reference struct{ registry, repo, tag string }

func parseReference(image string) (Reference, bool) {
	image = strings.TrimSpace(image)
	if image == "" || strings.Contains(image, "@sha256:") {
		return Reference{}, false
	}
	parts := strings.Split(image, "/")
	registry := "registry-1.docker.io"
	repoParts := parts
	if len(parts) > 1 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		registry = parts[0]
		repoParts = parts[1:]
	}
	if registry == "docker.io" {
		registry = "registry-1.docker.io"
	}
	if len(repoParts) == 0 {
		return Reference{}, false
	}
	last := repoParts[len(repoParts)-1]
	tag := "latest"
	if i := strings.LastIndex(last, ":"); i >= 0 {
		tag = last[i+1:]
		repoParts[len(repoParts)-1] = last[:i]
	}
	repo := strings.Join(repoParts, "/")
	if registry == "registry-1.docker.io" && !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	return Reference{registry: registry, repo: repo, tag: tag}, true
}
