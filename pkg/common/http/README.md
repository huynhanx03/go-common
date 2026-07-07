# Common HTTP

A Gin-based HTTP toolkit providing middlewares, request/response helpers, validation, and concurrency utilities for building REST APIs.

## Package Structure

```
pkg/common/http/
├── middlewares/          # Gin middleware chain
│   ├── auth.go           # JWT authentication (RSA)
│   ├── permission.go     # RBAC permission & role level checks
│   ├── rate_limit.go     # Per-key token bucket rate limiting
│   ├── circuit_breaker.go# Circuit breaker (503 on open)
│   ├── cors.go           # CORS headers
│   ├── recovery.go       # Panic recovery → 500
│   ├── body_limit.go     # Request body size cap → 413
│   └── compose.go        # Middleware composition helpers
├── handler/
│   ├── wrapper.go        # Wrap / WrapNoReq (parse → execute → respond)
│   └── base.go           # Base handler struct
├── request/
│   ├── parse.go          # Generic request parsing + validation
│   ├── retry.go          # Generic retry with backoff
│   └── fanout.go         # Concurrent fan-out execution
├── response/
│   ├── wrapper.go        # Standardized JSON responses (+ cid on errors)
│   ├── reply.go          # Created / Updated / Deleted / Paginated overrides
│   ├── codes.go          # Code ranges → HTTP status mapping
│   ├── messages.go       # Default messages per code
│   └── merge.go          # Map/struct merge strategies
├── validation/
│   └── validator.go      # Struct validation with human-readable errors
└── http_pool.go          # HTTP client pool with retry + cache
```

## Middlewares

### Authentication

JWT validation with RSA signing. Extracts `UserID` and `Username` into request context.

```go
r.Use(middlewares.Authentication(rsaPublicKey))

// Access in handler:
userID := ctx.Value(constraints.ContextKeyUserID).(int)
```

### Permission Checker

RBAC with nested-set role hierarchy. Cached via Ember local cache.

```go
pc := middlewares.NewPermissionChecker(userRepo, roleRepo, permRepo, localCache)

// Require specific permission scope on a resource
r.POST("/users", pc.RequirePermission("users", permissions.ScopeWrite))

// Require minimum role level (lower = more privileged)
r.DELETE("/admin", pc.RequireRole(0)) // admin only
```

### Rate Limiting

Per-key token bucket with configurable limit, burst, and window. Emits standard `X-RateLimit-*` headers.

```go
r.Use(middlewares.RateLimit(middlewares.RateLimitConfig{
    Limit:   100,              // requests per window
    Burst:   100,              // token bucket capacity
    Window:  time.Minute,      // sliding window
    KeyFunc: func(c *gin.Context) string { return c.ClientIP() },
    Skip:    func(c *gin.Context) bool { return c.Request.URL.Path == "/health" },
}))
```

**Response headers:**
| Header | Description |
|--------|-------------|
| `X-RateLimit-Limit` | Max requests per window |
| `X-RateLimit-Remaining` | Tokens remaining |
| `X-RateLimit-Reset` | Window reset timestamp |
| `Retry-After` | Seconds until retry (on 429) |

### Circuit Breaker

Wraps routes with a circuit breaker. Returns `503 Service Unavailable` when circuit is open. Records success/failure based on HTTP status (>= 500 = failure).

```go
cb := algorithm.NewCircuitBreaker(/* config */)
r.Use(middlewares.CircuitBreakerMiddleware(cb))
```

### CORS

Sets permissive CORS headers. Returns `204` for `OPTIONS`/`HEAD` preflight.

```go
r.Use(middlewares.CORSMiddleware)
```

### Recovery

Catches panics, logs stack trace, returns `500` with standardized error response.

```go
r.Use(middlewares.RecoveryMiddleware)
```

### Body Limit

Caps request body size so one oversized request cannot exhaust memory. When
exceeded, the client gets `413` with code `40005`.

```go
r.Use(middlewares.BodyLimit(0)) // 0 → DefaultBodyLimit (1 MiB)
```

### Compose

