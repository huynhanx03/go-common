# Plan: Unified SQL Module with Ent (`pkg/database/ent`)

Tài liệu này mô tả thiết kế chi tiết cho module cơ sở dữ liệu SQL hợp nhất, sử dụng framework `ent` để hỗ trợ cả MySQL và PostgreSQL. Mục tiêu là cung cấp trải nghiệm Generic Repository `Create/Update/Delete/Find` tương tự như implementation của MongoDB và Elasticsearch hiện có.

## 1. Kiến trúc Tổng quan

Chúng ta sẽ không tách riêng `mysql` và `postgresql` mà gộp chung vào `pkg/database/ent`. Lý do:
-   **Abstraction**: Ent đã trừu tượng hóa lớp Driver (Dialect). Code logic giống hệt nhau, chỉ khác driver init.
-   **Maintainability**: Tránh lặp lại code xử lý Generic Repository phức tạp.
-   **Extensibility**: Dễ dàng thêm SQLite (test) hoặc TiDB (scale) mà không cần cấu trúc lại thư mục.

### 1.1 Cấu trúc thư mục

```text
pkg/database/
├── ent/
│   ├── client.go           # Factory: NewClient (Switch Driver MySQL/PG)
│   ├── config.go           # Config struct (DriverName, DSN, MaxIdle...)
│   ├── repository.go       # Core: BaseRepository[T] implementation
│   ├── interface.go        # Interfaces (Ent Mutation/Query abstractions)
│   ├── migrate.go          # Wrapper cho Schema Migration (AutoMigrate)
│   └── schema/             # Shared Test Schema (cho Unit Test)
├── interfaces.go           # Common Repository Interface (đã có)
```

## 2. Giải pháp Generic Repository cho Ent

Đây là thách thức kỹ thuật lớn nhất vì Ent là **Static Code Generation** (mỗi entity có Client riêng), không có interface chung cho `Create/Update`. Để đạt được `BaseRepository[T]`, ta cần pattern sau:

### 2.1 Interface Definition (`Model` và `Transactor`)
Model của chúng ta sẽ cần implement các method cơ bản:

```go
// T là struct được gen bởi ent (ví dụ: ent.User)
type EntModel interface {
    GetID() int // hoặc uuid, string...
    // Có thể cần thêm hooks để convert to Mutation nếu không dùng Reflection
}
```

### 2.2 BaseRepository Pattern (Reflection Based)
Để `BaseRepository` hoạt động generic, chúng ta sẽ sử dụng **Reflection** kết hợp với **Ent Runtime** để gọi các method của Client được gen ra.

*Tại sao Reflection?*
-   Ent Client (VD: `UserClient`) có method `Create()`, `UpdateOneID()`.
-   `BaseRepository[T]` nhận vào `client interface{}`.
-   Trong runtime, ta dùng reflect để gọi `client.MapCreateBulk` hoặc `client.Create`.

*Cơ chế hoạt động:*
1.  **Init**: `NewBaseRepository[T](client, schemaType)`
2.  **Create(ctx, model)**:
    -   Dùng reflect map fields từ `model` struct sang `Create` builder của Ent.
    -   Hoặc tiện hơn: Ent hỗ trợ `Save(ctx, v)` cho struct nếu setup đúng feature `sql/modifier`.
3.  **Find(ctx, queryOptions)**:
    -   Map `QueryOptions` (filter, sort, pagination) sang `ent.Predicate`.
    -   Dùng `Client.Query().Where(...).Limit(...).Offset(...).All(ctx)`.

## 3. Implementation Plan Chi Tiết

### Bước 1: Setup Environment
-   Tạo `pkg/database/ent`.
-   Cài đặt `ent`, `mysql driver`, `postgres driver`.
-   Tạo `pkg/database/ent/schema/test_model.go` (giống `TestDocument` bên Mongo) để generate code Ent làm mẫu test.
-   Chạy `go generate`.

### Bước 2: `Client` & `Config`
-   Implement `NewClient(cfg Config)`:
    -   Support `cfg.Driver`: `mysql`, `postgres`, `sqlite`, `pgx`.
    -   Setup Connection Pool (`SetMaxOpenConns`, `SetMaxIdleConns`).
    -   Setup Hooks/Interceptors (Logging/Tracing).

### Bước 3: `BaseRepository` (Core)
-   Implement `Create`, `Update`, `Delete`, `Get`.
-   Sử dụng Reflection để invoke Ent builders.
-   *Lưu ý*: Sẽ giới hạn hỗ trợ các field cơ bản (int, string, bool, time) trong phase đầu.

### Bước 4: Advanced Features
-   **Migration**: Implement `RunMigration` wrap `client.Schema.Create`.
-   **Transaction**: Implement `WithTx` support.
-   **Find**: Implement mapping từ `dto.SearchFilter` sang `sql.Predicate` (dùng `ent/dialect/sql`).

### Bước 5: Testing
-   Viết `client_test.go` dùng `Testcontainers` (test cả container MySQL và Postgres với cùng 1 bộ test case).
-   Verify toàn bộ interface `Repository`.

## 4. Tại sao thiết kế này là tốt nhất?
1.  **Consistency**: Dev dùng `repo.Create()` giống hệt MongoDB. Không cần học API riêng của Ent nếu chỉ CRUD đơn giản.
2.  **Performance**: Dù dùng Reflection, overhead là không đáng kể so với I/O network DB.
3.  **Database Agnostic**: Test trên SQLite/Docker, chạy prod trên MySQL/Postgres mà không sửa dòng code logic nào.
