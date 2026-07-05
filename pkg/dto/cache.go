package dto

// GeoLocation represents a geospatial location stored in a cache engine.
type GeoLocation struct {
	Member    string
	Longitude float64
	Latitude  float64
}

// ZMember represents a member of a sorted set with its score.
type ZMember struct {
	Score  float64
	Member any
}
