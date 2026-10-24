// Package hook implements the commands Claude Code runs on every edit and
// session event. Everything here is on the hot path: parse stdin once, do
// the minimum, exit 0 no matter what.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/elythi0n/pawprints/internal/install"
	"github.com/elythi0n/pawprints/internal/store"
)

// Input is the JSON Claude Code writes to the hook's stdin. Only the fields
// Pawprints reads are listed; unknown fields are ignored.
type Input struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	CWD            string          `json:"cwd"`
	HookEvent      string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	Source         string          `json:"source"`
	Reason         string          `json:"reason"`
	Prompt         string          `json:"prompt"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	// AgentID and AgentType name the subagent a hook fired in.
	AgentID              string `json:"agent_id"`
	AgentType            string `json:"agent_type"`
	LastAssistantMessage string `json:"last_assistant_message"`
	AgentTranscriptPath  string `json:"agent_transcript_path"`
	NewCWD               string `json:"new_cwd"`
}

// ReadInput parses stdin.
func ReadInput(r io.Reader) (Input, error) {
	data, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return Input{}, err
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return Input{}, fmt.Errorf("hook input: %w", err)
	}
	if in.CWD == "" {
		in.CWD, _ = os.Getwd()
	}
	return in, nil
}

// TargetPath extracts the edited file from tool_input for the edit tools.
func (in Input) TargetPath() string {
	if len(in.ToolInput) == 0 {
		return ""
	}
	var ti struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	if json.Unmarshal(in.ToolInput, &ti) != nil {
		return ""
	}
	if ti.FilePath != "" {
		return ti.FilePath
	}
	return ti.NotebookPath
}

// Run executes one hook event and always returns exit code 0. Problems go to
// stderr, where Claude Code keeps them out of the conversation.
func Run(event string, stdin io.Reader, stderr io.Writer) int {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "pawprints hook %s: panic: %v\n", event, r)
		}
	}()
	// With both settings.json hooks and the plugin in place every edit would
	// be recorded twice; the plugin's own calls carry PAWPRINTS_VIA_PLUGIN.
	if os.Getenv("PAWPRINTS_VIA_PLUGIN") == "" && install.PluginActive() {
		return 0
	}
	in, err := ReadInput(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "pawprints hook %s: %v\n", event, err)
		return 0
	}
	start := time.Now()
	switch event {
	case "pre":
		err = runPre(in)
	case "post":
		err = runPost(in)
	case "session-start":
		err = runSessionStart(in)
	case "session-end":
		err = runSessionEnd(in)
	case "prompt":
		err = runPrompt(in)
	case "stop":
		err = runStop(in)
	case "bash-pre", "command-pre":
		err = runCommandPre(in)
	case "bash-post", "command-post":
		err = runCommandPost(in)
	case "agent-start":
		err = runAgentStart(in)
	case "agent-stop":
		err = runAgentStop(in)
	case "cwd":
		err = runCwd(in)
	default:
		err = fmt.Errorf("unknown hook event %q", event)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pawprints hook %s: %v\n", event, err)
	}
	if os.Getenv("PAWPRINTS_HOOK_TIMING") != "" {
		fmt.Fprintf(stderr, "pawprints hook %s: %s\n", event, time.Since(start))
	}
	return 0
}

// MaxPromptChars is how much of a prompt is kept on the session record.
const MaxPromptChars = 200

func truncatePrompt(s string) string {
	s = store.ReadablePrompt(s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= MaxPromptChars {
		return s
	}
	return s[:MaxPromptChars-1] + "…"
}

func jsonUnmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}
