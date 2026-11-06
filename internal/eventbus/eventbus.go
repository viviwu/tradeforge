package eventbus

import (
	"errors"
	"sync"
	"time"
)

type EventType string

const (
	EventOrderAccepted      EventType = "ORDER_ACCEPTED"
	EventOrderPartiallyFill EventType = "ORDER_PARTIALLY_FILLED"
	EventOrderFilled        EventType = "ORDER_FILLED"
	EventOrderCanceled      EventType = "ORDER_CANCELED"
	EventOrderRejected      EventType = "ORDER_REJECTED"
	EventTrade              EventType = "TRADE"
	EventPositionUpdated    EventType = "POSITION_UPDATED"
	EventStrategyStatus     EventType = "STRATEGY_STATUS"
	EventMarketTick         EventType = "MARKET_TICK"
	EventMarketBook         EventType = "MARKET_BOOK"
)

var ErrSlowConsumer = errors.New("slow consumer")

type Event struct {
	Seq       uint64
	Type      EventType
	AccountID string
	Symbol    string
	Time      time.Time
	Payload   any
}

type Filter func(Event) bool

type Subscription struct {
	C     <-chan Event
	Err   <-chan error
	bus   *Bus
	id    uint64
	mutex sync.Once
}

func (s *Subscription) Close() {
	s.mutex.Do(func() { s.bus.unsubscribe(s.id) })
}

type subscriber struct {
	id         uint64
	ch         chan Event
	errCh      chan error
	filter     Filter
	dropOnFull bool
	overflow   bool
}

type Bus struct {
	mu        sync.Mutex
	nextID    uint64
	seq       uint64
	subs      map[uint64]*subscriber
	replay    []Event
	replayCap int
}

func New(replayCap int) *Bus {
	if replayCap <= 0 {
		replayCap = 4096
	}
	return &Bus{subs: make(map[uint64]*subscriber), replayCap: replayCap}
}

func (b *Bus) Publish(e Event) Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	e.Seq = b.seq
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	b.replay = append(b.replay, e)
	if len(b.replay) > b.replayCap {
		b.replay = b.replay[len(b.replay)-b.replayCap:]
	}
	for id, sub := range b.subs {
		if sub.filter != nil && !sub.filter(e) {
			continue
		}
		if sub.dropOnFull {
			select {
			case sub.ch <- e:
			default:
			}
			continue
		}
		select {
		case sub.ch <- e:
		default:
			sub.overflow = true
			select {
			case sub.errCh <- ErrSlowConsumer:
			default:
			}
			delete(b.subs, id)
		}
	}
	return e
}

func (b *Bus) Subscribe(buffer int, dropOnFull bool, filter Filter, fromSeq uint64) *Subscription {
	if buffer <= 0 {
		buffer = 256
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	sub := &subscriber{
		id:         b.nextID,
		ch:         make(chan Event, buffer),
		errCh:      make(chan error, 1),
		filter:     filter,
		dropOnFull: dropOnFull,
	}
	if fromSeq > 0 {
		start := len(b.replay)
		for i, e := range b.replay {
			if e.Seq > fromSeq {
				start = i
				break
			}
		}
		replay := b.replay[start:]
		if len(replay) > buffer {
			replay = replay[len(replay)-buffer:]
		}
		for _, e := range replay {
			if filter != nil && !filter(e) {
				continue
			}
			select {
			case sub.ch <- e:
			default:
			}
		}
	}
	b.subs[sub.id] = sub
	return &Subscription{C: sub.ch, Err: sub.errCh, bus: b, id: sub.id}
}

func (b *Bus) unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, id)
}

func (b *Bus) CurrentSeq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

func (b *Bus) Replay(fromSeq uint64, limit int, filter Filter) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if limit <= 0 || limit > len(b.replay) {
		limit = len(b.replay)
	}
	out := make([]Event, 0, limit)
	for _, e := range b.replay {
		if e.Seq <= fromSeq {
			continue
		}
		if filter != nil && !filter(e) {
			continue
		}
		out = append(out, e)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}
