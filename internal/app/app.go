// Package app is the Knack window. It reads skill directories on
// disk, and only changes them after a confirmation that can be undone.
package app

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"knack/internal/manage"
	"knack/internal/repair"
	"knack/internal/skill"
	"knack/internal/store"
)

// App is the window's state. New does not scan; Attach starts the first scan
// once the window exists, so a scan result is never applied before the
// window can show it.
type App struct {
	home        string
	configPath  string
	undoRoot    string
	catalogPath string
	cached      bool

	settingsPath  string
	foldLongSteps bool
	hidden        map[string]bool // agent root IDs left out of the sidebar
	settingsOpen  bool
	settingsTab   int

	repos      []store.Repo
	snap       *skill.Snapshot
	repoDialog *repoDialogState

	invocations   map[string]manage.Invocation
	invocationErr string
	sessions      sessionState
	activity      *sessionActivity
	skillPeriod   int

	cache       viewCache
	documents   map[string]*documentState
	comparisons map[string]*comparisonState
	graphs      map[string]*graphState

	scanning      bool
	mutating      bool
	completed     *actionResult
	rescanPending bool
	scanGen       int
	loadErr       string
	notice        string
	query         string

	eff           effectiveState
	page          string
	filter        int
	agentsOpen    bool
	pages         map[string]*pageState
	sideW, listW  float32
	graphW        float32
	sideCollapsed bool
	// rail is set each frame when the window is too narrow for the
	// sidebar; railPeek shows it anyway until the page changes.
	rail, railPeek bool
	railPage       string
	// 技能库: the filter chip and the order of each source's skills.
	libFilter, libSort int
	libCounts          [4]int
	detailTab          int    // the skill detail's tab: SKILL.md, files, deps, sessions
	graphPick          string // the path of the skill the 依赖图 page shows

	win *mygo.Window
	ask ask

	// Replacements for the system, so tests never open a dialog.
	pickDirFn func() (string, error)
	revealFn  func(string)
	copyFn    func(string)
	openFn    func(string)
}

type pageState struct {
	list        ui.ListState
	sel         int
	chosen      ui.Selection[string]
	choiceScope string
	choiceSnap  *skill.Snapshot
	agentTab    int
	view        int    // 0 the list, 1 the dependency graph
	node        string // the node chosen in the graph
	graph       graphCanvasState
}

type actionResult struct {
	done    string
	records []manage.Record
	err     error
}

type ask struct {
	open    bool
	title   string
	message string
	ok      string
	done    string
	run     func() ([]manage.Record, error)
	onUI    bool

	// An optional second way to go ahead, offered beside the default one.
	alt, altDone string
	altRun       func() ([]manage.Record, error)
}

// New loads the repo list for home. A missing config file is an empty list.
// Keep the original data directories so Knack retains existing settings and undo records.
func New(home string) *App {
	home = filepath.Clean(home)
	a := &App{
		home:         home,
		configPath:   filepath.Join(home, ".config", "skill-manager", "repos.json"),
		undoRoot:     filepath.Join(home, ".config", "skill-manager", "undo"),
		catalogPath:  filepath.Join(home, ".cache", "skill-manager", "catalog.json"),
		settingsPath: filepath.Join(home, ".config", "skill-manager", "settings.json"),
		hidden:       map[string]bool{},
		skillPeriod:  1,
		page:         "overview",
		pages:        map[string]*pageState{},
		documents:    map[string]*documentState{},
		comparisons:  map[string]*comparisonState{},
		graphs:       map[string]*graphState{},
		sideW:        224,
		listW:        440,
		graphW:       720,
		agentsOpen:   true,
	}
	repos, err := store.Load(a.configPath)
	if err != nil {
		a.loadErr = err.Error()
	}
	a.repos = repos
	if st, err := store.LoadSettings(a.settingsPath); err != nil {
		a.loadErr = err.Error()
	} else {
		a.foldLongSteps = st.FoldLongSteps
		for _, id := range st.HiddenAgents {
			a.hidden[id] = true
		}
		a.page = a.homePage()
	}
	if snap, err := store.LoadCatalog(a.catalogPath, a.home, a.repoPaths()); err == nil && snap != nil {
		a.snap = snap
		a.cached = true
		a.refreshInvocations()
	}
	a.loadSessions()
	return a
}

