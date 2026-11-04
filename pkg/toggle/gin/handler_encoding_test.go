package gin

import (
	"net/http/httptest"
	"testing"

	ginframework "github.com/gin-gonic/gin"
	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
)

func TestWriteJSONFallsBackWhenResponseCannotBeEncoded(t *testing.T) {
	ginframework.SetMode(ginframework.TestMode)
	response := httptest.NewRecorder()
	ctx, _ := ginframework.CreateTestContext(response)
	writeJSON(ctx, 201, make(chan int))
	if response.Code != 500 {
		t.Fatalf("status = %d, want internal server error", response.Code)
	}
	var body map[string]string
	if err := jsoncodec.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("fallback response is not a safe error object: %q (%v)", response.Body.String(), err)
	}
}
