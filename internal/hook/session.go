package hook

import (
	"time"

	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/usage"
)

// rootFor returns the project a session belongs to: its pin, else the
// directory Claude Code says it was launched in, else the current directory.
// The answer is pinned on first use so later calls are one small file read.
func rootFor(in Input) string {
	if root, ok := store.SessionRoot(in.SessionID); ok {
		return root
	}
	dir := in.CWD
	if launch, ok := sessions.LaunchDir(in.SessionID); ok {
		dir = launch
	}
	root := store.ResolveRoot(dir)
	_ = store.PinSessionRoot(in.SessionID, root, false)
	return root
}

func projectFor(in Input) (*store.Project, error) {
	return store.OpenProject(rootFor(in))
}

// noteAccount is the Claude account this hook runs under, or "". It also
// remembers the account and the config folder, so views can name the account
// and read every folder's sessions.
func noteAccount() string {
	home := sessions.ClaudeHome()
	_ = store.RememberClaudeHome(home)
	acct, ok := sessions.CurrentAccount(home)
	if !ok {
		return ""
	}
	_ = store.RememberAccount(acct, time.Now())
	return acct.ID
}

// recordUsage reads the transcripts' new lines into the record and the
// session's usage log, each reply credited to the account of the prompt it
// answers, so a logout and login as someone else between prompts splits the
// session between the two accounts. It runs inside UpdateSession, under the
// project lock.
func recordUsage(p *store.Project, r *store.SessionRecord, account string) {
	if account != "" {
		r.Account = account
	}
	buckets, rebuilt := usage.Accumulate(r, r.AccountAt)
	if rebuilt {
		_ = p.ResetUsage(r.Session)
	}
	if len(buckets) > 0 {
		_ = p.AppendUsage(r.Session, buckets)
	}
}

func runSessionStart(in Input) error {
	// At startup the hook's cwd is the launch directory; a resume or a
	// clear keeps the session's existing pin.
	if in.Source == "startup" {
		_ = store.PinSessionRoot(in.SessionID, store.ResolveRoot(in.CWD), false)
	}
	p, err := projectFor(in)
	if err != nil {
		return err
	}
	now := store.Now(time.Now())
	account := noteAccount()
	return p.UpdateSession(in.SessionID, func(r *store.SessionRecord) {
		if r.StartedAt == "" {
			r.StartedAt = now
		}
		if account != "" {
			r.Account = account
		}
		if in.Source != "" {
			r.StartSource = in.Source
		}
		r.CWD = in.CWD
		if in.TranscriptPath != "" {
			r.TranscriptPath = in.TranscriptPath
		}
		// A resumed session is alive again.
		r.EndedAt = ""
		r.EndReason = ""
	})
}

func runPrompt(in Input) error {
	// Whatever changed since the last scan belongs before this prompt.
	if err := scanBetween(in); err != nil {
		return err
	}
	// A subagent's report or a finished task's notice continues the prompt
	// that started the work; it is not a turn of its own.
	if store.Delivered(in.Prompt) {
		return nil
	}
	p, err := projectFor(in)
	if err != nil {
		return err
	}
	cfg, _ := store.LoadConfig()
	now := time.Now()
	account := noteAccount()
	text := ""
	if cfg.PromptsEnabled() {
		text = truncatePrompt(in.Prompt)
	}
	return p.UpdateSession(in.SessionID, func(r *store.SessionRecord) {
		if r.StartedAt == "" {
			r.StartedAt = store.Now(now)
		}
		if r.CWD == "" {
			r.CWD = in.CWD
		}
		if in.TranscriptPath != "" {
			r.TranscriptPath = in.TranscriptPath
		}
		if account != "" {
			r.Account = account
		}
		// A prompt sent again, such as a re-run /goal, stays one turn.
		if n := len(r.Turns); n == 0 || !store.RepeatOf(r.Turns[n-1], text, now) {
			r.Turns = append(r.Turns, store.Turn{ID: store.NewID(now), At: store.Now(now), Text: text, Account: r.Account})
		}
		if r.FirstPrompt == "" {
			r.FirstPrompt = text
		}
		r.LastPrompt = text
		r.LastPromptAt = store.Now(now)
		r.EndedAt = ""
		r.EndReason = ""
	})
}

