package mongodb

import (
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/huynhanx03/go-common/pkg/dto"
)

// GetPaginationOptions creates MongoDB options for pagination
func GetPaginationOptions(p *dto.PaginationOptions) *options.FindOptions {
	limit := int64(p.PageSize)

	findOptions := &options.FindOptions{
		Limit: &limit,
	}

	// Only use skip if no cursor is provided
	if p.Cursor == "" {
		skip := int64((p.Page - 1) * p.PageSize)
		findOptions.Skip = &skip
	}

	return findOptions
}

// BuildFilter creates MongoDB filter from SearchFilter slice
func BuildFilter(filters *[]dto.SearchFilter) bson.M {
	filter := bson.M{}
	
	if filters == nil {
		return filter
	}

	for i := range *filters {
		f := &(*filters)[i]
		if f.Key == "" || f.Value == nil {
			continue
		}

		switch f.Type {
		case "search":
			// Text search using regex
			if str, ok := f.Value.(string); ok && str != "" {
				filter[f.Key] = bson.M{"$regex": str, "$options": "i"}
			}
		case "exact":
			filter[f.Key] = f.Value
		case "filter":
			if str, ok := f.Value.(string); ok {
				// Convert string ID to ObjectID
				if objectID, err := primitive.ObjectIDFromHex(str); err == nil {
					filter[f.Key] = bson.M{"$in": []primitive.ObjectID{objectID}}
				}
			} else {
				filter[f.Key] = f.Value
			}
		default:
			// Default to exact match
			filter[f.Key] = f.Value
		}
	}

	return filter
}

// BuildSort creates MongoDB sort from SortOption slice
func BuildSort(sorts *[]dto.SortOption) bson.M {
	sort := bson.M{}

	if sorts == nil {
		// Default sort if no sort specified
		sort["created_at"] = -1
		return sort
	}

	for i := range *sorts {
		s := &(*sorts)[i]
		if s.Key == "" {
			continue
		}

		order := s.Order
		if order != 1 && order != -1 {
			order = -1 // Default to descending
		}
		sort[s.Key] = order
	}

	// Default sort if no valid sort keys found
	if len(sort) == 0 {
		sort["created_at"] = -1
	}

	return sort
}