// Attach remembers the window and starts a scan. Call it after NewWindow.
func (a *App) Attach(win *mygo.Window) {
	a.win = win
	a.startScan()
}

// frosted reports that the sidebar should leave its background undrawn so
// the window's sidebar material shows. A desktop that asks for less
// transparency gets a solid sidebar instead. Headless tests have no
// window; vibrancy there is only what the tester turned on.
func (a *App) frosted(c *ui.Context) bool {
	if c == nil || !c.Vibrancy() {
		return false
	}
	if a.win == nil {
		return true
	}
	return !mygo.Theme.Preferences().ReduceTransparency
}

func (a *App) startScan() {
	if a.scanning || a.mutating {
		a.rescanPending = true
		return
	}
	a.rescanPending = false
	a.startSessionScan()
	a.scanGen++
	gen := a.scanGen
	a.scanning = true
	home := a.home
	repos := a.repoPaths()
	run := func() {
		snap := skill.Scan(skill.Discover(home), repos)
		invocations, invocationErr := manage.ReadInvocations(home)
		cacheErr := store.SaveCatalog(a.catalogPath, home, repos, snap)
		a.post(func() {
			if gen != a.scanGen {
				a.scanning = false
				if a.rescanPending {
					a.startScan()
				}
				return
			}
			a.snap = snap
			a.documents = map[string]*documentState{}
			a.comparisons = map[string]*comparisonState{}
			a.graphs = map[string]*graphState{}
			a.cached = false
			if cacheErr != nil {
				a.notice = "无法保存启动缓存：" + cacheErr.Error()
			}
			a.invocations = invocations
			a.invocationErr = ""
			if invocationErr != nil {
				a.invocationErr = invocationErr.Error()
			}
			a.scanning = false
			if a.rescanPending {
				a.startScan()
			}
		})
	}
	if a.win == nil {
		run()
		return
	}
	go run()
}

func (a *App) post(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	fn()
}

func (a *App) repoPaths() []string {
	out := make([]string, len(a.repos))
	for i, r := range a.repos {
		out[i] = r.Path
	}
	return out
}

func (a *App) state(id string) *pageState {
	ps := a.pages[id]
	if ps == nil {
		ps = &pageState{}
		a.pages[id] = ps
	}
	ps.list.Selected = &ps.sel
	return ps
}

func (a *App) chooseDir() (string, error) {
	return a.chooseDirWith("添加仓库", "选择一个仓库或目录，扫描其中的 SKILL.md")
}

