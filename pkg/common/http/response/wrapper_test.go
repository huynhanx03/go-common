package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/dto"
	"github.com/huynhanx03/go-common/pkg/logger"
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

func TestRespondStatusPreservesSuccessEnvelopeWithExplicitHTTPStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	RespondStatus(ctx, http.StatusAccepted, map[string]string{"status": "accepted"})

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	var body Body
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != apperr.CodeSuccess {
		t.Fatalf("code = %d, want %d", body.Code, apperr.CodeSuccess)
	}
}

func TestRespondRendersRepliesAndPlainValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meta := &dto.PaginationMeta{CurrentPage: 2, PageSize: 10, TotalPages: 3, TotalItems: 25, HasNext: true, HasPrev: true}
	for _, test := range []struct {
		name       string
		reply      any
		status     int
		code       int
		pagination bool
	}{
		{name: "created", reply: Created(map[string]string{"id": "u1"}), status: http.StatusCreated, code: apperr.CodeCreated},
		{name: "updated", reply: Updated("updated"), status: http.StatusOK, code: apperr.CodeUpdated},
		{name: "deleted", reply: Deleted(nil), status: http.StatusOK, code: apperr.CodeDeleted},
		{name: "paginated", reply: Paginated([]string{"a"}, meta), status: http.StatusOK, code: apperr.CodeSuccess, pagination: true},
		{name: "custom code", reply: With(20042, "accepted"), status: http.StatusOK, code: 20042},
		{name: "plain value", reply: map[string]string{"ok": "yes"}, status: http.StatusOK, code: apperr.CodeSuccess},
		{name: "nil reply", reply: (*Reply)(nil), status: http.StatusOK, code: apperr.CodeSuccess},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			Respond(ctx, test.reply)

			if recorder.Code != test.status {
				t.Fatalf("HTTP status = %d, want %d", recorder.Code, test.status)
			}
			var body Body
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != test.code {
				t.Errorf("business code = %d, want %d", body.Code, test.code)
			}
			if (body.Pagination != nil) != test.pagination {
				t.Errorf("pagination present = %v, want %v", body.Pagination != nil, test.pagination)
			}
		})
	}
}

func TestRespondStatusRejectsNonSuccessStatusAndPreservesReplyCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{http.StatusContinue, http.StatusMultipleChoices, http.StatusBadRequest} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		RespondStatus(ctx, status, "ignored")
		if recorder.Code != http.StatusInternalServerError {
			t.Errorf("invalid status %d rendered as %d", status, recorder.Code)
		}
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	RespondStatus(ctx, http.StatusAccepted, Created("queued"))
	var body Body
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusAccepted || body.Code != apperr.CodeCreated || body.Data != "queued" {
		t.Fatalf("status/code/data = %d/%d/%v", recorder.Code, body.Code, body.Data)
	}
}

func TestErrorResponseUsesWrappedApplicationErrorAndRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx.Request = request.WithContext(logger.WithRequestID(request.Context(), "req-42"))
	appError := apperr.New(apperr.CodeNotFound, "resource unavailable", nil).
		WithDetails(map[string]any{"resource": "user"})
	ErrorResponse(ctx, apperr.CodeInternalServer, fmt.Errorf("lookup: %w", appError))

	var body Body
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusNotFound || body.Code != apperr.CodeNotFound {
		t.Fatalf("status/code = %d/%d, want wrapped application error", recorder.Code, body.Code)
	}
	if body.RequestID != "req-42" {
		t.Errorf("request ID = %q, want propagated request ID", body.RequestID)
	}
	if details, ok := body.Data.(map[string]any); !ok || details["resource"] != "user" {
		t.Errorf("details = %#v, want client-safe application details", body.Data)
	}
}
