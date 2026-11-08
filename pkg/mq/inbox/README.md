# Inbox

Inbox middleware prevents a consumer from applying the same database changes
twice when its broker redelivers a message. The consumer uses a producer's
`message-id` header when present, otherwise it uses the broker record identity.
Use the same producer-assigned ID when republishing a logical message; broker
coordinates only deduplicate redelivery of the same stored record. The existing
Forge outbox header is also recognized for compatibility.

Kafka producers can attach that identity with `PublishWithID`; JSON messages can
use `kafka.PublishJSONWithID`.

Each service implements `inbox.Store` with its own Ent client. `Store.Begin`
must insert the unique `(consumer, message_id)` row before processing and return
a non-nil Ent transaction. If a uniqueness conflict means the message was
already committed, roll back the failed transaction and return
`duplicate=true` without a transaction. If `Begin` returns an error, it must
clean up any transaction it opened.
The inbox row and handler's database writes commit together only for
`consumer.Success`; retries and dead-letter outcomes roll back. A missing
message ID returns `consumer.Retry` with `inbox.ErrMissingMessageID`.

```go
import (
	"entgo.io/ent"
	inbox "github.com/huynhanx03/go-common/pkg/mq/inbox"
	inboxent "github.com/huynhanx03/go-common/pkg/mq/inbox/ent"
	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

type Inbox struct{ ent.Schema }

func (Inbox) Mixin() []ent.Mixin {
	return []ent.Mixin{inboxent.Mixin{}}
}

processor, err := inbox.New(store, "email-notifications")
if err != nil {
	return err
}
handler := consumer.Chain(handle, processor.Middleware())
```

Keep inbox rows for at least the broker's redelivery and replay window. Inbox
protects transactional database effects; write external side effects to an
outbox in the same transaction.
