package a2a

import "sync"

// Broker distributes already-persisted events to live SSE subscribers. Replay
// is intentionally delegated to Store so reconnects survive process restarts.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string]map[uint64]chan StoredEvent
	nextID      uint64
}

func NewBroker() *Broker {
	return &Broker{subscribers: make(map[string]map[uint64]chan StoredEvent)}
}

func (b *Broker) Subscribe(taskID string) (<-chan StoredEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := b.nextID
	channel := make(chan StoredEvent, 64)
	if b.subscribers[taskID] == nil {
		b.subscribers[taskID] = make(map[uint64]chan StoredEvent)
	}
	b.subscribers[taskID][id] = channel

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			subscribers := b.subscribers[taskID]
			if current, ok := subscribers[id]; ok {
				delete(subscribers, id)
				close(current)
			}
			if len(subscribers) == 0 {
				delete(b.subscribers, taskID)
			}
		})
	}
	return channel, cancel
}

func (b *Broker) Publish(event StoredEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subscribers := b.subscribers[event.TaskID]
	for id, channel := range subscribers {
		select {
		case channel <- event:
		default:
			// Force a slow subscriber to reconnect and replay from its last
			// acknowledged cursor instead of silently creating a stream gap.
			delete(subscribers, id)
			close(channel)
		}
	}
	if len(subscribers) == 0 {
		delete(b.subscribers, event.TaskID)
	}
}
