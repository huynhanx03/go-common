//go:build integration

package ent_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/huynhanx03/go-common/pkg/testutil/containers"
	"github.com/huynhanx03/go-common/pkg/toggle"
	toggleent "github.com/huynhanx03/go-common/pkg/toggle/ent"
	"github.com/huynhanx03/go-common/pkg/toggle/ent/internal/testent"
	"github.com/huynhanx03/go-common/pkg/toggle/unleash"
	_ "github.com/lib/pq"
)

var postgresScope = toggle.Scope{Namespace: "billing", Environment: "test"}

func newPostgres(t *testing.T) (*sql.DB, dialect.Driver, *toggleent.Store, *toggle.Manager) {
	t.Helper()
	dsn := containers.StartPostgres(t)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	driver := entsql.OpenDB(dialect.Postgres, db)
	fixture := testent.NewClient(testent.Driver(driver))
	if err := fixture.Schema.Create(ctx); err != nil {
		t.Fatalf("migrate toggle test schemas: %v", err)
	}
	store, err := toggleent.NewStore(driver)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := toggle.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	return db, driver, store, manager
}

func integrationFlag(key string, enabled bool) toggle.Flag {
	return toggle.Flag{Key: key, Enabled: enabled}
}

func targetedFlag(user string) toggle.Flag {
	return toggle.Flag{
		Key: "checkout-v2", Enabled: true,
		Rules: []toggle.Rule{{
			Rollout:     100,
			Constraints: []toggle.Constraint{{ContextName: "userId", Operator: "IN", Values: []string{user}}},
			Variants:    []toggle.Variant{{Name: "new-checkout", Weight: 1000, Payload: &toggle.Payload{Type: "json", Value: `{"version":2}`}}},
		}},
	}
}

func newEntClient(t *testing.T, source toggle.Source, refresh time.Duration) toggle.Client {
	t.Helper()
	client, err := unleash.New(context.Background(), unleash.Config{
		Scope: postgresScope, AppName: "billing-integration", Source: source,
		RefreshInterval: refresh, SourceTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.Close(ctx); err != nil {
			t.Errorf("close Unleash client: %v", err)
		}
	})
	return client
}

func waitEnabled(t *testing.T, client toggle.Client, key, user string, want bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		got, err := client.IsEnabled(context.Background(), key, toggle.EvaluationContext{UserID: user}, false)
		if err == nil && got == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("flag %q for user %q did not become %v (got %v, err %v)", key, user, want, got, err)
		case <-ticker.C:
		}
	}
}
