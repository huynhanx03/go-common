package request

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
)

type parseRequestFixture struct {
	ID   string `uri:"id" json:"id" validate:"required"`
	Name string `form:"name" json:"name" validate:"required,min=2"`
}

func newParseContext(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Params = gin.Params{{Key: "id", Value: "from-uri"}}
	return context, recorder
}

func TestParseRequestBindsInDocumentedPrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)

	context, _ := newParseContext(
		http.MethodPost,
		"/users/from-uri?name=from-query",
		`{"name":"from-json"}`,
	)
	request, err := ParseRequest[parseRequestFixture](context)
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	if request.ID != "from-uri" || request.Name != "from-json" {
		t.Fatalf("request = %+v", request)
	}
}

func TestParseRequestMapsBindingAndValidationErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name      string
		body      string
		configure func(*gin.Context, *httptest.ResponseRecorder)
		wantCode  int
	}{
		{
			name:     "invalid JSON",
			body:     `{"name":`,
			wantCode: apperr.CodeParamInvalid,
		},
		{
			name:     "validation",
			body:     `{"name":"x"}`,
			wantCode: apperr.CodeValidationFailed,
		},
		{
			name: "body too large",
			body: `{"name":"long enough"}`,
			configure: func(context *gin.Context, recorder *httptest.ResponseRecorder) {
				context.Request.Body = http.MaxBytesReader(recorder, context.Request.Body, 2)
			},
			wantCode: apperr.CodeBodyTooLarge,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			context, recorder := newParseContext(http.MethodPost, "/users/from-uri", test.body)
			if test.configure != nil {
				test.configure(context, recorder)
			}
			_, err := ParseRequest[parseRequestFixture](context)
			var applicationError *apperr.AppError
			if !errors.As(err, &applicationError) || applicationError.Code != test.wantCode {
				t.Fatalf("error = %v, want app code %d", err, test.wantCode)
			}
			if test.wantCode == apperr.CodeValidationFailed && applicationError.Details == nil {
				t.Fatal("validation details are absent")
			}
		})
	}
}

func TestParseRequestAllowsEmptyBodyWhenFieldsComeFromOtherSources(t *testing.T) {
	gin.SetMode(gin.TestMode)

	context, _ := newParseContext(http.MethodGet, "/users/from-uri?name=query-name", "")
	context.Request.Body = io.NopCloser(strings.NewReader(""))
	request, err := ParseRequest[parseRequestFixture](context)
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	if request.ID != "from-uri" || request.Name != "query-name" {
		t.Fatalf("request = %+v", request)
	}
}