func (a *App) chooseDirWith(title, message string) (string, error) {
	if a.pickDirFn != nil {
		return a.pickDirFn()
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:    a.win,
		Title:     title,
		Directory: true,
		Message:   message,
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

func (a *App) beginAddRepo() {
	run := func() {
		path, err := a.chooseDir()
		a.post(func() { a.acceptRepo(path, err) })
	}
	if a.win == nil {
		run()
		return
	}
	go run()
}

func (a *App) acceptRepo(path string, err error) {
	if err != nil {
		a.notice = err.Error()
		return
	}
	if strings.TrimSpace(path) == "" {
		return
	}
	path = filepath.Clean(path)
	if a.isExistingRoot(path) {
		a.notice = "这个目录已经在扫描范围内"
		return
	}
	for _, r := range a.repos {
		if filepath.Clean(r.Path) == path {
			a.notice = "这个仓库已经添加过"
			return
		}
	}
	a.repos = append(a.repos, store.Repo{Path: path, Added: time.Now()})
	if err := store.Save(a.configPath, a.repos); err != nil {
		a.repos = a.repos[:len(a.repos)-1]
		a.notice = err.Error()
		return
	}
	a.page = "repo:" + path
	a.notice = "已添加仓库，正在扫描"
	a.refreshRepoScan()
}

func (a *App) isExistingRoot(path string) bool {
	path = filepath.Clean(path)
	for _, r := range skill.Discover(a.home) {
		if filepath.Clean(r.Path) == path {
			return true
		}
	}
	return false
}

func (a *App) removeRepo(path string) error {
	path = filepath.Clean(path)
	kept := make([]store.Repo, 0, len(a.repos))
	for _, r := range a.repos {
		if filepath.Clean(r.Path) != path {
			kept = append(kept, r)
		}
	}
	if err := store.Save(a.configPath, kept); err != nil {
		return err
	}
	a.repos = kept
	if a.page == "repo:"+path {
		a.page = a.homePage()
	}
	a.notice = "已不再扫描这个仓库"
	delete(a.pages, "repo:"+path)
	a.refreshRepoScan()
	return nil
}

func (a *App) poolPath() (string, bool) {
	return repair.PoolRoot(a.snap, a.home)
}

func (a *App) beginMutation() {
	a.mutating = true
	if a.scanning {
		a.scanGen++
		a.rescanPending = true
	}
}

func (a *App) undo(recs []manage.Record) {
	if a.mutating {
		return
	}
	a.beginMutation()
	run := func() {
		var first error
		for i := len(recs) - 1; i >= 0; i-- {
			if err := manage.Restore(recs[i]); err != nil && first == nil {
				first = err
			}
		}
		a.post(func() {
			a.mutating = false
			a.notice = "已撤销"
			if first != nil {
				a.notice = first.Error()
			}
			a.refreshAfter(recs)
		})
	}
	if a.win == nil {
		run()
	} else {
		go run()
	}
}

// Invocation preferences do not change the skill tree or its dependencies.
func (a *App) refreshAfter(recs []manage.Record) {
	if len(recs) == 0 {
		return
	}
	for _, rec := range recs {
		if rec.Invocation == nil {
			a.startScan()
			return
		}
	}
	a.refreshInvocations()
	if a.rescanPending {
		a.startScan()
	}
}

func (a *App) refreshInvocations() {
	invocations, err := manage.ReadInvocations(a.home)
	a.invocations = invocations
	a.invocationErr = ""
	if err != nil {
		a.invocationErr = err.Error()
	}
}

func (a *App) reveal(path string) {
	if a.revealFn != nil {
		a.revealFn(path)
		return
	}
	mygo.Shell.ShowItemInFolder(path)
}

// openPath opens a file in the app the system gives it.
func (a *App) openPath(path string) {
	if a.openFn != nil {
		a.openFn(path)
		return
	}
	if err := mygo.Shell.OpenPath(path); err != nil {
		a.notice = "无法打开：" + err.Error()
	}
}

func (a *App) copyPath(path string) {
	if a.copyFn != nil {
		a.copyFn(path)
		return
	}
	mygo.Clipboard.WriteText(path)
}

func (a *App) rootByPage(page string) (skill.Root, bool) {
	if a.snap == nil {
		return skill.Root{}, false
	}
	want := page
	if id, ok := strings.CutPrefix(page, "root:"); ok {
		want = id
	}
	if page == "pool" {
		want = "pool"
	}
	for _, r := range a.snap.Roots {
		if r.ID == want {
			return r, true
		}
	}
	return skill.Root{}, false
}

func (a *App) repoLabel(path string) string {
	base := filepath.Base(path)
	n := 0
	for _, r := range a.repos {
		if r.Path == path && strings.TrimSpace(r.Name) != "" {
			return r.Name
		}
		if filepath.Base(r.Path) == base {
			n++
		}
	}
	if n > 1 {
		parent := filepath.Base(filepath.Dir(path))
		if parent != "" && parent != "." {
			return parent + "/" + base
		}
	}
	return base
}

func shortPath(home, path string) string {
	path = filepath.Clean(path)
	home = filepath.Clean(home)
	if path == home {
		return "~"
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return "~/" + filepath.ToSlash(rel)
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	rs := []rune(s)
	if len(rs) > 140 {
		return string(rs[:140]) + "…"
	}
	return s
}

func matchQuery(q string, parts ...string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	for _, p := range parts {
		if strings.Contains(strings.ToLower(p), q) {
			return true
		}
	}
	return false
}
