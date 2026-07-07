package validation

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

var validate = validator.New()

// FieldError describes one invalid field, ready to render to the client.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Validate checks a request object and returns every invalid field, so the
// client can show the whole form's problems from a single submit. A nil
// result means the object is valid.
func Validate(obj any) []FieldError {
	err := validate.Struct(obj)
	if err == nil {
		return nil
	}

	errs, ok := err.(validator.ValidationErrors)
	if !ok {
		return []FieldError{{Field: "", Message: err.Error()}}
	}

	fields := make([]FieldError, 0, len(errs))
	for _, fe := range errs {
		field := jsonFieldName(obj, fe.Field())
		if field == "" {
			field = strings.ToLower(fe.Field())
		}
		field = strings.Split(field, "[")[0]

		fields = append(fields, FieldError{Field: field, Message: fieldMessage(field, fe)})
	}
	return fields
}

// fieldMessage renders a human-readable message for one validation failure.
func fieldMessage(field string, fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", field)
	case "min":
		return fmt.Sprintf("%s must be at least %s characters", field, fe.Param())
	case "max":
		return fmt.Sprintf("%s must not exceed %s characters", field, fe.Param())
	case "email":
		return fmt.Sprintf("%s must be a valid email", field)
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", field, fe.Param())
	default:
		return fmt.Sprintf("%s is invalid (%s)", field, fe.Tag())
	}
}

// jsonFieldName returns the JSON field name for a given field
func jsonFieldName(obj any, field string) string {
	t := reflect.TypeOf(obj)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if f, ok := t.FieldByName(field); ok {
		tag := f.Tag.Get("json")
		if tag != "" && tag != "-" {
			return strings.Split(tag, ",")[0]
		}
	}
	return ""
}
