package session

import (
	"slices"
	"strings"
	"sync"
	"time"
)

// AgentInfo describes an Agent and what its local logs can tell. The UI shows
// "暂不支持" from these declarations instead of branching on Agent names.
type AgentInfo struct {
	ID   Agent
	Name string
	// Unsupported explains why the Agent has no readable logs; empty otherwise.
	Unsupported    string
	Skills         bool // Skill tool calls are recorded
	OutputRate     bool // request generation time and output tokens are reliable
	PermissionWait bool // explicit user confirmation of tool calls is recorded
	ToolOutcome    bool // tool timeouts and interruptions are recorded
	// Notes qualify missing or estimated data. Empty notes get generic text.
	SkillNote, UsageNote, RateNote, TimingNote string
}

// A format reads one Agent's local logs: where they are, how records map to
// the normalized Session model, and what replay content they carry. Formats
// are registered in formats; adding an Agent needs no change elsewhere.
type format interface {
	Info() AgentInfo
	// version names the parsing rules. Cached statistics of another version
	// are rebuilt, so changing a parser must change its version.
	version() string
	layout() layout
	// visit reports whether a path below a root is followed (directory) or
	// read (file). For a file it also returns the explicit parent session and
	// child Agent ID when the layout records them.
	visit(parts []string, dir bool) (parent, child string, ok bool)
	// parentLog returns the log path of a child's explicitly linked parent
	// session, or "" when the format cannot link it to a parent turn.
	parentLog(child logFile) string
	// record returns a record's time and whether it is one of the format's.
	record(o object) (time.Time, bool)
	// recordID identifies a record so duplicated history is counted once.
	recordID(o object) string
	parse(p *parser, o object, at time.Time)
	replay(tr *transcriptReader, o object, at time.Time)
}

type layout struct {
	roots    []string // below the home directory, slash-separated
	suffixes []string
}

// typedRecords are the built-in formats' records: a "type" and an RFC 3339
// "timestamp" field.
type typedRecords struct{}

func (typedRecords) record(o object) (time.Time, bool) { return stamp(o), str(o, "type") != "" }

// flatLayout reads every log below the roots, up to the discovery depth.
type flatLayout struct{}

func (flatLayout) visit([]string, bool) (string, string, bool) { return "", "", true }
func (flatLayout) parentLog(logFile) string                    { return "" }

var builtinFormats = []format{embeddedFormat("claude"), embeddedFormat("codex"), factoryFormat{}}

// registry adds the user-defined Agents of agents.json and of the plugins
// directory after the built-in ones. It is set once at startup, before any
// Reader runs, and read concurrently.
var registry struct {
	sync.RWMutex
	custom, plugins          []format
	warnings, pluginWarnings []string
	pluginStatus             []PluginStatus
}

func formats() []format {
	registry.RLock()
	defer registry.RUnlock()
	return slices.Concat(builtinFormats, registry.custom, registry.plugins)
}

// Agents without readable local logs are listed so the UI can say so.
var unsupportedAgents = []AgentInfo{{ID: Cursor, Name: "Cursor", Unsupported: "没有本地会话日志"}}

// Agents returns every known Agent in display order.
func Agents() []AgentInfo {
	fs := formats()
	out := make([]AgentInfo, 0, len(fs)+len(unsupportedAgents))
	for _, f := range fs {
		out = append(out, f.Info())
	}
	return append(out, unsupportedAgents...)
}

// LookupAgent returns the Agent's description. Unknown Agents get their ID as
// name and are reported as unsupported.
func LookupAgent(id Agent) AgentInfo {
	for _, info := range Agents() {
		if info.ID == id {
			return info
		}
	}
	return AgentInfo{ID: id, Name: string(id), Unsupported: "没有已验证的本地会话日志格式"}
}

// AgentByName matches a display name or ID, ignoring case.
func AgentByName(name string) (AgentInfo, bool) {
	for _, info := range Agents() {
		if strings.EqualFold(info.Name, name) || strings.EqualFold(string(info.ID), name) {
			return info, true
		}
	}
	return AgentInfo{}, false
}

func formatOf(id Agent) format {
	for _, f := range formats() {
		if f.Info().ID == id {
			return f
		}
	}
	return nil
}