func runStop(in Input) error {
	if err := scanBetween(in); err != nil {
		return err
	}
	p, err := projectFor(in)
	if err != nil {
		return err
	}
	now := store.Now(time.Now())
	account := noteAccount()
	return p.UpdateSession(in.SessionID, func(r *store.SessionRecord) {
		r.LastStopAt = now
		if in.TranscriptPath != "" {
			r.TranscriptPath = in.TranscriptPath
		}
		recordUsage(p, r, account)
	})
}

func runSessionEnd(in Input) error {
	if err := scanBetween(in); err != nil {
		return err
	}
	p, err := projectFor(in)
	if err != nil {
		return err
	}
	p.EndBackground(in.SessionID)
	now := store.Now(time.Now())
	account := noteAccount()
	return p.UpdateSession(in.SessionID, func(r *store.SessionRecord) {
		r.EndedAt = now
		r.EndReason = in.Reason
		if in.TranscriptPath != "" {
			r.TranscriptPath = in.TranscriptPath
		}
		recordUsage(p, r, account)
	})
}

func runAgentStart(in Input) error {
	p, err := projectFor(in)
	if err != nil {
		return err
	}
	now := store.Now(time.Now())
	desc := ""
	// A subagent's task is written by Claude from the person's prompt, so it
	// follows the same setting as prompts.
	if cfg, _ := store.LoadConfig(); cfg.PromptsEnabled() {
		desc = sessions.SubagentDescription(subagentTranscript(in))
	}
	return p.UpdateSession(in.SessionID, func(r *store.SessionRecord) {
		a, ok := r.AgentByID(in.AgentID)
		if !ok {
			r.Agents = append(r.Agents, store.Agent{ID: in.AgentID, Type: in.AgentType})
			a = &r.Agents[len(r.Agents)-1]
		}
		a.StartedAt = now
		if a.Turn == "" {
			a.Turn = r.CurrentTurn()
		}
		if desc != "" {
			a.Description = desc
		}
	})
}

func runAgentStop(in Input) error {
	p, err := projectFor(in)
	if err != nil {
		return err
	}
	now := store.Now(time.Now())
	var desc, report, summary string
	// The task and the report carry the person's words; with prompts not
	// recorded they are not either.
	if cfg, _ := store.LoadConfig(); cfg.PromptsEnabled() {
		transcript := subagentTranscript(in)
		desc = sessions.SubagentDescription(transcript)
		report = sessions.SubagentReport(transcript)
		if report == "" {
			report = sessions.Shorten(in.LastAssistantMessage, sessions.MaxReport)
		}
		summary = sessions.ReportSummary(report)
	}
	return p.UpdateSession(in.SessionID, func(r *store.SessionRecord) {
		a, ok := r.AgentByID(in.AgentID)
		if !ok {
			// Claude Code's own helpers, such as the compaction summarizer,
			// stop without ever starting and have no type. They are not agents.
			if in.AgentType == "" {
				return
			}
			r.Agents = append(r.Agents, store.Agent{ID: in.AgentID, Type: in.AgentType})
			a = &r.Agents[len(r.Agents)-1]
		}
		a.StoppedAt = now
		if summary != "" {
			a.Summary, a.Report = summary, report
		}
		if desc != "" {
			a.Description = desc
		}
	})
}

// runCwd notes a folder outside the project the session moved into, such as
// a worktree, so command scans walk it.
func runCwd(in Input) error {
	p, err := projectFor(in)
	if err != nil {
		return err
	}
	noteDir(p, in, in.NewCWD)
	return nil
}

// subagentTranscript is the path of the subagent's own transcript, or "".
func subagentTranscript(in Input) string {
	if in.AgentTranscriptPath != "" {
		return in.AgentTranscriptPath
	}
	return sessions.SubagentTranscript(in.TranscriptPath, in.AgentID)
}
