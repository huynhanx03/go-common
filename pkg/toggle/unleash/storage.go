package unleash

import "github.com/Unleash/unleash-go-sdk/v6/api"

// memoryStorage hands a private bootstrap to the SDK. Runtime state belongs to
// the SDK; Persist deliberately performs no durable write.
type memoryStorage struct{ initial *api.FeatureResponse }

func (*memoryStorage) Init(string, string)                   {}
func (s *memoryStorage) Load() (*api.FeatureResponse, error) { return s.initial, nil }
func (*memoryStorage) Persist(*api.FeatureResponse) error    { return nil }
