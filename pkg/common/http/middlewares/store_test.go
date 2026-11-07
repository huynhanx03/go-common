package middlewares

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestStoreSharesTypedEnrichmentAndAbort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	Set(ctx, "profile", map[string]string{"name": "Ada"})

	profile, ok := Get[map[string]string](ctx, "profile")
	if !ok || profile["name"] != "Ada" {
		t.Fatalf("profile enrichment = (%v, %v)", profile, ok)
	}
	if _, ok := Get[map[string]int](ctx, "profile"); ok {
		t.Fatal("typed lookup accepted an incompatible enrichment value")
	}
	if _, ok := Get[string](ctx, "missing"); ok {
		t.Fatal("lookup reported a missing enrichment value")
	}

	wantErr := errors.New("required profile source failed")
	Abort(ctx, wantErr)
	gotErr, ok := AbortError(ctx)
	if !ok || !errors.Is(gotErr, wantErr) {
		t.Fatalf("abort error = (%v, %v), want original error", gotErr, ok)
	}
}
