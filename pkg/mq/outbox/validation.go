package outbox

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/huynhanx03/go-common/pkg/correlation"
)

const (
	maximumMessageIDBytes        = 256
	maximumDestinationBytes      = 512
	maximumClaimDestinations     = 128
	maximumAttributes            = 16
	maximumAttributeKeyBytes     = 64
	maximumAttributeValueBytes   = 1024
	maximumPublishReferenceBytes = 1024
)

func validBoundedText(value string, maximum int) bool {
	return len(value) > 0 && validOptionalText(value, maximum)
}

func messageDataWithinBudget(message Message, maximum int) bool {
	if maximum <= 0 || len(message.Key) > maximum {
		return false
	}
	return len(message.Payload) <= maximum-len(message.Key)
}

func normalizedPublishAck(ack PublishAck) PublishAck {
	if !validOptionalText(ack.Reference, maximumPublishReferenceBytes) {
		ack.Reference = ""
	} else {
		ack.Reference = strings.Clone(ack.Reference)
	}
	return ack
}

func validateLease(lease Lease) error {
	if !validBoundedText(lease.MessageID, maximumMessageIDBytes) ||
		!validBoundedText(lease.Token, maximumMessageIDBytes) || lease.Attempt == 0 {
		return ErrInvalidMessage
	}
	return nil
}

func validateClaimedMessage(lease Lease, message Message, maximumDataBytes int) error {
	if err := validateLease(lease); err != nil {
		return err
	}
	if lease.MessageID != message.ID {
		return fmt.Errorf("%w: lease and message identity differ", ErrStoreContract)
	}
	if !validBoundedText(message.ID, maximumMessageIDBytes) ||
		!validBoundedText(message.Destination, maximumDestinationBytes) ||
		!messageDataWithinBudget(message, maximumDataBytes) ||
		len(message.Metadata.Attributes) > maximumAttributes {
		return ErrInvalidMessage
	}
	if message.Metadata.CorrelationID != "" && correlation.Validate(message.Metadata.CorrelationID) != nil {
		return ErrInvalidMessage
	}
	for _, attribute := range message.Metadata.Attributes {
		if !validBoundedText(attribute.Key, maximumAttributeKeyBytes) ||
			!validOptionalText(attribute.Value, maximumAttributeValueBytes) {
			return ErrInvalidMessage
		}
	}
	return nil
}

func validOptionalText(value string, maximum int) bool {
	if len(value) > maximum || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
