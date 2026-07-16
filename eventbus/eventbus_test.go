package eventbus

import (
	"bytes"
	"testing"
)

func TestBrokerPublishesToSubscribers(t *testing.T) {
	broker := NewBroker[string]()
	sub, unsubscribe := broker.Subscribe(1)
	defer unsubscribe()

	broker.Publish("ready")

	select {
	case got := <-sub:
		if got != "ready" {
			t.Fatalf("got %q, want ready", got)
		}
	default:
		t.Fatal("expected event")
	}
}

func TestBrokerDropsForSlowSubscribers(t *testing.T) {
	broker := NewBroker[int]()
	sub, unsubscribe := broker.Subscribe(1)
	defer unsubscribe()

	broker.Publish(1)
	broker.Publish(2)

	select {
	case got := <-sub:
		if got != 1 {
			t.Fatalf("got %d, want first event", got)
		}
	default:
		t.Fatal("expected first event")
	}

	select {
	case got := <-sub:
		t.Fatalf("got unexpected second event %d", got)
	default:
	}
}

func TestUnsubscribeClosesChannelAndIsIdempotent(t *testing.T) {
	broker := NewBroker[string]()
	sub, unsubscribe := broker.Subscribe(1)

	unsubscribe()
	unsubscribe()
	broker.Publish("ignored")

	if _, ok := <-sub; ok {
		t.Fatal("expected closed channel")
	}
}

func TestBrokerPublisher(t *testing.T) {
	broker := NewBroker[string]()
	sub, unsubscribe := broker.Subscribe(1)
	defer unsubscribe()

	publish := BrokerPublisher(broker)
	publish("ready")

	if got := <-sub; got != "ready" {
		t.Fatalf("got %q, want ready", got)
	}
}

func TestNilBrokerPublisherDropsEvents(t *testing.T) {
	publish := BrokerPublisher[string](nil)
	publish("ignored")
}

func TestJSONPublisher(t *testing.T) {
	var buf bytes.Buffer
	publish := JSONPublisher[struct {
		Kind string `json:"kind"`
	}](&buf)

	publish(struct {
		Kind string `json:"kind"`
	}{Kind: "ready"})

	if got, want := buf.String(), "{\"kind\":\"ready\"}\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
