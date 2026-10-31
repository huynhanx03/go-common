package ent_test

import (
	"context"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/huynhanx03/go-common/pkg/toggle"
	toggleent "github.com/huynhanx03/go-common/pkg/toggle/ent"
)

type dialectDriver struct{ name string }

func (d *dialectDriver) Exec(context.Context, string, any, any) error  { return nil }
func (d *dialectDriver) Query(context.Context, string, any, any) error { return nil }
func (d *dialectDriver) Tx(context.Context) (dialect.Tx, error)        { return dialect.NopTx(d), nil }
func (d *dialectDriver) Close() error                                  { return nil }
func (d *dialectDriver) Dialect() string                               { return d.name }

func TestNewStoreRejectsMissingOrUnsupportedDriver(t *testing.T) {
	if _, err := toggleent.NewStore(nil); !errors.Is(err, toggle.ErrInvalidConfig) {
		t.Fatalf("NewStore(nil) error = %v, want ErrInvalidConfig", err)
	}
	if _, err := toggleent.NewStore(&dialectDriver{name: dialect.MySQL}); !errors.Is(err, toggle.ErrInvalidConfig) {
		t.Fatalf("NewStore(mysql) error = %v, want ErrInvalidConfig", err)
	}
}
