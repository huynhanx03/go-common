# gRPC server

`pkg/common/grpc/server` owns one gRPC listener as a `lifecycle.Component`.
Register application services before the listener starts:

```go
server, err := grpcserver.New(grpcserver.Config{
    Name:         "authorization-grpc",
    Address:      "0.0.0.0:9000",
    TLS:          tlsConfig,
    Logger:       logger,
    StatsHandler: otelgrpc.NewServerHandler(),
}, func(grpcServer *grpc.Server) {
    authpb.RegisterAuthorizationServer(grpcServer, checker)
})
if err != nil { return err }
if err := runner.Add(server, lifecycle.ComponentOptions{Required: true}); err != nil {
    return err
}
```

`New` automatically registers the standard gRPC health service. It reports
`NOT_SERVING` before `Run`, `SERVING` after bind, and shuts down health watchers
before draining. A service can update readiness with `server.Health()`.

The default transport policy caps receive/send messages at 4 MiB, metadata at
16 KiB and concurrent streams at 1024 per connection. Limits can be adjusted
through `Config` up to explicit maxima. Recovery interceptors are always
installed. Structured RPC logging is enabled when a logger is provided;
additional unary/stream interceptors and one stats handler are injectable.
Keepalive pings are left at grpc-go defaults unless the service explicitly
configures them; clients cannot send pings more often than once per minute.

Use `LoadServerMTLS(cert, key, clientCA)` for a private network listener. It
requires a complete certificate set, TLS 1.3, and verified client certificates.
Passing `TLS: nil` is an explicit plaintext choice suitable only for a
trusted local listener. The files are loaded at construction time; rotate
credentials by restarting the listener after updating the mounted files.

`Run(ctx)` returns on cancellation or fatal listener error. `Shutdown(ctx)`
first drains in-flight RPCs, then force-stops at the caller's deadline or the
configured drain timeout (10 seconds by default). It is safe to call more
than once. The process lifecycle, not this package, owns OS signals.
