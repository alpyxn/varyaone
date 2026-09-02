package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ControlResult is the durable result of the most recent elevated panel
// command. Elevated child-process stderr is not reliably inherited through
// PowerShell/UAC, so this small file is the diagnostic hand-off to the panel.
type ControlResult struct {
	Action string    `json:"action"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

func controlResultPath() string {
	return filepath.Join(DiscoverLayout().Logs(), "control-result.json")
}

func recordControlResult(action string, commandErr error) {
	result := ControlResult{Action: action, OK: commandErr == nil, At: time.Now().UTC()}
	if commandErr != nil {
		result.Error = commandErr.Error()
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return
	}
	path := controlResultPath()
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "control-result-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return
	}
	if err = tmp.Close(); err != nil {
		return
	}
	// os.Rename does not replace an existing regular file on every supported
	// Windows version. The record is advisory and tiny, so a brief missing-file
	// window is preferable to silently keeping a stale failure forever.
	_ = os.Remove(path)
	if err = os.Rename(tmpName, path); err != nil {
		// Antivirus/indexer races can transiently block a rename on Windows. A
		// non-atomic fresh write is still better than losing the only detailed
		// error the non-elevated panel can show.
		_ = os.WriteFile(path, payload, 0o644)
	}
}

// LastControlResult returns the latest elevated command result, if readable.
func LastControlResult() (ControlResult, error) {
	var result ControlResult
	payload, err := os.ReadFile(controlResultPath())
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(payload, &result)
	return result, err
}

// StackLogTail returns a bounded diagnostic excerpt without loading a runaway
// log into the panel process.
func StackLogTail(maxBytes int) string {
	if maxBytes <= 0 || maxBytes > 64<<10 {
		maxBytes = 8 << 10
	}
	payload, err := os.ReadFile(filepath.Join(DiscoverLayout().Logs(), "stack.log"))
	if err != nil {
		return ""
	}
	if len(payload) > maxBytes {
		payload = payload[len(payload)-maxBytes:]
		if newline := strings.IndexByte(string(payload), '\n'); newline >= 0 {
			payload = payload[newline+1:]
		}
	}
	return strings.TrimSpace(string(payload))
}

// StackFailure turns "the stack is not serving" into the actual reason, by
// pulling the last logged error out of stack.log. Without it every startup
// problem — a port clash, a broken PostgreSQL bundle, a failed migration —
// reaches the installer and the control panel as the same bare timeout.
func StackFailure(prefix string) error {
	tail := StackLogTail(8 << 10)
	if tail == "" {
		return errors.New(prefix + "; stack.log henüz bir hata içermiyor")
	}
	lines := strings.Split(tail, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var entry struct {
			Message string `json:"msg"`
			Error   string `json:"error"`
		}
		if json.Unmarshal([]byte(lines[i]), &entry) == nil && entry.Error != "" {
			return fmt.Errorf("%s: %s", prefix, entry.Error)
		}
	}
	return fmt.Errorf("%s; ayrıntı için stack.log dosyasına bakın", prefix)
}
