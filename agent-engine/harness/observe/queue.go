package observe

import (
	"sync"

	core "github.com/hycjack/crux-ai/core"
)

// QueueMode controls how Steering / FollowUp messages are scheduled
// relative to the in-flight turn.
type QueueMode string

const (
	// QueueModeInterrupt aborts the current turn, then resumes with
	// the new prompt. The aborted turn is preserved in the message
	// history (with StopAborted).
	QueueModeInterrupt QueueMode = "interrupt"

	// QueueModeOneShot interrupts the current turn once, regardless
	// of how many messages are queued. Use this for a single steering
	// note.
	QueueModeOneShot QueueMode = "one_shot"

	// QueueModeQueue waits for the current turn to finish, then
	// appends the queued messages as a single follow-up batch.
	QueueModeQueue QueueMode = "queue"

	// QueueModeSteer injects the message into the current turn at
	// the next yield checkpoint. Use this for in-flight steering.
	QueueModeSteer QueueMode = "steer"
)

// MessageQueue is a thread-safe FIFO of pending messages with an
// associated queue mode. The agent loop polls the queue at yield
// checkpoints; the consumer-side code enqueues via the public methods.
//
// A single MessageQueue is shared between steering and follow-up
// slots: each slot has its own mode but they share the underlying
// goroutine-safe store.
type MessageQueue struct {
	mu     sync.Mutex
	steer  []core.Message
	follow []core.Message
	mode   QueueMode
}

// NewMessageQueue returns an empty queue. The initial mode defaults to
// QueueModeSteer.
func NewMessageQueue() *MessageQueue {
	return &MessageQueue{mode: QueueModeSteer}
}

// SetMode updates the active queue mode.
func (q *MessageQueue) SetMode(m QueueMode) {
	q.mu.Lock()
	q.mode = m
	q.mu.Unlock()
}

// Mode returns the current queue mode.
func (q *MessageQueue) Mode() QueueMode {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.mode
}

// Steer enqueues one or more messages for in-flight steering.
func (q *MessageQueue) Steer(msgs ...core.Message) {
	q.mu.Lock()
	q.steer = append(q.steer, msgs...)
	q.mu.Unlock()
}

// FollowUp enqueues one or more messages for after the current turn.
func (q *MessageQueue) FollowUp(msgs ...core.Message) {
	q.mu.Lock()
	q.follow = append(q.follow, msgs...)
	q.mu.Unlock()
}

// DrainSteering returns the pending steering messages, clearing the
// queue. The agent loop calls this at every yield checkpoint.
func (q *MessageQueue) DrainSteering() []core.Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.steer) == 0 {
		return nil
	}
	out := q.steer
	q.steer = nil
	return out
}

// DrainFollowUp returns the pending follow-up messages, clearing the
// queue. The agent loop calls this at the end of the outer loop.
func (q *MessageQueue) DrainFollowUp() []core.Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.follow) == 0 {
		return nil
	}
	out := q.follow
	q.follow = nil
	return out
}

// DrainAll returns both steering and follow-up messages in order, then
// clears the queue. Used by QueueModeInterrupt and QueueModeOneShot.
func (q *MessageQueue) DrainAll() (steer, follow []core.Message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	steer, follow = q.steer, q.follow
	q.steer, q.follow = nil, nil
	return
}

// Len reports the number of queued messages (steering + follow-up).
func (q *MessageQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.steer) + len(q.follow)
}
