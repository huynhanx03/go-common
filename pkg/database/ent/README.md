# Generic Ent Repository

This library provides a **Generic BaseRepository** for the Ent Framework, designed to minimize boilerplate code in Go projects while maintaining high performance.

## Key Features

- **Zero Allocation Reflection**: Optimized for performance using metadata caching.
- **O(1) Metadata Lookup**: Field and method lookups are pre-computed at startup.
- **Strict Type Safety**: Enforces `Model[ID]` constraints to ensure type correctness at compile time.
- **Clean Architecture Ready**: Designed to fit perfectly into the Infrastructure/Adapter layer.

## Recommended Structure

For Clean/Hexagonal Architecture:

```
my-service/
├── internal/
│   └── adapters/
│       └── driven/
│           └── db/       # Database Adapters
│               └── ent/  # Ent Implementation
│                   ├── schema/       # Ent Schemas
│                   ├── generated/    # Generated Code (go generate)
│                   └── user_repo.go  # Repository Implementation
```

## Usage Guide

### 1. Generate Ent Code

Define your schema and run `go generate` in your project to create the `generated` package.

### 2. Initialize Infrastructure

Use the provided `NewDriver` helper to create a standardized database connection, then initialize your generated Ent client.

```go
package main

import (
    "github.com/huynhanx03/go-common/pkg/database/ent"
    "my-service/internal/adapters/driven/db/ent/generated" 
)

func main() {
    // Configure
    cfg := ent.Config{
        Driver: "mysql",
        DSN:    "user:pass@tcp(localhost:3306)/db?parseTime=true",
    }

    // Create Driver
    drv, err := ent.NewDriver(cfg)
    if err != nil {
        panic(err)
    }

    // Create Project Client
    client := generated.NewClient(generated.Driver(drv))
    defer client.Close()
}
```

### 3. Implement Repository

Embed `ent.BaseRepository` to inherit standard CRUD operations.

```go
package repository

import (
    "context"
    "github.com/huynhanx03/go-common/pkg/database/ent"
    "my-service/internal/adapters/driven/db/ent/generated"
)

// UserRepository defines the interface (usually defined in core/ports)
type UserRepository interface {
    ent.Repository[generated.User, int]
}

// userRepository implements the interface
type userRepository struct {
    *ent.BaseRepository[generated.User, int]
}

func NewUserRepository(client *generated.Client) UserRepository {
    return &userRepository{
        // The library automatically resolves the specific Entity Client from the generic Client
        BaseRepository: ent.NewBaseRepository[generated.User, int](client),
    }
}
```

### 4. Use It

```go
func main() {
    // ... initialize client ...

    repo := repository.NewUserRepository(client)

    // Create
    user := &generated.User{Name: "Alice", Age: 25}
    err := repo.Create(ctx, user)

    // Get
    u, err := repo.Get(ctx, user.ID)

    // Advanced Search & Filter
    users, err := repo.Find(ctx, &dto.QueryOptions{
        Filters: []dto.SearchFilter{
            {Key: "name", Value: "Alice", Type: "prefix"},
        },
        Sort: []dto.SortOption{
            {Key: "created_at", Order: -1}, // Descending
        },
    })
}
```
