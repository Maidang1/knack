package session

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Plugins install from GitHub (a repository's latest release, or the latest
// commit when it has none), from a folder or a .js file on this machine, or
// from a zip of either. plugin.json sits at the root of a package, or in the
// one folder a GitHub archive wraps it in. A loose .js file is a package of
// one script.

const originFile = ".origin.json"

const (
	maxPluginArchive  = 1 << 30
	maxPluginUnpacked = 2 << 30
	maxPluginFiles    = 50000
)

// PluginStatus is one installed plugin, loaded or not.
type PluginStatus struct {
	Name, ID, Title string
	Repo, Version   string
	Problem         string
}

// PluginStatuses is what the last LoadPlugins found, in directory order.
func PluginStatuses() []PluginStatus {
	registry.RLock()
	defer registry.RUnlock()
	out := make([]PluginStatus, len(registry.pluginStatus))
	copy(out, registry.pluginStatus)
	return out
}

func pluginStatusOf(e pluginEntry) PluginStatus {
	status := PluginStatus{Name: e.name}
	if !e.dir {
		return status
	}
	var origin pluginOrigin
	readPluginJSON(filepath.Join(e.path, originFile), &origin)
	status.Repo, status.Version = origin.Repo, origin.Version
	return status
}

// pluginManifest is a package's plugin.json.
type pluginManifest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Main    string `json:"main"`
}

func readPluginManifest(dir string) (pluginManifest, error) {
	var m pluginManifest
	if err := readPluginJSON(filepath.Join(dir, "plugin.json"), &m); err != nil {
		return m, fmt.Errorf("plugin.json：%w", err)
	}
	if m.ID == "" || m.Name == "" {
		return m, fmt.Errorf("plugin.json 需要 id 和 name")
	}
	if !agentID.MatchString(m.ID) {
		return m, fmt.Errorf("id 只能包含小写字母、数字、- 和 _，最长 32 个字符")
	}
	if m.Main == "" {
		m.Main = "index.js"
	}
	return m, nil
}

func readPluginJSON(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// pluginOrigin records where a package was installed from.
type pluginOrigin struct {
	Repo    string `json:"repo"`
	Version string `json:"version"`
}

// PluginPending is a plugin downloaded or copied and checked, waiting to be
// installed. Discard or Install it.
type PluginPending struct {
	Repo, Version string
	ID, Name      string
	// Entry is the file or folder name it will have in the plugins directory.
	Entry string
	root  string
	tmp   string
	file  bool // a loose script, not a directory
}

// ErrNoPluginRepo says there's no such public repository.
var ErrNoPluginRepo = errors.New("GitHub 上没有这个公开仓库")

var (
	pluginGitHubAPI = "https://api.github.com"
	pluginHTTP      = &http.Client{Timeout: 10 * time.Minute}
)

var (
	pluginRepoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

// ParsePluginRepo reads a repository as a person may paste it: owner/repo,
// a github.com page, or a git address.
func ParsePluginRepo(in string) (string, error) {
	s := strings.TrimSpace(in)
	if rest, ok := strings.CutPrefix(strings.ToLower(s), "git@github.com:"); ok {
		s = s[len(s)-len(rest):]
	} else {
		low := strings.ToLower(s)
		for _, prefix := range []string{"https://", "http://"} {
			if strings.HasPrefix(low, prefix) {
				s = s[len(prefix):]
				low = strings.ToLower(s)
				break
			}
		}
		if strings.HasPrefix(low, "www.") {
			s = s[4:]
			low = strings.ToLower(s)
		}
		if strings.HasPrefix(low, "github.com/") {
			rest := s[len("github.com/"):]
			rest, _, _ = strings.Cut(rest, "#")
			rest, _, _ = strings.Cut(rest, "?")
			if parts := strings.SplitN(rest, "/", 3); len(parts) >= 2 {
				rest = parts[0] + "/" + parts[1]
			}
			s = rest
		}
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	if !pluginRepoName.MatchString(s) || strings.Contains(s, "..") {
		return "", fmt.Errorf("不是 GitHub 仓库：%s", strings.TrimSpace(in))
	}
	return s, nil
}

func pluginToken(url string) string {
	if !strings.HasPrefix(url, "https://api.github.com/") {
		return ""
	}
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(name)); t != "" {
			return t
		}
	}
	gh, err := exec.LookPath("gh")
	if err != nil && runtime.GOOS == "darwin" {
		for _, path := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh"} {
			if _, statErr := os.Stat(path); statErr == nil {
				gh = path
				break
			}
		}
	}
	if gh == "" {
		return ""
	}
	out, err := exec.Command(gh, "auth", "token", "--hostname", "github.com").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func pluginGet(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "knack")
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := pluginToken(url); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return pluginHTTP.Do(req)
}

