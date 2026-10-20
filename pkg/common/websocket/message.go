package websocket

const maxCoalesceKeyBytes = 256

type DeliveryClass uint8

const (
	DeliveryLatestState DeliveryClass = iota + 1
	DeliveryEphemeral
	DeliveryCritical
)

type Message struct {
	Envelope    Envelope
	Class       DeliveryClass
	CoalesceKey string
}

func (message Message) Validate() error {
	if err := message.Envelope.Validate(); err != nil {
		return err
	}
	switch message.Class {
	case DeliveryLatestState:
		if !validRequiredTerm(message.CoalesceKey, maxCoalesceKeyBytes) {
			return newProtocolError(
				ErrInvalidMessage,
				CodeInvalidMessage,
				false,
			)
		}
	case DeliveryEphemeral, DeliveryCritical:
		if message.CoalesceKey != "" {
			return newProtocolError(
				ErrInvalidMessage,
				CodeInvalidMessage,
				false,
			)
		}
	default:
		return newProtocolError(
			ErrInvalidMessage,
			CodeInvalidMessage,
			false,
		)
	}
	return nil
}

func EncodeMessage(message Message, maximum int64) ([]byte, error) {
	if err := message.Validate(); err != nil {
		return nil, err
	}
	return EncodeEnvelope(message.Envelope, maximum)
}
