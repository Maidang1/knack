package manage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"knack/internal/fsutil"
)

// Invocation is the user's Claude Code skill invocation preference.
// Front matter and project or managed settings may restrict it further.
type Invocation string

const (
	InvocationAuto     Invocation = "on"
	InvocationManual   Invocation = "user-invocable-only"
	InvocationOff      Invocation = "off"
	InvocationNameOnly Invocation = "name-only"
)

const opInvocation = "set-invocation"

var invocationMu sync.Mutex

type invocationUndo struct {
	Name             string          `json:"name"`
	Old              json.RawMessage `json:"old,omitempty"`
	New              Invocation      `json:"new"`
	OverridesExisted bool            `json:"overridesExisted"`
	FileExisted      bool            `json:"fileExisted"`
}

type invocationSettings struct {
	fields    map[string]json.RawMessage
	overrides map[string]json.RawMessage
	bytes     []byte
	mode      os.FileMode
	existed   bool
}

// ReadInvocations reads explicit preferences from ~/.claude/settings.json.
// A name absent from the returned map has InvocationAuto as its preference.
func ReadInvocations(home string) (map[string]Invocation, error) {
	path, err := invocationPath(home)
	if err != nil {
		return nil, err
	}
	s, err := readInvocationSettings(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Invocation, len(s.overrides))
	for name, raw := range s.overrides {
		var mode Invocation
		if err := json.Unmarshal(raw, &mode); err != nil || !validInvocation(mode) {
			return nil, fmt.Errorf("skillOverrides[%q] 的调用方式无效", name)
		}
		out[name] = mode
	}
	return out, nil
}

// ReadInvocation returns one explicit preference, or InvocationAuto if unset.
func ReadInvocation(home, name string) (Invocation, error) {
	modes, err := ReadInvocations(home)
	if err != nil {
		return "", err
	}
	if mode, ok := modes[name]; ok {
		return mode, nil
	}
	return InvocationAuto, nil
}

// SetInvocation changes only skillOverrides[name], keeping an undo record.
// Callers use the skill's front matter name, falling back to its directory name.
func SetInvocation(home, name string, mode Invocation, undoRoot string) (Record, error) {
	if err := plainName(name); err != nil {
		return Record{}, err
	}
	if !validInvocation(mode) {
		return Record{}, errors.New("不认识的 skill 调用方式")
	}
	path, err := invocationPath(home)
	if err != nil {
		return Record{}, err
	}
	invocationMu.Lock()
	defer invocationMu.Unlock()
	s, err := readInvocationSettings(path)
	if err != nil {
		return Record{}, err
	}
	old := s.overrides[name]
	value, _ := json.Marshal(mode)
	if sameJSON(old, value) {
		return Record{}, errors.New("已经是这个调用方式")
	}
	rec, err := begin(undoRoot, opInvocation, path)
	if err != nil {
		return Record{}, err
	}
	_, hadOverrides := s.fields["skillOverrides"]
	rec.Invocation = &invocationUndo{Name: name, Old: old, New: mode,
		OverridesExisted: hadOverrides, FileExisted: s.existed}
	if s.existed {
		rec.Payload = filepath.Join(rec.Dir, "settings.json")
		if err := os.WriteFile(rec.Payload, s.bytes, 0o600); err != nil {
			_ = os.RemoveAll(rec.Dir)
			return Record{}, err
		}
	}
	// Both the backup and record must exist before the settings file changes.
	if err := save(rec); err != nil {
		_ = os.RemoveAll(rec.Dir)
		return Record{}, err
	}
	s.overrides[name] = value
	data, err := s.encode()
	if err == nil {
		err = writeInvocationSettings(path, s, data)
	}
	if err != nil {
		_ = os.RemoveAll(rec.Dir)
		return Record{}, err
	}
	return rec, nil
}

func restoreInvocation(r Record) error {
	if r.Invocation == nil || !validInvocation(r.Invocation.New) {
		return errors.New("调用方式撤销记录无效")
	}
	invocationMu.Lock()
	defer invocationMu.Unlock()
	s, err := readInvocationSettings(r.LinkPath)
	if err != nil {
		return err
	}
	u := r.Invocation
	want, _ := json.Marshal(u.New)
	if !sameJSON(s.overrides[u.Name], want) {
		return errors.New("这个 skill 的调用方式已被修改，没有覆盖后续设置")
	}
	if len(u.Old) == 0 {
		delete(s.overrides, u.Name)
	} else {
		s.overrides[u.Name] = u.Old
	}
	if len(s.overrides) == 0 && !u.OverridesExisted {
		delete(s.fields, "skillOverrides")
	} else {
		raw, err := json.Marshal(s.overrides)
		if err != nil {
			return err
		}
		s.fields["skillOverrides"] = raw
	}
	if len(s.fields) == 0 && !u.FileExisted {
		if err := expectInvocationSettings(r.LinkPath, s); err != nil {
			return err
		}
		return os.Remove(r.LinkPath)
	}
	data, err := json.MarshalIndent(s.fields, "", "  ")
	if err != nil {
		return err
	}
	return writeInvocationSettings(r.LinkPath, s, append(data, '\n'))
}

func validInvocation(mode Invocation) bool {
	switch mode {
	case InvocationAuto, InvocationManual, InvocationOff, InvocationNameOnly:
		return true
	}
	return false
}

func invocationPath(home string) (string, error) {
	if home == "" {
		return "", errors.New("没有用户主目录")
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func readInvocationSettings(path string) (invocationSettings, error) {
	s := invocationSettings{fields: make(map[string]json.RawMessage),
		overrides: make(map[string]json.RawMessage), mode: 0o600}
	if err := checkInvocationPath(path); err != nil {
		return s, err
	}
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !st.Mode().IsRegular() {
		return s, errors.New("Claude 设置不是普通文件")
	}
	s.existed, s.mode = true, st.Mode().Perm()
	s.bytes, err = os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(s.bytes, &s.fields); err != nil || s.fields == nil {
		return s, errors.New("Claude settings.json 不是有效的 JSON 对象")
	}
	if raw, ok := s.fields["skillOverrides"]; ok {
		if err := json.Unmarshal(raw, &s.overrides); err != nil || s.overrides == nil {
			return s, errors.New("Claude skillOverrides 不是有效的 JSON 对象")
		}
	}
	return s, nil
}

func (s invocationSettings) encode() ([]byte, error) {
	raw, err := json.Marshal(s.overrides)
	if err != nil {
		return nil, err
	}
	s.fields["skillOverrides"] = raw
	data, err := json.MarshalIndent(s.fields, "", "  ")
	return append(data, '\n'), err
}

func checkInvocationPath(path string) error {
	for _, p := range []string{filepath.Dir(path), path} {
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if fsutil.IsLink(p, st) {
			return errors.New("不会通过软链读写 Claude 设置")
		}
	}
	return nil
}

func expectInvocationSettings(path string, before invocationSettings) error {
	current, err := readInvocationSettings(path)
	if err != nil {
		return err
	}
	if current.existed != before.existed || !bytes.Equal(current.bytes, before.bytes) {
		return errors.New("Claude 设置已被修改，请刷新后重试")
	}
	return nil
}

func writeInvocationSettings(path string, before invocationSettings, data []byte) error {
	if err := checkInvocationPath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(before.mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := expectInvocationSettings(path, before); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func sameJSON(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var ca, cb bytes.Buffer
	return json.Compact(&ca, a) == nil && json.Compact(&cb, b) == nil && bytes.Equal(ca.Bytes(), cb.Bytes())
}