func pluginJSON(path string, out any) error {
	res, err := pluginGet(pluginGitHubAPI + path)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		return json.NewDecoder(res.Body).Decode(out)
	case http.StatusNotFound:
		return ErrNoPluginRepo
	case http.StatusForbidden, http.StatusTooManyRequests:
		return errors.New("GitHub 要求稍后再试")
	default:
		return fmt.Errorf("GitHub 返回 %s", res.Status)
	}
}

type pluginRelease struct {
	repo, version, url string
}

func latestPluginRelease(repo string) (*pluginRelease, error) {
	var rel struct {
		Tag    string `json:"tag_name"`
		Zip    string `json:"zipball_url"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	err := pluginJSON("/repos/"+repo+"/releases/latest", &rel)
	if err == nil && rel.Tag != "" {
		names := make([]string, len(rel.Assets))
		for i, a := range rel.Assets {
			names[i] = a.Name
		}
		url := rel.Zip
		if at := pickPluginAsset(names, runtime.GOOS, runtime.GOARCH); at >= 0 {
			url = rel.Assets[at].URL
		}
		return &pluginRelease{repo: repo, version: rel.Tag, url: url}, nil
	}
	if err != nil && !errors.Is(err, ErrNoPluginRepo) {
		return nil, err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := pluginJSON("/repos/"+repo+"/commits/HEAD", &commit); err != nil {
		return nil, err
	}
	if commit.SHA == "" {
		return nil, ErrNoPluginRepo
	}
	n := min(len(commit.SHA), 12)
	return &pluginRelease{repo: repo, version: commit.SHA[:n], url: pluginGitHubAPI + "/repos/" + repo + "/zipball/" + commit.SHA}, nil
}

// pickPluginAsset chooses the zip named for this platform, or else one named
// for none. A release of zips for other platforms only leaves its source.
func pickPluginAsset(names []string, goos, goarch string) int {
	general := -1
	for i, name := range names {
		n := strings.ToLower(name)
		if !strings.HasSuffix(n, ".zip") {
			continue
		}
		if strings.Contains(n, goos) && strings.Contains(n, goarch) {
			return i
		}
		other := strings.Contains(n, "darwin") || strings.Contains(n, "linux") || strings.Contains(n, "windows")
		if !other && general < 0 {
			general = i
		}
	}
	return general
}

// FetchPlugin downloads what a repository offers now into dir, the plugins
// folder, without installing it, and checks that it loads. Nothing it lists
// is read.
func FetchPlugin(repo, dir, home string) (*PluginPending, error) {
	repo, err := ParsePluginRepo(repo)
	if err != nil {
		return nil, err
	}
	rel, err := latestPluginRelease(repo)
	if err != nil {
		return nil, err
	}
	return pendingPlugin(dir, func(tmp string) (*PluginPending, error) {
		archive := filepath.Join(tmp, "archive.zip")
		if err := downloadPlugin(rel.url, archive); err != nil {
			return nil, err
		}
		unpacked := filepath.Join(tmp, "unpacked")
		if err := unzipPlugin(archive, unpacked); err != nil {
			return nil, fmt.Errorf("解压 %s：%w", rel.repo, err)
		}
		p, err := checkedPlugin(unpacked, tmp, home, rel.repo)
		if err != nil {
			return nil, err
		}
		p.Repo, p.Version = rel.repo, rel.version
		return p, nil
	})
}

// OpenPlugin copies a plugin from this machine — a folder, a .js file or a
// zip — into dir without installing it, and checks it as FetchPlugin does.
func OpenPlugin(path, dir, home string) (*PluginPending, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	return pendingPlugin(dir, func(tmp string) (*PluginPending, error) {
		unpacked := filepath.Join(tmp, "unpacked")
		switch {
		case info.IsDir():
			err = copyPluginTree(path, unpacked)
		case strings.HasSuffix(strings.ToLower(name), ".zip"):
			err = unzipPlugin(path, unpacked)
		case strings.HasSuffix(strings.ToLower(name), ".js"):
			if err = os.MkdirAll(unpacked, 0o755); err != nil {
				return nil, err
			}
			_, err = copyPluginFile(path, filepath.Join(unpacked, name), 0o644, maxPluginUnpacked)
		default:
			err = fmt.Errorf("请选择插件目录、.js 文件或 .zip")
		}
		if err != nil {
			return nil, fmt.Errorf("%s：%w", name, err)
		}
		return checkedPlugin(unpacked, tmp, home, name)
	})
}

func pendingPlugin(dir string, into func(tmp string) (*PluginPending, error)) (*PluginPending, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(dir, ".installing-")
	if err != nil {
		return nil, err
	}
	p, err := into(tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	return p, nil
}

// checkedPlugin finds the plugin in what was unpacked and loads it once.
// home is the home directory the script's paths are relative to.
func checkedPlugin(unpacked, tmp, home, from string) (*PluginPending, error) {
	root, single, err := pluginRoot(unpacked)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", from, err)
	}
	entry := pluginEntry{name: filepath.Base(root), path: root, dir: single == ""}
	if single != "" {
		entry = pluginEntry{name: filepath.Base(single), path: single}
	}
	p, _, id, name, err := loadPluginEntry(entry, home)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", from, err)
	}
	p.close()
	if _, err := checkIdentity(id, name, nil); err != nil {
		return nil, fmt.Errorf("%s：%w", from, err)
	}
	pending := &PluginPending{ID: id, Name: name, root: root, tmp: tmp, Entry: id}
	if single != "" {
		pending.file = true
		pending.root = single
		pending.Entry = filepath.Base(single)
	}
	return pending, nil
}

// pluginRoot finds plugin.json at the top or in the one folder there. A
// single .js file, with no json, is returned as single.
func pluginRoot(dir string) (root, single string, err error) {
	if _, statErr := os.Stat(filepath.Join(dir, "plugin.json")); statErr == nil {
		return dir, "", nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", err
	}
	var folders, scripts []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			folders = append(folders, e.Name())
			continue
		}
		if strings.HasSuffix(e.Name(), ".js") {
			scripts = append(scripts, e.Name())
		}
	}
	if len(folders) == 1 && len(scripts) == 0 {
		nested := filepath.Join(dir, folders[0])
		if _, statErr := os.Stat(filepath.Join(nested, "plugin.json")); statErr == nil {
			return nested, "", nil
		}
	}
	if len(folders) == 0 && len(scripts) == 1 {
		return dir, filepath.Join(dir, scripts[0]), nil
	}
	return "", "", errors.New("找不到 plugin.json，也不是单个 .js 文件")
}

// Discard removes a plugin that will not be installed.
func (p *PluginPending) Discard() { os.RemoveAll(p.tmp) }

// Install puts the plugin in dir. A package already installed from another
// repository keeps its name and its sign-in data. Data for this id is the
// directory itself; replacing the same origin keeps nothing of the old files
// except what the caller stored elsewhere.
func (p *PluginPending) Install(dir string) error {
	defer os.RemoveAll(p.tmp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, p.Entry)
	if !p.file {
		var was pluginOrigin
		readPluginJSON(filepath.Join(target, originFile), &was)
		if was.Repo != "" && was.Repo != p.Repo {
			return fmt.Errorf("已经安装了来自 %s 的插件 %s", was.Repo, p.ID)
		}
		if p.Repo != "" {
			b, err := json.Marshal(pluginOrigin{Repo: p.Repo, Version: p.Version})
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(p.root, originFile), b, 0o644); err != nil {
				return err
			}
		}
	}
	old := filepath.Join(p.tmp, "replaced")
	had := false
	if err := os.Rename(target, old); err == nil {
		had = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(p.root, target); err != nil {
		if had {
			os.Rename(old, target)
		}
		return err
	}
	return nil
}

// RemovePlugin deletes one installed entry, a script or a package directory.
// The name is a single path element.
func RemovePlugin(dir, name string) error {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("不能移除 %q", name)
	}
	target := filepath.Join(dir, name)
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(target, "plugin.json")); err != nil {
			return fmt.Errorf("%s 不是插件", name)
		}
	} else if !strings.HasSuffix(name, ".js") {
		return fmt.Errorf("%s 不是插件", name)
	}
	return os.RemoveAll(target)
}

func downloadPlugin(url, path string) error {
	res, err := pluginGet(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败：%s", res.Status)
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	n, err := io.Copy(out, io.LimitReader(res.Body, maxPluginArchive+1))
	if err != nil {
		return err
	}
	if n > maxPluginArchive {
		return errors.New("插件压缩包太大")
	}
	return out.Close()
}

func unzipPlugin(archive, dir string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var files int
	var total int64
	for _, f := range z.File {
		name := filepath.Clean(f.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(f.Name) {
			return fmt.Errorf("压缩包里的路径不安全：%s", f.Name)
		}
		target := filepath.Join(dir, name)
		if !strings.HasPrefix(target, dir+string(filepath.Separator)) && target != dir {
			return fmt.Errorf("压缩包里的路径不安全：%s", f.Name)
		}
		mode := f.FileInfo().Mode()
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("压缩包里有符号链接：%s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if files++; files > maxPluginFiles {
			return errors.New("文件太多")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		n, err := copyZipFile(f, target, int64(maxPluginUnpacked)-total)
		if err != nil {
			return err
		}
		if total += n; total > maxPluginUnpacked {
			return errors.New("插件太大")
		}
	}
	return nil
}

func copyZipFile(f *zip.File, path string, limit int64) (int64, error) {
	in, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	n, err := io.Copy(out, io.LimitReader(in, limit+1))
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, errors.New("插件太大")
	}
	return n, out.Close()
}

func copyPluginTree(from, dir string) error {
	var files int
	var total int64
	return filepath.WalkDir(from, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if rel != "." && strings.HasPrefix(e.Name(), ".") {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		to := filepath.Join(dir, rel)
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("插件里有符号链接：%s", filepath.ToSlash(rel))
		}
		if e.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		if !e.Type().IsRegular() {
			return nil
		}
		if files++; files > maxPluginFiles {
			return errors.New("文件太多")
		}
		n, err := copyPluginFile(path, to, 0o644, int64(maxPluginUnpacked)-total)
		if err != nil {
			return err
		}
		if total += n; total > maxPluginUnpacked {
			return errors.New("插件太大")
		}
		return nil
	})
}

func copyPluginFile(from, to string, perm os.FileMode, limit int64) (int64, error) {
	in, err := os.Open(from)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return 0, err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	n, err := io.Copy(out, io.LimitReader(in, limit+1))
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, errors.New("插件太大")
	}
	return n, out.Close()
}
