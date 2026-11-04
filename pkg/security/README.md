# Security checker

`Checker` shares authentication and authorization checks between HTTP, gRPC,
and application code. JWT verification and Casbin evaluation remain injected
engines. The core uses Go values and errors without transport dependencies.

```go
checker := security.NewChecker(security.Config{
    Verifier:   verifier,
    Authorizer: authorizer,
})

result, err := checker.Check(ctx, security.Request{
    Token:   rawToken,
    Purpose: authentication.PurposeAccess,
    Permissions: []security.Permission{{
        Scope:      authorization.Scope{Type: "tenant", ID: tenantID},
        Resource:   "problems",
        ResourceID: problemID,
        Action:     "update",
    }},
})
if err != nil {
    return err
}
principal := result.Principal
```

`Check` requires at least one permission. Multiple permissions mean any-of:
all must be well formed, then evaluation stops on the first allow or evaluator
error. A denied or failed combined check never exposes an authenticated principal.

Use `Authenticate` alone for authentication-only endpoints. Use `Authorize` with
a verified principal when authentication already ran, or when the application
must resolve object/session state between the two phases. A verifier can include
application-specific session checks. Principal claims do not replace live
session state.

The application resolves scopes and object identifiers. An empty scope is not a
wildcard; scoped authorizers reject it. Omit an engine only if its operation is
unused. `Config.Clock` sets the shared principal validation clock.

HTTP applications extract credentials, resolve permissions, and call the checker
directly. They map the returned errors to HTTP responses; the middleware package
does not provide authentication or authorization adapters.

gRPC unary/stream authentication and authorization interceptors receive the same
checker and map its errors to bounded transport responses.

For Envoy, an external authorization service can translate `CheckRequest` into
the same Go request and call `Check`. This package does not register an Envoy
service or trust principal headers from clients.
