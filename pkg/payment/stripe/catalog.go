package stripe

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/common/apperr"
	"github.com/huynhanx03/go-common/pkg/payment"
	stripesdk "github.com/stripe/stripe-go/v85"
)

// stripeProductIDsPerRequest bounds how many product IDs one list call filters
// on, matching Stripe's per-request cap.
const stripeProductIDsPerRequest = 100

// ListProducts fetches the named products. An empty id list returns no products
// without a round trip.
func (p *Provider) ListProducts(ctx context.Context, ids []string) ([]payment.Product, *apperr.AppError) {
	products := make([]payment.Product, 0)

	if len(ids) == 0 {
		return products, nil
	}

	for _, batch := range chunkStrings(ids, stripeProductIDsPerRequest) {
		params := &stripesdk.ProductListParams{}
		for _, id := range batch {
			params.IDs = append(params.IDs, stripesdk.String(id))
		}

		for product, err := range p.client.V1Products.List(ctx, params).All(ctx) {
			if err != nil {
				return nil, p.fail("stripe products list failed", err)
			}

			if product == nil {
				continue
			}

			products = append(products, productToNeutral(product))
		}
	}

	return products, nil
}

// ListPrices fetches every price in the catalog.
func (p *Provider) ListPrices(ctx context.Context) ([]payment.Price, *apperr.AppError) {
	prices := make([]payment.Price, 0)

	for price, err := range p.client.V1Prices.List(ctx, &stripesdk.PriceListParams{}).All(ctx) {
		if err != nil {
			return nil, p.fail("stripe prices list failed", err)
		}

		if price == nil {
			continue
		}

		prices = append(prices, priceToNeutral(price))
	}

	return prices, nil
}

func productToNeutral(src *stripesdk.Product) payment.Product {
	out := payment.Product{
		Ref:         src.ID,
		Name:        src.Name,
		Description: src.Description,
		Active:      src.Active,
		Images:      src.Images,
		Metadata:    src.Metadata,
	}

	out.MarketingFeatures = make([]string, 0, len(src.MarketingFeatures))

	for _, feature := range src.MarketingFeatures {
		if feature == nil || feature.Name == "" {
			continue
		}

		out.MarketingFeatures = append(out.MarketingFeatures, feature.Name)
	}

	return out
}

func priceToNeutral(src *stripesdk.Price) payment.Price {
	out := payment.Price{
		Ref:             src.ID,
		Active:          src.Active,
		Type:            payment.PriceType(src.Type),
		UnitAmount:      moneyOf(src.UnitAmount, src.Currency),
		TaxBehavior:     payment.TaxBehavior(src.TaxBehavior),
		CurrencyOptions: priceCurrencyOptionsToNeutral(src.CurrencyOptions),
		Metadata:        src.Metadata,
	}

	if src.Product != nil {
		out.ProductRef = src.Product.ID
	}

	if src.Recurring != nil {
		out.Recurring = &payment.PriceRecurrence{
			Interval:        payment.RecurringInterval(src.Recurring.Interval),
			IntervalCount:   src.Recurring.IntervalCount,
			TrialPeriodDays: src.Recurring.TrialPeriodDays,
		}
	}

	return out
}

// priceCurrencyOptionsToNeutral turns Stripe's per-currency pricing into a
// currency -> Money map, which is how one price carries region-specific amounts.
func priceCurrencyOptionsToNeutral(src map[string]*stripesdk.PriceCurrencyOptions) map[string]payment.Money {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]payment.Money, len(src))

	for currency, opt := range src {
		if opt == nil {
			continue
		}

		out[currency] = payment.NewMoney(opt.UnitAmount, currency)
	}

	return out
}

func chunkStrings(ids []string, size int) [][]string {
	if size <= 0 || len(ids) == 0 {
		return nil
	}

	batches := make([][]string, 0, (len(ids)+size-1)/size)
	for start := 0; start < len(ids); start += size {
		end := start + size
		if end > len(ids) {
			end = len(ids)
		}

		batches = append(batches, ids[start:end])
	}

	return batches
}
