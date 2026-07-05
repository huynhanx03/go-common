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
│   └── compose.go        # Middleware composition helpers
├── handler/
│   ├── wrapper.go        # Generic handler wrapper (parse → execute → respond)
│   └── base.go           # Base handler struct
├── request/
│   ├── parse.go          # Generic request parsing + validation
│   ├── retry.go          # Generic retry with backoff
│   └── fanout.go         # Concurrent fan-out execution
├── response/
│   ├── wrapper.go        # Standardized JSON responses
│   ├── codes.go          # Business error codes → HTTP status mapping
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

Generic handler that auto-parses requests, validates, executes business logic, and returns standardized responses.

```go
// Define a typed handler
func CreateUser(ctx context.Context, req *CreateUserRequest) (*UserResponse, error) {
    // business logic
}

// Register with Gin
r.POST("/users", handler.Wrap(CreateUser))
```

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

With pagination:
```json
{
    "code": 20000,
    "message": "Success",
    "data": [ ... ],
    "pagination": { "page": 1, "page_size": 20, "total": 100, "total_pages": 5 }
}
```

### Business Codes

| Range | Category | Example |
|-------|----------|---------|
| 20000–29999 | Success | `20000` Success, `20001` Created |
| 40000–40999 | Client error | `40000` Invalid params, `40001` Validation failed |
| 41000–41999 | Auth error | `41000` Unauthorized, `41002` Token expired |
| 42900–42999 | Rate limit | `42900` Too many requests |
| 43000–43999 | Forbidden | `43000` Forbidden |
| 44000–44999 | Not found | `44000` Not found |
| 49000–49999 | Conflict | `49000` Conflict |
| 50000–59999 | Server error | `50000` Internal error, `50001` DB error |

### Merge

Combine maps from parallel data sources with configurable strategy.

```go
merged := response.MergeMaps(response.MergeLast, map1, map2, map3)
// Strategies: MergeFirst (keep first), MergeLast (overwrite), MergeAppend (collect into slice)
```

## Validation

Struct validation using `go-playground/validator` with human-readable error messages.

```go
type CreateUserRequest struct {
    Email    string `json:"email" validate:"required,email"`
    Username string `json:"username" validate:"required,min=3,max=50"`
}

ok, msg := validation.IsRequestValid(req) // false, "email is required"
```

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
