package app

import (
	"cmp"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A tool call reads as what it did — 读取 README.md, 运行 go test — rather
// than as its name and raw arguments; the arguments stay one click away.

// toolDesc is a tool call put in words: what it did, to what, and where.
type toolDesc struct {
	verb        string
	subject     string // the file, command, pattern or skill
	detail      string // where, or the command behind a described step
	subjectCode bool   // subject is a command or pattern, shown monospaced
	detailCode  bool
	todos       []todoItem // the plan a to-do call set
	path        string     // the whole path a file call worked on
	command     string     // the whole command a shell call ran
	look        lookKind   // what a call that only looked did
}

// lookKind sorts calls that only look around — read a file, search, list a
// folder — which the replay folds together.
type lookKind int

const (
	lookNone lookKind = iota
	lookRead
	lookSearch
	lookList
)

type todoItem struct {
	text   string
	status string // done, active, or pending
}

// describeTool reads a call's input by what the tool does, across the
// Agents' names for it. Unknown tools keep their name and telling argument.
func describeTool(name, input string) toolDesc {
	var args map[string]any
	json.Unmarshal([]byte(input), &args)
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := args[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	file := func(verb string, keys ...string) toolDesc {
		p := str(keys...)
		if p == "" {
			return toolDesc{verb: verb, subject: inputPreview(input), subjectCode: true}
		}
		return toolDesc{verb: verb, subject: path.Base(p), detail: shortDir(path.Dir(p)), path: p}
	}
	switch n := strings.ToLower(name); n {
	case "read", "view", "read_file", "readfile":
		d := file("读取", "file_path", "path", "filePath", "target_file")
		d.look = lookRead
		if offset, limit := num(args["offset"]), num(args["limit"]); limit > 0 {
			d.detail = joinDetail(d.detail, fmt.Sprintf("第 %d–%d 行", max(offset, 1), max(offset, 1)+limit-1))
		}
		return d
	case "view_image":
		d := file("查看图片", "path", "file_path")
		d.look = lookRead
		return d
	case "ls", "list_dir", "list_directory", "listdirectory":
		d := file("列出", "directory_path", "path", "dir", "target_directory")
		d.look = lookList
		if !d.subjectCode {
			d.subject += "/"
		}
		return d
	case "glob", "find", "file_search":
		return toolDesc{verb: "查找文件", subject: strOrList(args, "pattern", "patterns", "glob", "query"), subjectCode: true,
			detail: shortDir(str("path", "folder", "directory")), look: lookSearch}
	case "grep", "rg", "search", "search_files", "grep_search":
		d := toolDesc{verb: "搜索", subject: str("pattern", "query", "regex"), subjectCode: true, detail: shortDir(str("path", "folder", "directory")), look: lookSearch}
		if g := str("glob", "glob_pattern", "include", "type"); g != "" {
			d.detail = joinDetail(d.detail, g)
		}
		return d
	case "bash", "shell", "execute", "exec_command", "run_command", "run_terminal_cmd", "local_shell", "run_shell_command":
		cmd := str("command", "cmd")
		if cmd == "" {
			cmd = shellCommand(args["command"])
		}
		if why := str("description", "summary", "justification"); why != "" && cmd != "" {
			return toolDesc{verb: "运行", subject: why, detail: firstLine(cmd), detailCode: true, command: cmd}
		}
		return toolDesc{verb: "运行", subject: firstLine(cmd), subjectCode: true, detail: shortDir(str("workdir", "cwd")), command: cmd}
	case "script", "exec":
		code := str("script", "code", "source")
		if code == "" && args == nil {
			code = input
		}
		d := toolDesc{verb: "运行脚本", subject: firstLine(code), subjectCode: true}
		if calls := scriptTools(code); len(calls) > 0 {
			d.subject, d.subjectCode = "调用 "+strings.Join(calls, "、"), false
		}
		return d
	case "edit", "multiedit", "str_replace", "str_replace_editor", "replace", "edit_file", "search_replace":
		return file("编辑", "file_path", "path", "target_file")
	case "write", "create", "write_file", "create_file", "notebookedit":
		verb := "写入"
		if n == "create" || n == "create_file" {
			verb = "新建"
		}
		d := file(verb, "file_path", "path", "target_file", "notebook_path")
		if content := str("content", "file_text"); content != "" {
			d.detail = joinDetail(d.detail, fmt.Sprintf("%d 行", strings.Count(strings.TrimRight(content, "\n"), "\n")+1))
		}
		return d
	case "apply_patch", "applypatch":
		patch := str("input", "patch")
		if patch == "" && args == nil {
			patch = input
		}
		files := patchFiles(patch)
		switch len(files) {
		case 0:
			return toolDesc{verb: "修改文件"}
		case 1:
			return toolDesc{verb: "修改", subject: path.Base(files[0]), detail: shortDir(path.Dir(files[0])), path: files[0]}
		}
		names := make([]string, len(files))
		for i, f := range files {
			names[i] = path.Base(f)
		}
		return toolDesc{verb: "修改", subject: fmt.Sprintf("%d 个文件", len(files)), detail: strings.Join(names, "、")}
	case "todowrite", "todo_write", "update_plan", "write_todos":
		todos := parseTodos(args)
		d := toolDesc{verb: "更新待办", todos: todos}
		if len(todos) > 0 {
			done := 0
			for _, t := range todos {
				if t.status == "done" {
					done++
				}
			}
			d.subject = fmt.Sprintf("%d 项，完成 %d", len(todos), done)
		}
		return d
	case "skill":
		return toolDesc{verb: "启用技能", subject: str("skill", "name"), detail: firstLine(str("args"))}
	case "task", "agent", "spawn_agent":
		return toolDesc{verb: "派出子 Agent", subject: cmp.Or(str("description", "task_name"), firstLine(str("prompt", "message"))), detail: str("subagent_type")}
	case "webfetch", "fetchurl", "fetch", "web_fetch", "read_url":
		u := str("url")
		return toolDesc{verb: "打开网页", subject: strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")}
	case "websearch", "web_search", "search_web":
		return toolDesc{verb: "搜索网页", subject: str("query", "q")}
	case "toolsearch":
		return toolDesc{verb: "加载工具", subject: strings.TrimPrefix(str("query"), "select:")}
	case "askuserquestion", "askuser", "request_user_input", "request_user_input_async":
		return toolDesc{verb: "向用户提问", subject: firstQuestion(args)}
	}
	return toolDesc{verb: name, subject: inputPreview(input), subjectCode: true}
}

// toolOutcome says in a few words what a call returned: how much it read or
// found. It is empty when the output says it better itself.
func toolOutcome(name, output string) string {
	output = strings.TrimSpace(output)
	n := strings.ToLower(name)
	var list []any
	if json.Unmarshal([]byte(output), &list) == nil {
		switch {
		case len(list) == 0:
			return "没有结果"
		case n == "glob" || n == "find" || n == "file_search":
			return fmt.Sprintf("%d 个文件", len(list))
		case strings.Contains(n, "grep") || strings.Contains(n, "search"):
			return fmt.Sprintf("%d 处匹配", len(list))
		}
		return fmt.Sprintf("%d 项", len(list))
	}
	switch n {
	case "read", "view", "read_file", "readfile":
		if m := readLines.FindStringSubmatch(output); m != nil {
			return m[1] + " 行"
		}
		return fmt.Sprintf("%d 行", strings.Count(output, "\n")+1)
	case "skill":
		return "已启用"
	case "script", "exec":
		// A script returns one {"i": n, "result": …} line per call it made.
		if n := strings.Count("\n"+output, "\n{\"i\":"); n > 0 {
			return fmt.Sprintf("返回 %d 个结果", n)
		}
	case "askuserquestion", "askuser", "request_user_input", "request_user_input_async":
		if _, answer, ok := strings.Cut(output, "[answer] "); ok {
			return "回答：" + firstLine(answer)
		}
	}
	return ""
}

var (
	readLines   = regexp.MustCompile(`\((\d+) lines`)
	scriptCalls = regexp.MustCompile(`tools\.([A-Za-z_][\w.]*)\s*\(`)
	todoLine    = regexp.MustCompile(`^\s*(?:\d+\.\s*)?\[(\w+)\]\s*(.+)$`)
)

// shortDir keeps the last two folders of a path: enough to place a file.
func shortDir(dir string) string {
	if dir == "" || dir == "." || dir == "/" {
		return ""
	}
	parts := strings.Split(strings.Trim(dir, "/"), "/")
	if len(parts) > 2 {
		return "…/" + strings.Join(parts[len(parts)-2:], "/")
	}
	return dir
}

func joinDetail(a, b string) string {
	if a == "" {
		return b
	}
	return a + " · " + b
}

func num(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

func strOrList(args map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := args[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case []any:
			var out []string
			for _, x := range v {
				if s, ok := x.(string); ok {
					out = append(out, s)
				}
			}
			if len(out) > 0 {
				return strings.Join(out, ", ")
			}
		}
	}
	return ""
}

// shellCommand reads a command given as argv, as Codex logs it: a shell's
// -c script is the command itself.
func shellCommand(v any) string {
	list, _ := v.([]any)
	var argv []string
	for _, x := range list {
		if s, ok := x.(string); ok {
			argv = append(argv, s)
		}
	}
	if n := len(argv); n >= 3 && (argv[n-2] == "-lc" || argv[n-2] == "-c") {
		return argv[n-1]
	}
	return strings.Join(argv, " ")
}

// scriptTools lists the tools a script calls, each once, in order.
func scriptTools(code string) []string {
	var out []string
	for _, m := range scriptCalls.FindAllStringSubmatch(code, -1) {
		name := m[1]
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

func patchFiles(patch string) []string {
	var out []string
	for _, l := range strings.Split(patch, "\n") {
		for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
			if strings.HasPrefix(l, prefix) {
				out = append(out, strings.TrimSpace(strings.TrimPrefix(l, prefix)))
			}
		}
	}
	return out
}

// parseTodos reads a plan as Claude (a list of content and status), Codex
// (a list of step and status) or Factory (numbered "[status] text" lines)
// log it.
func parseTodos(args map[string]any) []todoItem {
	status := func(s string) string {
		switch strings.ToLower(s) {
		case "completed", "complete", "done":
			return "done"
		case "in_progress", "in-progress", "active", "running":
			return "active"
		}
		return "pending"
	}
	var out []todoItem
	for _, key := range []string{"todos", "plan"} {
		switch v := args[key].(type) {
		case []any:
			for _, x := range v {
				m, _ := x.(map[string]any)
				text, _ := m["content"].(string)
				if text == "" {
					text, _ = m["step"].(string)
				}
				s, _ := m["status"].(string)
				if text != "" {
					out = append(out, todoItem{text: text, status: status(s)})
				}
			}
		case string:
			for _, l := range strings.Split(v, "\n") {
				if m := todoLine.FindStringSubmatch(l); m != nil {
					out = append(out, todoItem{text: strings.TrimSpace(m[2]), status: status(m[1])})
				}
			}
		}
	}
	return out
}

func firstQuestion(args map[string]any) string {
	if qs, ok := args["questions"].([]any); ok && len(qs) > 0 {
		if q, ok := qs[0].(map[string]any); ok {
			for _, k := range []string{"question", "title"} {
				if s, ok := q[k].(string); ok {
					return s
				}
			}
		}
	}
	if s, ok := args["questionnaire"].(string); ok {
		if m := todoLine.FindStringSubmatch(firstLine(s)); m != nil {
			return m[2]
		}
		return firstLine(s)
	}
	return ""
}
