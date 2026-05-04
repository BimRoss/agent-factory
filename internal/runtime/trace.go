package runtime

import "time"

type TraceEntry struct {
	Sequence       int
	EmployeeID     string
	SkillID        string
	Status         string
	Note           string
	RenderOutputID string
	Timestamp      time.Time
}
