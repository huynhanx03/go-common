package websocket

import "sync"

type encodedFrame struct {
	payload []byte
}

type mailboxNode struct {
	frame       *encodedFrame
	class       DeliveryClass
	coalesceKey string
	charge      *chargeToken
	previous    *mailboxNode
	next        *mailboxNode
}

type chargeToken struct {
	once       sync.Once
	hub        *Hub
	connection *Conn
	bytes      int64
}

func (token *chargeToken) releaseLocked() {
	if token == nil {
		return
	}
	token.once.Do(func() {
		mailbox := token.connection.mailbox
		mailbox.items--
		mailbox.bytes -= token.bytes
		token.hub.queuedItems--
		token.hub.queuedBytes -= token.bytes
	})
}

type mailbox struct {
	head   *mailboxNode
	tail   *mailboxNode
	latest map[string]*mailboxNode
	items  int
	bytes  int64
	wake   chan struct{}
}

func newMailbox() *mailbox {
	return &mailbox{
		latest: make(map[string]*mailboxNode),
		wake:   make(chan struct{}, 1),
	}
}

func (hub *Hub) offerFrame(
	connection *Conn,
	frame *encodedFrame,
	class DeliveryClass,
	coalesceKey string,
) error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.status != hubRunning || connection.state != connectionOpen {
		return ErrClosed
	}
	hub.offerFrameLocked(connection, frame, class, coalesceKey)
	return nil
}

func (hub *Hub) offerFrameLocked(
	connection *Conn,
	frame *encodedFrame,
	class DeliveryClass,
	coalesceKey string,
) {
	mailbox := connection.mailbox
	size := int64(len(frame.payload))
	if class == DeliveryLatestState {
		if existing := mailbox.latest[coalesceKey]; existing != nil {
			delta := size - existing.charge.bytes
			if delta > 0 &&
				(mailbox.bytes+delta > hub.options.WriteQueueByteCapacity ||
					hub.queuedBytes+delta > hub.options.MaxHubQueuedBytes) {
				hub.metrics.droppedLatest.Add(1)
				return
			}
			existing.charge.releaseLocked()
			existing.frame = frame
			existing.charge = hub.reserveChargeLocked(connection, size)
			hub.metrics.coalesced.Add(1)
			hub.signalMailbox(mailbox)
			return
		}
	}

	if mailbox.items >= hub.options.WriteQueueCapacity ||
		mailbox.bytes+size > hub.options.WriteQueueByteCapacity ||
		hub.queuedBytes+size > hub.options.MaxHubQueuedBytes {
		switch class {
		case DeliveryLatestState:
			hub.metrics.droppedLatest.Add(1)
		case DeliveryEphemeral:
			hub.metrics.droppedEphemeral.Add(1)
		case DeliveryCritical:
			hub.metrics.criticalClosed.Add(1)
			hub.markClosingLocked(connection, CloseOptions{
				Code:   CloseTryAgainLater,
				Reason: "critical delivery overloaded",
			})
		}
		return
	}

	node := &mailboxNode{
		frame:       frame,
		class:       class,
		coalesceKey: coalesceKey,
		charge:      hub.reserveChargeLocked(connection, size),
		previous:    mailbox.tail,
	}
	if mailbox.tail == nil {
		mailbox.head = node
	} else {
		mailbox.tail.next = node
	}
	mailbox.tail = node
	if class == DeliveryLatestState {
		mailbox.latest[coalesceKey] = node
	}
	hub.metrics.enqueued.Add(1)
	hub.signalMailbox(mailbox)
}

func (hub *Hub) reserveChargeLocked(
	connection *Conn,
	bytes int64,
) *chargeToken {
	connection.mailbox.items++
	connection.mailbox.bytes += bytes
	hub.queuedItems++
	hub.queuedBytes += bytes
	return &chargeToken{
		hub:        hub,
		connection: connection,
		bytes:      bytes,
	}
}

func (hub *Hub) signalMailbox(mailbox *mailbox) {
	select {
	case mailbox.wake <- struct{}{}:
	default:
	}
}

func (hub *Hub) dequeue(connection *Conn) (*encodedFrame, bool) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	mailbox := connection.mailbox
	if mailbox == nil || mailbox.head == nil {
		return nil, false
	}
	node := mailbox.head
	mailbox.head = node.next
	if mailbox.head == nil {
		mailbox.tail = nil
	} else {
		mailbox.head.previous = nil
	}
	if node.class == DeliveryLatestState &&
		mailbox.latest[node.coalesceKey] == node {
		delete(mailbox.latest, node.coalesceKey)
	}
	node.charge.releaseLocked()
	hub.metrics.dequeued.Add(1)
	return node.frame, true
}

func (hub *Hub) clearMailboxLocked(connection *Conn) {
	if connection == nil || connection.mailbox == nil {
		return
	}
	mailbox := connection.mailbox
	for node := mailbox.head; node != nil; node = node.next {
		node.charge.releaseLocked()
	}
	mailbox.head = nil
	mailbox.tail = nil
	clear(mailbox.latest)
}
