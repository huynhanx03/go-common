package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/huynhanx03/go-common/pkg/cid"
	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/common/http/middlewares"
	"github.com/huynhanx03/go-common/pkg/common/http/response"
	"github.com/huynhanx03/go-common/pkg/dto"
)

type createUserReq struct {
	ID    string `uri:"id" validate:"required"`
	Name  string `json:"name" validate:"required,min=2"`
	Email string `json:"email" validate:"required,email"`
}

type userRes struct {
	Name string `json:"name"`
}

func do(r *gin.Engine, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	r.ServeHTTP(rec, req)

	var parsed map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return rec, parsed
}

func newRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func TestWrapBindsURIQueryAndBody(t *testing.T) {
	r := newRouter()
	r.POST("/users/:id", Wrap(func(ctx context.Context, req *createUserReq) (*userRes, error) {
		if req.ID != "u1" {
			t.Errorf("uri param not bound, ID = %q", req.ID)
		}
		return &userRes{Name: req.Name}, nil
	}))

	rec, body := do(r, http.MethodPost, "/users/u1", `{"name":"Jerry","email":"j@x.io"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if body["code"].(float64) != apperr.CodeSuccess {
		t.Errorf("code = %v, want %d", body["code"], apperr.CodeSuccess)
	}
	if body["data"].(map[string]any)["name"] != "Jerry" {
		t.Errorf("data = %v", body["data"])
	}
}

func TestWrapValidationReturnsAllFields(t *testing.T) {
	r := newRouter()
	r.POST("/users/:id", Wrap(func(ctx context.Context, req *createUserReq) (*userRes, error) {
		t.Fatal("handler must not run on invalid input")
		return nil, nil
	}))

	rec, body := do(r, http.MethodPost, "/users/u1", `{"name":"J","email":"not-an-email"}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	if body["code"].(float64) != apperr.CodeValidationFailed {
		t.Errorf("code = %v, want %d", body["code"], apperr.CodeValidationFailed)
	}
	fieldErrs := body["data"].(map[string]any)["errors"].([]any)
	if len(fieldErrs) != 2 {
		t.Fatalf("errors = %v, want both name and email reported", fieldErrs)
	}
}

func TestWrapAppErrorKeepsCodeThroughWrapping(t *testing.T) {
	r := newRouter()
	r.GET("/users/:id", Wrap(func(ctx context.Context, req *struct {
		ID string `uri:"id" validate:"required"`
	}) (*userRes, error) {
		appErr := apperr.For("user").NotFound(errors.New("sql: no rows"))
		return nil, fmt.Errorf("get user flow: %w", appErr) // service adds context
	}))

	rec, body := do(r, http.MethodGet, "/users/u1", "")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
	if body["message"] != "user not found" {
		t.Errorf("message = %v", body["message"])
	}
}

func TestWrapPlainErrorHidesInternals(t *testing.T) {
	r := newRouter()

	// Capture what the request logger would see.
	var logged []string
	r.Use(func(c *gin.Context) {
		c.Next()
		logged = c.Errors.Errors()
	})
	r.GET("/boom", WrapNoReq(func(ctx context.Context) (*userRes, error) {
		return nil, errors.New("pq: password authentication failed")
	}))

	rec, body := do(r, http.MethodGet, "/boom", "")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "pq:") {
		t.Errorf("internal error leaked to client: %s", rec.Body)
	}
	if body["message"] != "Internal server error" {
		t.Errorf("message = %v", body["message"])
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "pq:") {
		t.Errorf("cause must reach the request logger via c.Errors, got %v", logged)
	}
}

