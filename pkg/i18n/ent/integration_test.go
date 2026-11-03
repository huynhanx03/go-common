//go:build integration

package ent_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/common/cache/ristretto"
	"github.com/huynhanx03/go-common/pkg/i18n"
	i18nent "github.com/huynhanx03/go-common/pkg/i18n/ent"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/huynhanx03/go-common/pkg/testutil/containers"
	_ "github.com/lib/pq"
)

func postgresManager(t *testing.T) (*sql.DB, *i18nent.Store, *i18n.Manager, *cache.Cache) {
	t.Helper()
	db, err := sql.Open("postgres", containers.StartPostgres(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	create := fmt.Sprintf(`CREATE TABLE %s (
		id SERIAL PRIMARY KEY,
		domain VARCHAR(%d) NOT NULL,
		revision BIGINT NOT NULL CHECK (revision > 0),
		state JSONB NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		CONSTRAINT i18n_domain_unique UNIQUE (domain),
		CONSTRAINT i18n_state_size CHECK (octet_length(state::text) <= %d)
	)`, i18nent.TableName, i18n.MaxIdentifierBytes, i18nent.MaxJSONBTextBytes)
	if _, err := db.ExecContext(ctx, create); err != nil {
		t.Fatal(err)
	}
	store, err := i18nent.NewStore(entsql.OpenDB(dialect.Postgres, db))
	if err != nil {
		t.Fatal(err)
	}
	log, err := logger.NewLogger(logger.LoggerConfig{Level: "error"})
	if err != nil {
		t.Fatal(err)
	}
	l1, err := ristretto.New[string, any](ristretto.WithNumCounters(10000), ristretto.WithMaxCost(8<<20))
	if err != nil {
		t.Fatal(err)
	}
	sharedCache, err := cache.New(cache.Config{L1: l1, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sharedCache.Close)
	manager, err := i18n.NewManager(store, sharedCache)
	if err != nil {
		t.Fatal(err)
	}
	return db, store, manager, sharedCache
}

func TestPostgresCatalogPublicationPersistsAndLoadsLocally(t *testing.T) {
	_, store, manager, sharedCache := postgresManager(t)
	ctx := context.Background()
	revision, err := manager.Configure(ctx, "billing", i18n.DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}}, 0)
	if err != nil || revision != 1 {
		t.Fatalf("configure revision=%d err=%v", revision, err)
	}
	revision, err = manager.PutKey(ctx, "billing", i18n.Definition{Key: "invoice.ready", Source: i18n.Forms{Other: "Hóa đơn {{.Number}} đã sẵn sàng"}}, revision)
	if err != nil || revision != 2 {
		t.Fatalf("put key revision=%d err=%v", revision, err)
	}
	revision, err = manager.PutTranslation(ctx, "billing", "invoice.ready", "en", i18n.Forms{Other: "Invoice {{.Number}} is ready"}, revision)
	if err != nil || revision != 3 {
		t.Fatalf("put translation revision=%d err=%v", revision, err)
	}
	revision, err = manager.Review(ctx, "billing", "invoice.ready", "en", revision)
	if err != nil || revision != 4 {
		t.Fatalf("review revision=%d err=%v", revision, err)
	}
	revision, err = manager.Publish(ctx, "billing", revision)
	if err != nil || revision != 5 {
		t.Fatalf("publish revision=%d err=%v", revision, err)
	}
	if otherRevision, err := manager.Configure(ctx, "other-domain", i18n.DomainConfig{DefaultLocale: "vi", Locales: []string{"vi", "en"}}, 0); err != nil || otherRevision != 1 {
		t.Fatalf("configure independent domain revision=%d err=%v", otherRevision, err)
	}
	other, err := manager.Get(ctx, "other-domain")
	if err != nil || len(other.Draft.Keys) != 0 {
		t.Fatalf("other domain received billing keys: state=%+v err=%v", other, err)
	}
	if _, err := manager.Load(ctx, "other-domain"); !errors.Is(err, i18n.ErrNotFound) {
		t.Fatalf("unpublished domain source error=%v", err)
	}

	state, err := store.Read(ctx, "billing")
	if err != nil || state.Published == nil || state.Published.Revision != revision || state.Published.Keys[0].Translations["en"].Forms.Other != "Invoice {{.Number}} is ready" {
		t.Fatalf("persisted publication=%+v err=%v", state.Published, err)
	}
	client, err := i18n.New(ctx, settings.I18n{Domain: "billing"}, i18n.ClientConfig{Source: manager, Cache: sharedCache})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	got, err := client.Translate(ctx, "invoice.ready", i18n.Options{Locale: "en", Params: map[string]any{"Number": "INV-42"}})
	if err != nil || got.Text != "Invoice INV-42 is ready" || got.Revision != revision {
		t.Fatalf("translated=%+v err=%v", got, err)
	}
	if _, err := manager.PutKey(ctx, "billing", i18n.Definition{Key: "stale", Source: i18n.Forms{Other: "stale"}}, revision-1); !errors.Is(err, i18n.ErrConflict) {
		t.Fatalf("stale compare-and-swap error=%v", err)
	}
	state, err = store.Read(ctx, "billing")
	if err != nil || state.Draft.Revision != revision || state.Published == nil || state.Published.Revision != revision {
		t.Fatalf("stored state=%+v err=%v", state, err)
	}
	var successes, conflicts int
	var counts sync.Mutex
	var writes sync.WaitGroup
	for _, key := range []string{"invoice.next-a", "invoice.next-b"} {
		writes.Add(1)
		go func(key string) {
			defer writes.Done()
			_, err := manager.PutKey(ctx, "billing", i18n.Definition{Key: key, Source: i18n.Forms{Other: key}}, revision)
			counts.Lock()
			defer counts.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, i18n.ErrConflict):
				conflicts++
			default:
				t.Errorf("concurrent catalog write: %v", err)
			}
		}(key)
	}
	writes.Wait()
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent writes successes=%d conflicts=%d", successes, conflicts)
	}
}
