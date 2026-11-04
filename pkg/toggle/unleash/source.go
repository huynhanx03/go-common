package unleash

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/Unleash/unleash-go-sdk/v6/api"
	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/toggle/core"
)

type sourceTransport struct {
	source   core.Source
	scope    core.Scope
	timeout  time.Duration
	lifetime context.Context
	accepted core.Snapshot
	first    bool
}

func (s *sourceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !isSDKFeaturesRequest(request) {
		return nil, core.SourceError(core.ErrInvalidConfig)
	}
	ctx, cancel := context.WithTimeout(request.Context(), s.timeout)
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, core.SourceError(err)
	}
	if err := s.lifetime.Err(); err != nil {
		return nil, core.SourceError(err)
	}
	candidate := s.accepted
	if !s.first {
		loaded, err := s.source.Load(ctx, s.scope)
		if err != nil {
			return nil, core.SourceError(err)
		}
		if err := ctx.Err(); err != nil {
			return nil, core.SourceError(err)
		}
		candidate, err = core.NormalizeSnapshot(s.scope, loaded)
		if err != nil {
			return nil, core.SourceError(err)
		}
		if candidate.Revision < s.accepted.Revision || (candidate.Revision == s.accepted.Revision && !reflect.DeepEqual(candidate, s.accepted)) {
			return nil, core.SourceError(core.ErrStaleRevision)
		}
		if reflect.DeepEqual(candidate, s.accepted) {
			return response(request, http.StatusNotModified, nil), nil
		}
	}
	body, err := jsoncodec.Marshal(mapSnapshot(candidate))
	if err != nil {
		return nil, core.SourceError(err)
	}
	s.accepted = candidate
	s.first = false
	return response(request, http.StatusOK, body), nil
}

func isSDKFeaturesRequest(request *http.Request) bool {
	return request.Method == http.MethodGet && request.URL.Scheme == "http" &&
		request.URL.Host == sdkHost && request.URL.Path == sdkFeaturesPath &&
		request.URL.RawQuery == "" && request.URL.User == nil && request.URL.Fragment == ""
}

func response(request *http.Request, status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{sdkContentType}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request}
}

func mapSnapshot(snapshot core.Snapshot) *api.FeatureResponse {
	response := &api.FeatureResponse{Features: make([]api.Feature, 0, len(snapshot.Flags)), Segments: []api.Segment{}}
	for _, flag := range snapshot.Flags {
		feature := api.Feature{Name: flag.Key, Description: flag.Description, Enabled: flag.Enabled}
		for _, rule := range flag.Rules {
			strategy := api.Strategy{Name: sdkRolloutStrategy, Parameters: api.ParameterMap{"rollout": strconv.Itoa(rule.Rollout), "groupId": rule.GroupID, "stickiness": rule.Stickiness}}
			for _, constraint := range rule.Constraints {
				strategy.Constraints = append(strategy.Constraints, api.Constraint{ContextName: constraint.ContextName, Operator: api.Operator(constraint.Operator), Value: constraint.Value, Values: append([]string(nil), constraint.Values...), CaseInsensitive: constraint.CaseInsensitive, Inverted: constraint.Inverted})
			}
			for _, variant := range rule.Variants {
				mapped := api.VariantInternal{Variant: api.Variant{Name: variant.Name}, Weight: variant.Weight, WeightType: sdkVariantWeightType, Stickiness: rule.Stickiness}
				if variant.Payload != nil {
					mapped.Payload = api.Payload{Type: variant.Payload.Type, Value: variant.Payload.Value}
				}
				strategy.Variants = append(strategy.Variants, mapped)
			}
			feature.Strategies = append(feature.Strategies, strategy)
		}
		response.Features = append(response.Features, feature)
	}
	return response
}

// bootstrapStorage seeds the SDK with the already-loaded snapshot. The SDK owns
// its runtime cache; Persist intentionally does not write durable data.
type bootstrapStorage struct{ initial *api.FeatureResponse }

func (*bootstrapStorage) Init(string, string)                   {}
func (s *bootstrapStorage) Load() (*api.FeatureResponse, error) { return s.initial, nil }
func (*bootstrapStorage) Persist(*api.FeatureResponse) error    { return nil }
