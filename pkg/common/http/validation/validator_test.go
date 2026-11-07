package validation

import "testing"

type signupRequest struct {
	DisplayName string   `json:"display_name,omitempty" validate:"required,min=2,max=12"`
	Email       string   `json:"email" validate:"required,email"`
	Role        string   `json:"role" validate:"oneof=admin member"`
	Alias       string   `json:"-" validate:"gte=3"`
	Unlabelled  string   `validate:"required"`
	ShortCode   string   `json:"short_code" validate:"max=4"`
	Tags        []string `json:"tags" validate:"dive,required"`
}

func TestValidateReportsEveryInvalidJSONField(t *testing.T) {
	got := Validate(&signupRequest{
		DisplayName: "x",
		Email:       "not-an-email",
		Role:        "guest",
		Alias:       "a",
		ShortCode:   "12345",
		Tags:        []string{""},
	})
	if len(got) != 7 {
		t.Fatalf("Validate() returned %d errors, want 7: %+v", len(got), got)
	}

	fields := make(map[string]string, len(got))
	for _, field := range got {
		fields[field.Field] = field.Message
	}
	for _, field := range []string{"display_name", "email", "role", "alias", "unlabelled", "short_code", "tags"} {
		if message := fields[field]; message == "" {
			t.Errorf("field %q has no validation message; fields = %+v", field, fields)
		}
	}
}

func TestValidateAcceptsValidValueAndHandlesInvalidInput(t *testing.T) {
	valid := &signupRequest{
		DisplayName: "Ada",
		Email:       "ada@example.test",
		Role:        "member",
		Alias:       "ada",
		Unlabelled:  "present",
		ShortCode:   "1234",
		Tags:        []string{"go"},
	}
	if got := Validate(valid); got != nil {
		t.Fatalf("Validate(valid) = %+v, want nil", got)
	}

	for _, value := range []any{nil, 42} {
		got := Validate(value)
		if len(got) != 1 || got[0].Field != "" || got[0].Message == "" {
			t.Errorf("Validate(%T) = %+v, want one object-level validation error", value, got)
		}
	}
}
