package solo

import "strings"

var canonicalTaskStatuses = []string{"draft", "ready", "active", "completed", "failed", "blocked", "cancelled"}

func normalizeTaskStatus(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "draft", "ready", "active", "completed", "failed", "blocked", "cancelled":
		return s, true
	case "open", "triaged":
		return "draft", true
	case "in_progress", "in-review", "in_review":
		return "active", true
	case "done":
		return "completed", true
	default:
		return "", false
	}
}

func canonicalTaskStatus(raw string) string {
	if s, ok := normalizeTaskStatus(raw); ok {
		return s
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func legacyTaskStatus(canonical string) string {
	switch canonical {
	case "draft":
		return "open"
	case "active":
		return "in_progress"
	case "completed":
		return "done"
	default:
		return canonical
	}
}

func parsePriorityValue(raw string, fallback int) int {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "":
		return fallback
	case "low", "p3":
		return 2
	case "medium", "normal", "p2":
		return 3
	case "high", "p1":
		return 4
	case "critical", "urgent", "p0":
		return 5
	}
	// Numeric compatibility path.
	if raw == "1" || raw == "2" {
		return 2
	}
	if raw == "3" {
		return 3
	}
	if raw == "4" {
		return 4
	}
	if raw == "5" {
		return 5
	}
	return fallback
}

func priorityLabel(v int) string {
	switch {
	case v >= 5:
		return "critical"
	case v == 4:
		return "high"
	case v == 3:
		return "medium"
	default:
		return "low"
	}
}
