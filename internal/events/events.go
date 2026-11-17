package events

import (
	"sync"
	"time"
)

type Event struct {
	Type    string      `json:"type"`
	JobID   string      `json:"job_id"`
	TaskID  string      `json:"task_id,omitempty"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	TS      time.Time   `json:"ts"`
}

// Bus 是一个按 job 订阅的内存事件总线，驱动 SSE 实时推送。
type Bus struct {
	mu          sync.Mutex
	subscribers map[string]map[chan Event]struct{}
}

func New() *Bus {
	return &Bus{subscribers: map[string]map[chan Event]struct{}{}}
}

func (b *Bus) Subscribe(jobID string) chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribers[jobID] == nil {
		b.subscribers[jobID] = map[chan Event]struct{}{}
	}
	b.subscribers[jobID][ch] = struct{}{}
	return ch
}

func (b *Bus) Unsubscribe(jobID string, ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if subs, ok := b.subscribers[jobID]; ok {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(b.subscribers, jobID)
		}
	}
}

func (b *Bus) Publish(jobID string, ev Event) {
	ev.JobID = jobID
	if ev.TS.IsZero() {
		ev.TS = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subscribers[jobID] {
		select {
		case ch <- ev:
		default: // 丢弃以避免阻塞慢消费者
		}
	}
}
