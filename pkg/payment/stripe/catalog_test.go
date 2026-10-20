package stripe

import (
	"context"
	"testing"

	"github.com/huynhanx03/go-common/pkg/payment"
)

const productListBody = `{
  "object": "list",
  "url": "/v1/products",
  "has_more": false,
  "data": [{
    "id": "prod_1",
    "object": "product",
    "name": "Pro",
    "description": "Pro plan",
    "active": false,
    "images": ["https://img.local/pro.png"],
    "marketing_features": [{"name": "Unlimited scans"}, {"name": ""}, null],
    "metadata": {"plan_tier": "pro"}
  }]
}`

// A price with regional pricing: base USD plus a VND currency option.
const priceListBody = `{
  "object": "list",
  "url": "/v1/prices",
  "has_more": false,
  "data": [{
    "id": "price_month",
    "object": "price",
    "active": true,
    "currency": "usd",
    "type": "recurring",
    "unit_amount": 1000,
    "tax_behavior": "exclusive",
    "product": {"id": "prod_1", "object": "product"},
    "recurring": {"interval": "month", "interval_count": 1, "trial_period_days": 14},
    "currency_options": {"vnd": {"unit_amount": 250000, "tax_behavior": "inclusive"}}
  }]
}`

func TestListProductsMapsNeutralProducts(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, productListBody)

	products, appErr := provider.ListProducts(context.Background(), []string{"prod_1"})
	if appErr != nil {
		t.Fatalf("ListProducts returned an error: %v", appErr)
	}

	if captured.method != "GET" || captured.path != "/v1/products" {
		t.Fatalf("%s %s", captured.method, captured.path)
	}

	if len(products) != 1 {
		t.Fatalf("products = %+v", products)
	}

	// An archived product still comes back; filtering is the caller's choice.
	if products[0].Active {
		t.Error("Active must reflect the provider value verbatim")
	}

	// Empty and null marketing features are dropped.
	if len(products[0].MarketingFeatures) != 1 || products[0].MarketingFeatures[0] != "Unlimited scans" {
		t.Fatalf("marketing features = %+v", products[0].MarketingFeatures)
	}
}

func TestListProductsEmptyIDsSkipsRequest(t *testing.T) {
	t.Parallel()

	provider, captured := newTestProvider(t, productListBody)

	products, appErr := provider.ListProducts(context.Background(), nil)
	if appErr != nil {
		t.Fatalf("ListProducts returned an error: %v", appErr)
	}

	if len(products) != 0 {
		t.Fatalf("expected no products, got %+v", products)
	}

	if captured.method != "" {
		t.Fatalf("no request should be made for an empty id list, saw %s %s", captured.method, captured.path)
	}
}

func TestListPricesMapsRegionalPricing(t *testing.T) {
	t.Parallel()

	provider, _ := newTestProvider(t, priceListBody)

	prices, appErr := provider.ListPrices(context.Background())
	if appErr != nil {
		t.Fatalf("ListPrices returned an error: %v", appErr)
	}

	if len(prices) != 1 {
		t.Fatalf("prices = %+v", prices)
	}

	price := prices[0]
	if price.UnitAmount != payment.NewMoney(1000, "usd") {
		t.Fatalf("UnitAmount = %v", price.UnitAmount)
	}

	if price.Type != payment.PriceTypeRecurring || price.TaxBehavior != payment.TaxBehaviorExclusive {
		t.Fatalf("type/tax = %v / %v", price.Type, price.TaxBehavior)
	}

	// Regional pricing: the VND amount resolves through AmountFor.
	vnd, ok := price.AmountFor("vnd")
	if !ok || vnd != payment.NewMoney(250000, "vnd") {
		t.Fatalf("AmountFor(vnd) = %v, %t", vnd, ok)
	}

	if price.Recurring == nil || price.Recurring.TrialPeriodDays != 14 {
		t.Fatalf("recurring = %+v", price.Recurring)
	}
}
