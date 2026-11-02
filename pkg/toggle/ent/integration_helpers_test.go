//go:build integration

package ent_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/testutil/containers"
	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/core"
	toggleent "github.com/huynhanx03/go-common/pkg/toggle/ent"
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
	if err := createToggleSchema(ctx, db); err != nil {
		t.Fatalf("create toggle test schema: %v", err)
	}
	driver := entsql.OpenDB(dialect.Postgres, db)
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

func createToggleSchema(ctx context.Context, db *sql.DB) error {
	scopeTable := fmt.Sprintf(`
		CREATE TABLE toggle_scopes (
			id SERIAL PRIMARY KEY,
			namespace VARCHAR(%d) NOT NULL,
			environment VARCHAR(%d) NOT NULL,
			revision BIGINT NOT NULL DEFAULT 0,
			CONSTRAINT toggle_revision_nonnegative CHECK (revision >= 0),
			CONSTRAINT scope_namespace_environment UNIQUE (namespace, environment)
		)`, core.MaxIdentifierLength, core.MaxIdentifierLength)
	flagTable := fmt.Sprintf(`
		CREATE TABLE toggle_flags (
			id SERIAL PRIMARY KEY,
			namespace VARCHAR(%d) NOT NULL,
			environment VARCHAR(%d) NOT NULL,
			key VARCHAR(%d) NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT FALSE,
			description VARCHAR(%d) NOT NULL DEFAULT '',
			definition JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			CONSTRAINT flag_namespace_environment_key UNIQUE (namespace, environment, key)
		)`, core.MaxIdentifierLength, core.MaxIdentifierLength, core.MaxIdentifierLength, core.MaxFlagDescriptionLength)
	if _, err := db.ExecContext(ctx, scopeTable); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, flagTable)
	return err
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
	refreshSeconds := 0
	if refresh > 0 {
		refreshSeconds = int(math.Ceil(refresh.Seconds()))
	}
	client, err := toggle.New(context.Background(), settings.Toggle{
		Namespace: postgresScope.Namespace, Environment: settings.Env(postgresScope.Environment), AppName: "billing-integration",
		RefreshIntervalSeconds: refreshSeconds, SourceTimeoutSeconds: 1,
	}, source)
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