func TestWrapNoReqSkipsBinding(t *testing.T) {
	r := newRouter()
	r.GET("/stats", WrapNoReq(func(ctx context.Context) (map[string]int, error) {
		return map[string]int{"users": 7}, nil
	}))

	rec, body := do(r, http.MethodGet, "/stats", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if body["data"].(map[string]any)["users"].(float64) != 7 {
		t.Errorf("data = %v", body["data"])
	}
}

func TestWrapCreatedReply(t *testing.T) {
	r := newRouter()
	r.POST("/users/:id", Wrap(func(ctx context.Context, req *createUserReq) (*response.Reply, error) {
		return response.Created(&userRes{Name: req.Name}), nil
	}))

	rec, body := do(r, http.MethodPost, "/users/u1", `{"name":"Jerry","email":"j@x.io"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	if body["code"].(float64) != apperr.CodeCreated {
		t.Errorf("code = %v, want %d", body["code"], apperr.CodeCreated)
	}
}

func TestWrapUpdatedAndDeletedReplies(t *testing.T) {
	r := newRouter()
	r.PUT("/users/:id", Wrap(func(ctx context.Context, req *createUserReq) (*response.Reply, error) {
		return response.Updated(&userRes{Name: req.Name}), nil
	}))
	r.DELETE("/users/:id", Wrap(func(ctx context.Context, req *struct {
		ID string `uri:"id" validate:"required"`
	}) (*response.Reply, error) {
		return response.Deleted(nil), nil
	}))

	rec, body := do(r, http.MethodPut, "/users/u1", `{"name":"Jerry","email":"j@x.io"}`)
	if rec.Code != http.StatusOK || body["code"].(float64) != apperr.CodeUpdated {
		t.Errorf("PUT: status/code = %d/%v, want 200/%d", rec.Code, body["code"], apperr.CodeUpdated)
	}

	rec, body = do(r, http.MethodDelete, "/users/u1", "")
	if rec.Code != http.StatusOK || body["code"].(float64) != apperr.CodeDeleted {
		t.Errorf("DELETE: status/code = %d/%v, want 200/%d", rec.Code, body["code"], apperr.CodeDeleted)
	}
	if body["message"] != "Resource deleted successfully" {
		t.Errorf("DELETE message = %v", body["message"])
	}
}

func TestWrapPaginatedReply(t *testing.T) {
	r := newRouter()
	r.GET("/users", WrapNoReq(func(ctx context.Context) (*response.Reply, error) {
		return response.Paginated([]userRes{{Name: "a"}}, dto.CalculatePagination(1, 10, 25)), nil
	}))

	rec, body := do(r, http.MethodGet, "/users", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	p := body["pagination"].(map[string]any)
	if p["total_items"].(float64) != 25 || p["has_next"] != true {
		t.Errorf("pagination = %v", p)
	}
}

func TestBodyLimitReturns413(t *testing.T) {
	r := newRouter()
	r.Use(middlewares.BodyLimit(64))
	r.POST("/users/:id", Wrap(func(ctx context.Context, req *createUserReq) (*userRes, error) {
		return &userRes{}, nil
	}))

	huge := fmt.Sprintf(`{"name":"Jerry","email":"j@x.io","pad":%q}`, strings.Repeat("x", 4096))
	rec, body := do(r, http.MethodPost, "/users/u1", huge)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body)
	}
	if body["code"].(float64) != apperr.CodeBodyTooLarge {
		t.Errorf("code = %v, want %d", body["code"], apperr.CodeBodyTooLarge)
	}
}

func TestErrorResponseCarriesCID(t *testing.T) {
	r := newRouter()
	r.Use(func(c *gin.Context) { // stand-in for RequestLogger's cid injection
		c.Request = c.Request.WithContext(cid.WithContext(c.Request.Context(), "cid-42"))
		c.Next()
	})
	r.GET("/boom", WrapNoReq(func(ctx context.Context) (*userRes, error) {
		return nil, errors.New("boom")
	}))

	_, body := do(r, http.MethodGet, "/boom", "")

	if body["cid"] != "cid-42" {
		t.Errorf("cid = %v, want cid-42", body["cid"])
	}
}

func TestSuccessResponseHasNoCID(t *testing.T) {
	r := newRouter()
	r.GET("/ok", WrapNoReq(func(ctx context.Context) (string, error) { return "fine", nil }))

	_, body := do(r, http.MethodGet, "/ok", "")

	if _, ok := body["cid"]; ok {
		t.Error("success responses must not carry cid")
	}
}
