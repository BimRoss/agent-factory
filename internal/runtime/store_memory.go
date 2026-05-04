package runtime

import "sync"

type MemoryStore struct {
	mu     sync.RWMutex
	tasks  map[string]Task
	traces map[string][]TraceEntry
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:  map[string]Task{},
		traces: map[string][]TraceEntry{},
	}
}

func (s *MemoryStore) SaveTask(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.ID] = task
	return nil
}

func (s *MemoryStore) GetTask(taskID string) (Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[taskID]
	return task, ok
}

func (s *MemoryStore) AppendTrace(taskID string, entry TraceEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces[taskID] = append(s.traces[taskID], entry)
	return nil
}

func (s *MemoryStore) ListTrace(taskID string) []TraceEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := s.traces[taskID]
	out := make([]TraceEntry, len(entries))
	copy(out, entries)
	return out
}
