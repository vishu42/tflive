package events

import (
	"encoding/json"
	"fmt"

	"github.com/vishu42/tflive/internal/queue"
)

// KindNotifyUser reserves the work kind notifications will use. There is no
// handler yet: this package has no delivery mechanism — no email, no
// websocket, no webhook — so nothing enqueues this kind and the controller
// would reschedule it forever if anything did. What is fixed here is the shape
// a notification must have, so the first delivery mechanism cannot pick a
// conflicting one.
const KindNotifyUser queue.Kind = "notify_user"

// NotifyUserPayload is one notification to one subject. Notifications are
// enqueued as a handler's follow-up work, returned only after its external
// call succeeded — never before, or a delivery that later fails would still
// have announced itself.
type NotifyUserPayload struct {
	Subject string `json:"subject"`
	Topic   string `json:"topic"`
	// Event distinguishes one notification from the next for the same subject
	// and topic. It MUST be derived from the item that produced it — the
	// parent's resource and revision, for instance — because the safety of
	// enqueueing outside the completing statement rests entirely on the key
	// being deterministic. A random or time-based value sends a duplicate
	// message on every replay.
	Event string `json:"event"`
}

// NotifyUserSpec declares the notify_user kind.
//
// ModeJob: a notification is the work itself, so collapsing two of them loses
// one. The key names the event rather than the resource for the same reason.
//
// Key derivation is a frozen contract; see queue.Spec.
var NotifyUserSpec = queue.Spec{
	Kind: KindNotifyUser,
	Mode: queue.ModeJob,
	Key:  notifyUserKey,
}

func notifyUserKey(payload json.RawMessage) (string, error) {
	var parsed NotifyUserPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("decode notify user payload: %w", err)
	}
	if parsed.Subject == "" || parsed.Topic == "" || parsed.Event == "" {
		return "", fmt.Errorf("notify user payload needs a subject, topic and event")
	}
	return "notif:" + parsed.Topic + "/" + parsed.Subject + "/" + parsed.Event, nil
}