Chain multiple middlewares into a single handler or compose them for route registration.

```go
// Wrap a final handler with middlewares
r.GET("/admin", middlewares.Compose(
    middlewares.Authentication(key),
    middlewares.RateLimit(cfg),
    middlewares.CircuitBreakerMiddleware(cb),
)(adminHandler))

// Or pass as handler slice
r.GET("/path", middlewares.ComposeHandlers(
    middlewares.RateLimit(cfg),
    middlewares.Authentication(key),
    myHandler,
)...)
```

## Handler Wrapper

Generic handlers that auto-parse, validate, execute, and render — routes stay
one line, handlers contain only business logic.

```go
r.GET ("/stats",     handler.WrapNoReq(h.Stats))   // no DTO at all
r.GET ("/users/:id", handler.Wrap(h.GetUser))      // :id lives in the DTO
r.POST("/users",     handler.Wrap(h.CreateUser))   // 201 via response.Created
r.GET ("/users",     handler.Wrap(h.ListUsers))    // pagination via response.Paginated
```

One struct describes the endpoint's full input — URI, query, and body bind
into it (in that order; use `validate` tags, not `binding`, since URI/query
bind errors are non-fatal):

```go
type UpdateUserReq struct {
    ID   string `uri:"id" validate:"required"`
    Name string `json:"name" validate:"required,min=2"`
}
```

Plain results render as `200`/`20000`. Return `*response.Reply` to override:

```go
func (h *H) Stats(ctx context.Context) (*StatsRes, error)                      // bare GET: zero DTO

func (h *H) CreateUser(ctx context.Context, req *CreateUserReq) (*response.Reply, error) {
    u, err := h.svc.Create(ctx, req)
    if err != nil {
        return nil, err                  // AppError anywhere in the chain → its code + status
    }
    return response.Created(u), nil      // HTTP 201, code 20001
}

func (h *H) ListUsers(ctx context.Context, req *ListUsersReq) (*response.Reply, error) {
    items, meta, err := h.svc.List(ctx, req) // meta from dto.CalculatePagination
    if err != nil {
        return nil, err
    }
    return response.Paginated(items, meta), nil
}
```

The full CRUD story — the common case costs nothing, the rest is one word:

```go
return response.Created(u), nil              // POST        → 201, 20001
return user, nil                             // GET         → 200, 20000
return response.Paginated(items, meta), nil  // GET list    → 200 + pagination
return response.Updated(u), nil              // PUT/PATCH   → 200, 20002
return response.Deleted(nil), nil            // DELETE      → 200, 20003
```

Every error is attached to the Gin context (`c.Error`), so the request logger
records the full cause chain under the request's cid — the client only ever
sees `code + message + cid`.

## Error Handling

**Map an error once, where it is born; every layer above passes it through.**

- Ent repos: `ent.MapEntError(err, "user")`
- Business rules: `apperr.New(CodeEmailTaken, "email already exists", nil)`
- Other sources (redis, external APIs): the entity factory below
- Service layers in between: `return err`, or `fmt.Errorf("context: %w", err)` —
  `errors.As` in the response layer still finds the AppError

```go
var errUser = apperr.For("user") // once per service file

return nil, errUser.NotFound(err)      // 44000, "user not found"
return nil, errUser.CreateFailed(err)  // 50001, "failed to create user"
```

Anything without an AppError in its chain renders as a generic `500` — internal
error text never reaches the client.

## Request Utilities

### ParseRequest

Generic JSON + URI binding with struct validation.

```go
req, err := request.ParseRequest[CreateUserRequest](c)
```

### Retry

Generic retry with configurable backoff and retry predicate.

```go
result := request.Retry[*http.Response](ctx, request.RetryConfig{
    MaxRetries:  3,
    Backoff:     algorithm.NewJitterBackoff(algorithm.NewExponentialBackoff(1*time.Second, 30*time.Second, 2.0)),
    ShouldRetry: func(err error) bool { return err != nil },
}, func(ctx context.Context) (*http.Response, error) {
    return httpClient.Do(req)
})
```

