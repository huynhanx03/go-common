package authentication

import (
	"errors"
	"testing"
)

func TestTokenPurposeValidate(t *testing.T) {
	t.Parallel()

	for _, purpose := range []TokenPurpose{
		PurposeAccess,
		PurposeRefresh,
	} {
		if err := purpose.Validate(); err != nil {
			t.Errorf("%q.Validate() error = %v", purpose, err)
		}
	}
	if err := TokenPurpose("admin").Validate(); !errors.Is(
		err,
		ErrInvalidPurpose,
	) {
		t.Fatalf("unknown purpose error = %v", err)
	}
}
