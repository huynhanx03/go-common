package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
)

func run(f func(c *gin.Context)) map[string]any {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	f(c)

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body
}

func TestErrorResponseEmptyMessageFallsBackToDefault(t *testing.T) {
	body := run(func(c *gin.Context) {
		ErrorResponse(c, apperr.CodeInternalServer, apperr.New(apperr.CodeConflict, "", nil))
	})

	if body["message"] != Msg[apperr.CodeConflict] {
		t.Errorf("message = %v, want default %q", body["message"], Msg[apperr.CodeConflict])
	}
	if body["code"].(float64) != apperr.CodeConflict {
		t.Errorf("code = %v, want %d", body["code"], apperr.CodeConflict)
	}
}

func TestErrorResponsePlainErrorUsesFallbackCode(t *testing.T) {
	body := run(func(c *gin.Context) {
		ErrorResponse(c, apperr.CodeParamInvalid, errors.New("json: cannot unmarshal"))
	})

	if body["code"].(float64) != apperr.CodeParamInvalid {
		t.Errorf("code = %v, want fallback %d", body["code"], apperr.CodeParamInvalid)
	}
	if body["message"] != Msg[apperr.CodeParamInvalid] {
		t.Errorf("message = %v — raw error text must never render", body["message"])
	}
}

func TestErrorResponseRendersDetails(t *testing.T) {
	body := run(func(c *gin.Context) {
		err := apperr.New(apperr.CodeValidationFailed, "name is required", nil).
			WithDetails(map[string]any{"errors": []string{"name is required"}})
		ErrorResponse(c, apperr.CodeParamInvalid, err)
	})

	data, ok := body["data"].(map[string]any)
	if !ok || data["errors"] == nil {
		t.Errorf("details not rendered, data = %v", body["data"])
	}
}
