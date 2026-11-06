package eventbus

import (
	"testing"
	"time"
)

func TestPublishAssignsSequenceAndReplays(t *testing.T) {
	bus := New(10)
	first := bus.Publish(Event{Type: EventOrderAccepted, AccountID: "a"})
	second := bus.Publish(Event{Type: EventOrderFilled, AccountID: "b"})
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("seq = %d,%d", first.Seq, second.Seq)
	}

	sub := bus.Subscribe(10, false, nil, first.Seq)
	defer sub.Close()

	select {
	case event := <-sub.C:
		if event.Seq != second.Seq || event.Type != EventOrderFilled {
			t.Fatalf("unexpected replay: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("replay timed out")
	}
}

func TestFilterAndLiveDelivery(t *testing.T) {
	bus := New(10)
	sub := bus.Subscribe(10, false, func(e Event) bool { return e.AccountID == "a" }, 0)
	defer sub.Close()

	bus.Publish(Event{Type: EventOrderAccepted, AccountID: "b"})
	want := bus.Publish(Event{Type: EventOrderAccepted, AccountID: "a"})

	select {
	case event := <-sub.C:
		if event.Seq != want.Seq {
			t.Fatalf("got seq %d want %d", event.Seq, want.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("delivery timed out")
	}
}

func TestSlowConsumerIsDisconnected(t *testing.T) {
	bus := New(10)
	sub := bus.Subscribe(1, false, nil, 0)
	defer sub.Close()

	bus.Publish(Event{Type: EventOrderAccepted})
	bus.Publish(Event{Type: EventOrderAccepted})

	select {
	case err := <-sub.Err:
		if err != ErrSlowConsumer {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected slow consumer error")
	}
}

func TestDropOnFullSkipsEvents(t *testing.T) {
	bus := New(10)
	sub := bus.Subscribe(1, true, nil, 0)
	defer sub.Close()

	bus.Publish(Event{Type: EventMarketTick})
	bus.Publish(Event{Type: EventMarketTick})

	select {
	case <-sub.Err:
		t.Fatal("drop-on-full subscriber must not report errors")
	case <-time.After(50 * time.Millisecond):
	}
}
