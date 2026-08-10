package events

import (
	"encoding/json"
	"testing"

	"github.com/vishu42/tflive/internal/queue"
)

func TestNotifyUserSpecKeyNamesTheEvent(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(NotifyUserPayload{Subject: "user:xyz", Topic: "stack_abc", Event: "rev3"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	key, err := NotifyUserSpec.Key(payload)
	if err != nil {
		t.Fatalf("Key returned error: %v", err)
	}
	if key != "notif:stack_abc/user:xyz/rev3" {
		t.Fatalf("Key = %q, want notif:stack_abc/user:xyz/rev3", key)
	}
	if NotifyUserSpec.Mode != queue.ModeJob {
		t.Fatal("notify_user must be ModeJob: coalescing notifications loses messages")
	}
}

func TestNotifyUserSpecRejectsNonDistinguishingPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload json.RawMessage
	}{
		{name: "not json", payload: json.RawMessage(`{`)},
		{name: "no subject", payload: json.RawMessage(`{"topic":"stack_abc","event":"rev3"}`)},
		{name: "no topic", payload: json.RawMessage(`{"subject":"user:xyz","event":"rev3"}`)},
		{name: "no event", payload: json.RawMessage(`{"subject":"user:xyz","topic":"stack_abc"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// Without all three the key cannot separate one delivery from the
			// next, and every replay would send a duplicate message.
			if _, err := NotifyUserSpec.Key(test.payload); err == nil {
				t.Fatal("Key accepted a payload it cannot distinguish")
			}
		})
	}
}
