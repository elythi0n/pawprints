package store

import (
	"regexp"
	"strings"
)

// promptTags are wrappers Claude Code puts around prompts it submits itself.
var promptTags = regexp.MustCompile(`</?[a-z][a-z0-9-]*>`)

// ReadablePrompt turns slash commands and system-submitted prompts into the
// short text a person would recognize.
func ReadablePrompt(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "<task-notification>"):
		return "(background task finished)"
	case strings.Contains(s, "<command-name>"):
		name := between(s, "<command-name>", "</command-name>")
		args := between(s, "<command-args>", "</command-args>")
		return strings.TrimSpace(name + " " + args)
	case strings.HasPrefix(s, "<"):
		return strings.TrimSpace(promptTags.ReplaceAllString(s, " "))
	}
	return s
}

func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	if j := strings.Index(rest, close); j >= 0 {
		return rest[:j]
	}
	return rest
}

// Delivered reports whether a prompt is a message Claude Code put into the
// conversation itself, such as a background subagent's report or a finished
// task's notice, rather than one the person sent. It continues the prompt
// that started the work, so it is not a turn of its own.
func Delivered(prompt string) bool {
	s := strings.TrimSpace(prompt)
	return strings.HasPrefix(s, "<agent-message ") || strings.HasPrefix(s, "<task-notification>")
}
