package validation

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

var validate = validator.New()

// IsRequestValid validates a request object
func IsRequestValid(obj any) (bool, string) {
	err := validate.Struct(obj)
	if err != nil {
		if errs, ok := err.(validator.ValidationErrors); ok {
			fe := errs[0]
			field := jsonFieldName(obj, fe.Field())
			if field == "" {
				field = strings.ToLower(fe.Field())
			}
			field = strings.Split(field, "[")[0]

			var message string
			switch fe.Tag() {
			case "required":
				message = fmt.Sprintf("%s is required", field)
			case "min":
				message = fmt.Sprintf("%s must be at least %s characters", field, fe.Param())
			case "max":
				message = fmt.Sprintf("%s must not exceed %s characters", field, fe.Param())
			case "email":
				message = fmt.Sprintf("%s must be a valid email", field)
			case "oneof":
				message = fmt.Sprintf("%s must be one of: %s", field, fe.Param())
			default:
				message = fmt.Sprintf("%s is invalid (%s)", field, fe.Tag())
			}

			return false, message
		}
		return false, err.Error()
	}
	return true, ""
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
