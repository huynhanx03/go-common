package outbox

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRouterFansOutInDeclarationOrderWithImmutableMessages(t *testing.T) {
	t.Parallel()

	order := make([]string, 0, 2)
	first := Route{
		Destination: "account.changed.v1",
		HandlerName: "projection",
		Handle: func(_ context.Context, message Message) error {
			order = append(order, "projection")
			message.Payload[0] = 'X'
			message.Metadata.Attributes[0].Value = "mutated"
			return nil
		},
	}
	second := Route{
		Destination: "account.changed.v1",
		HandlerName: "notification",
		Handle: func(_ context.Context, message Message) error {
			order = append(order, "notification")
			if string(message.Payload) != "payload" ||
				message.Metadata.Attributes[0].Value != "one" {
				t.Fatalf("handler received mutated message: %+v", message)
			}
			return nil
		},
	}
	router, err := NewRouter(first, second)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	message := Message{
		ID: "event-1", Destination: "account.changed.v1",
		Payload:  []byte("payload"),
		Metadata: Metadata{Attributes: []Attribute{{Key: "version", Value: "one"}}},
	}
	ack, err := router.Publish(context.Background(), message)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if ack.Reference != message.ID {
		t.Fatalf("ack reference = %q, want %q", ack.Reference, message.ID)
	}
	if !reflect.DeepEqual(order, []string{"projection", "notification"}) {
		t.Fatalf("handler order = %v", order)
	}
	if string(message.Payload) != "payload" ||
		message.Metadata.Attributes[0].Value != "one" {
		t.Fatalf("router mutated caller message: %+v", message)
	}
}

func TestRouterFailsClosedForMissingRouteAndPanic(t *testing.T) {
	t.Parallel()

	router, err := NewRouter(Route{
		Destination: "account.changed.v1",
		HandlerName: "projection",
		Handle: func(context.Context, Message) error {
			panic("secret must not escape")
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	_, err = router.Publish(context.Background(), Message{
		ID: "event-1", Destination: "unknown.event.v1",
	})
	assertPublishFailure(t, err, failureRouteNotFound, false, false)

	_, err = router.Publish(context.Background(), Message{
		ID: "event-2", Destination: "account.changed.v1",
	})
	assertPublishFailure(t, err, failureRouteHandlerPanic, true, true)
}

func TestRouterRejectsInvalidAndDuplicateRoutes(t *testing.T) {
	t.Parallel()

	valid := Route{
		Destination: "account.changed.v1",
		HandlerName: "projection",
		Handle:      func(context.Context, Message) error { return nil },
	}
	for _, routes := range [][]Route{
		nil,
		{{Destination: "", HandlerName: "projection", Handle: valid.Handle}},
		{{Destination: valid.Destination, HandlerName: "", Handle: valid.Handle}},
		{{Destination: valid.Destination, HandlerName: valid.HandlerName}},
		{valid, valid},
	} {
		if _, err := NewRouter(routes...); !errors.Is(err, ErrInvalidRouter) {
			t.Fatalf("NewRouter(%+v) error = %v, want ErrInvalidRouter", routes, err)
		}
	}
}

func TestRouterDestinationsAreStableAndImmutable(t *testing.T) {
	t.Parallel()

	router, err := NewRouter(
		Route{
			Destination: "z.changed.v1", HandlerName: "z",
			Handle: func(context.Context, Message) error { return nil },
		},
		Route{
			Destination: "a.changed.v1", HandlerName: "a",
			Handle: func(context.Context, Message) error { return nil },
		},
	)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	destinations := router.Destinations()
	if !reflect.DeepEqual(destinations, []string{"a.changed.v1", "z.changed.v1"}) {
		t.Fatalf("destinations = %v", destinations)
	}
	destinations[0] = "mutated"
	if router.Destinations()[0] != "a.changed.v1" {
		t.Fatal("caller mutated router destinations")
	}
}

func assertPublishFailure(
	t *testing.T,
	err error,
	code string,
	retryable bool,
	ambiguous bool,
) {
	t.Helper()
	var failure *PublishError
	if !errors.As(err, &failure) || failure.Code != code ||
		failure.Retryable != retryable || failure.Ambiguous != ambiguous {
		t.Fatalf("failure = %#v, want code=%q retryable=%t ambiguous=%t",
			err, code, retryable, ambiguous)
	}
}
