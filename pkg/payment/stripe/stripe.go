// Package stripe is the reference payment provider: a Stripe adapter that
// implements the capability interfaces in pkg/payment. It translates Stripe SDK
// types to and from the neutral domain model and never exposes a Stripe type in
// its public surface.
//
// Wire it either explicitly with New, or by name after a blank import registers
// it with the payment registry:
//
//	import _ "github.com/huynhanx03/go-common/pkg/payment/stripe"
//	p, appErr := payment.Open(stripe.ProviderName, payment.Config{APIKey: sk, WebhookSecret: wh, Logger: log})
package stripe

import (
	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

// ProviderName is the key this driver registers under.
const ProviderName = "stripe"

// Compile-time proof that the Stripe provider satisfies the framework's Provider
// handle and every capability interface it advertises. If a signature drifts,
// the build breaks here rather than at a caller.
var (
	_ payment.Provider               = (*Provider)(nil)
	_ payment.SubscriptionGateway    = (*Provider)(nil)
	_ payment.CustomerAccountGateway = (*Provider)(nil)
	_ payment.InvoiceGateway         = (*Provider)(nil)
	_ payment.PaymentIntentGateway   = (*Provider)(nil)
	_ payment.ScheduleGateway        = (*Provider)(nil)
	_ payment.EventDecoder           = (*Provider)(nil)
	_ payment.CheckoutGateway        = (*Provider)(nil)
	_ payment.CatalogGateway         = (*Provider)(nil)
	_ payment.PaymentMethodGateway   = (*Provider)(nil)
	_ payment.SetupIntentGateway     = (*Provider)(nil)
	_ payment.RefundGateway          = (*Provider)(nil)
)

func init() {
	payment.Register(ProviderName, func(cfg payment.Config) (payment.Provider, *apperr.AppError) {
		return New(cfg)
	})
}

// Provider is the Stripe implementation of the payment capability interfaces and
// payment.EventDecoder. Construct it with New.
type Provider struct {
	client        *stripesdk.Client
	logger        *logger.LoggerZap
	webhookSecret string
}

// New builds a Stripe provider from cfg. It requires cfg.APIKey; cfg.WebhookSecret
// is required only to verify webhooks (VerifyAndDecode). The logger is optional.
func New(cfg payment.Config) (*Provider, *apperr.AppError) {
	if cfg.APIKey == "" {
		return nil, payment.Invalid("stripe: missing API key")
	}

	return &Provider{
		client:        stripesdk.NewClient(cfg.APIKey),
		logger:        cfg.Logger,
		webhookSecret: cfg.WebhookSecret,
	}, nil
}

// newWithClient builds a provider around an already-configured SDK client. It
// exists for tests that point the client at a stub backend.
func newWithClient(client *stripesdk.Client, log *logger.LoggerZap, webhookSecret string) *Provider {
	return &Provider{client: client, logger: log, webhookSecret: webhookSecret}
}

// Name identifies the provider.
func (p *Provider) Name() string { return ProviderName }
