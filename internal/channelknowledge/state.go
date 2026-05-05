package channelknowledge

import (
	"encoding/json"
	"strings"
)

// HarvestState tracks incremental Slack harvest cursors per channel (JSON in Redis).
type HarvestState struct {
	// HistoryWatermark is the max Slack message ts seen on the channel timeline
	// (conversations.history). Next history call uses Oldest=HistoryWatermark with Inclusive=false.
	HistoryWatermark string `json:"hw,omitempty"`

	// ThreadCursors maps thread root ts -> max message ts ingested in that thread (for conversations.replies Oldest).
	ThreadCursors map[string]string `json:"t,omitempty"`

	// ThreadPollRR rotates through ThreadCursors for fair per-tick polling.
	ThreadPollRR int `json:"rr,omitempty"`
}

// Copy returns a deep copy for mutation.
func (s *HarvestState) Copy() *HarvestState {
	if s == nil {
		return nil
	}
	out := &HarvestState{
		HistoryWatermark: strings.TrimSpace(s.HistoryWatermark),
		ThreadPollRR:     s.ThreadPollRR,
		ThreadCursors:    make(map[string]string, len(s.ThreadCursors)),
	}
	for k, v := range s.ThreadCursors {
		out.ThreadCursors[k] = v
	}
	return out
}

func normalizeHarvestState(s *HarvestState) *HarvestState {
	if s == nil {
		return nil
	}
	s.HistoryWatermark = strings.TrimSpace(s.HistoryWatermark)
	if s.ThreadCursors == nil {
		s.ThreadCursors = make(map[string]string)
	}
	return s
}

func unmarshalHarvestState(data []byte) (*HarvestState, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var s HarvestState
	if b, ok := raw["hw"]; ok {
		_ = json.Unmarshal(b, &s.HistoryWatermark)
	}
	// Legacy single watermark (pre-split); treat as history cursor.
	if s.HistoryWatermark == "" {
		if b, ok := raw["w"]; ok {
			_ = json.Unmarshal(b, &s.HistoryWatermark)
		}
	}
	if b, ok := raw["t"]; ok {
		_ = json.Unmarshal(b, &s.ThreadCursors)
	}
	if b, ok := raw["rr"]; ok {
		_ = json.Unmarshal(b, &s.ThreadPollRR)
	}
	return normalizeHarvestState(&s), nil
}