### Fanout

Concurrent execution of multiple functions with ordered results.

```go
results := request.Fanout[UserData](ctx,
    func(ctx context.Context) (UserData, error) { return fetchProfile(ctx) },
    func(ctx context.Context) (UserData, error) { return fetchSettings(ctx) },
)

// Or get first successful result
val, err := request.FanoutFirst[UserData](ctx, fn1, fn2, fn3)
```

## Response

### Standardized Format

```json
{
    "code": 20000,
    "message": "Success",
    "data": { ... }
}
```

With pagination (from `response.Paginated` + `dto.CalculatePagination`):
```json
{
    "code": 20000,
    "message": "Success",
    "data": [ ... ],
    "pagination": { "current_page": 1, "page_size": 20, "total_pages": 5, "total_items": 100, "has_next": true, "has_prev": false }
}
```

Errors carry the request's correlation ID, so a client-side error report can
be matched to server logs directly (`grep` the cid):

```json
{
    "code": 44100,
    "message": "user not found",
    "data": null,
    "cid": "0197f6f2-6a3e-7cc0-..."
}
```

### Business Codes

Codes live in `apperr` (the contract package); `response` only maps them to
HTTP statuses. The code is what clients branch on — the message is a human
hint, never parsed.

| Range | Category | Example |
|-------|----------|---------|
| 20000–29999 | Success | `20000` Success, `20001` Created |
| 40000–40999 | Client error | `40000` Invalid params, `40001` Validation failed, `40005` Body too large |
| 41000–41999 | Auth error | `41000` Unauthorized, `41002` Token expired |
| 42900–42999 | Rate limit | `42900` Too many requests |
| 43000–43999 | Forbidden | `43000` Forbidden |
| 44000–44999 | Not found | `44000` Not found |
| 49000–49999 | Conflict | `49000` Conflict |
| 50000–59999 | Server error | `50000` Internal error, `50001` DB error |

**Convention for app-specific codes** — `[2 digits HTTP class][1 digit module][2 digits sequence]`.
go-common owns the generic block `xx0xx`; each app allocates module digits
(e.g. 1=user, 2=order) and its codes inherit the right HTTP status from the
range with zero registration:

```go
const (
    CodeUserNotFound = 44100 // 44xxx → 404
    CodeEmailTaken   = 49101 // 49xxx → 409
)
return nil, apperr.New(CodeEmailTaken, "email already exists", nil)
```

### Merge

Combine maps from parallel data sources with configurable strategy.

```go
merged := response.MergeMaps(response.MergeLast, map1, map2, map3)
// Strategies: MergeFirst (keep first), MergeLast (overwrite), MergeAppend (collect into slice)
```

## Validation

Struct validation using `go-playground/validator`. Every invalid field is
reported in one pass, so the client can render the whole form's problems from
a single submit — `422` with the first error as message and the full list in
`data.errors`:

```json
{
    "code": 40001,
    "message": "email must be a valid email",
    "data": {
        "errors": [
            {"field": "email", "message": "email must be a valid email"},
            {"field": "username", "message": "username is required"}
        ]
    }
}
```

```go
errs := validation.Validate(req) // nil when valid, one FieldError per problem
```

## Performance Notes

- **JSON encoder**: gin ≥ v1.9 swaps its JSON implementation via build tag —
  `go build -tags=sonic` is a ~2-3× serialize/deserialize win with zero code
  changes.
- `WrapNoReq` skips binding and validation entirely — bare GETs (health,
  stats) cost nothing beyond the handler itself.
- Generic wrappers mean no reflection on the hot path; the only reflection
  (JSON field-name lookup) runs when validation already failed.

## HTTP Client Pool

Pooled HTTP client with retry, exponential backoff + jitter, and simple in-memory cache.

```go
pool := http.NewHTTPClientPool(&http.HTTPClientConfig{
    Timeout:         30 * time.Second,
    MaxIdleConns:    100,
    MaxConnsPerHost: 10,
})

resp, err := pool.RequestWithRetry(ctx, req, 3)
```
