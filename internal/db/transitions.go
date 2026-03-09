package db

// ValidTransitions defines the allowed status transitions per spec §5.1.
var ValidTransitions = map[string][]string{
	"open":        {"triaged", "ready", "cancelled"},
	"triaged":     {"ready", "blocked", "cancelled"},
	"ready":       {"in_progress", "blocked", "cancelled"},
	"in_progress": {"in_review", "blocked", "ready", "cancelled"},
	"in_review":   {"done", "in_progress", "blocked", "cancelled"},
	"blocked":     {"ready", "triaged", "cancelled"},
	"done":        {}, // terminal
	"cancelled":   {"open"}, // reopen only
}

// IsValidTransition checks whether a status transition is allowed.
func IsValidTransition(from, to string) bool {
	allowed, ok := ValidTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// AllStatuses returns all valid task statuses.
func AllStatuses() []string {
	return []string{"open", "triaged", "ready", "in_progress", "in_review", "blocked", "done", "cancelled"}
}

// IsValidStatus checks if a status string is valid.
func IsValidStatus(s string) bool {
	for _, valid := range AllStatuses() {
		if s == valid {
			return true
		}
	}
	return false
}

// AllTaskTypes returns all valid task types.
func AllTaskTypes() []string {
	return []string{"task", "bug", "feature", "chore", "spike"}
}

// IsValidTaskType checks if a type string is valid.
func IsValidTaskType(t string) bool {
	for _, valid := range AllTaskTypes() {
		if t == valid {
			return true
		}
	}
	return false
}
