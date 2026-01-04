package mongodb

import (
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/huynhanx03/go-common/pkg/dto"
)

// ApplyQueryOptions builds MongoDB filter and options from QueryOptions
func ApplyQueryOptions(opts *dto.QueryOptions) (bson.M, *options.FindOptions) {
	if opts == nil {
		opts = &dto.QueryOptions{}
	}
	if opts.Pagination == nil {
		opts.Pagination = &dto.PaginationOptions{}
	}
	opts.Pagination.SetDefaults()

	// Build Filter
	filter := BuildFilter(&opts.Filters)
	if filter == nil {
		filter = bson.M{}
	}

	// Build Sort
	sort := BuildSort(&opts.Sort)

	// Pagination & Cursor
	limit := int64(opts.Pagination.PageSize)
	findOptions := &options.FindOptions{
		Limit: &limit,
		Sort:  sort,
	}

	if opts.Pagination.Cursor != nil && opts.Pagination.Cursor != "" {
		var cursorVal interface{} = opts.Pagination.Cursor
		if str, ok := opts.Pagination.Cursor.(string); ok {
			if oid, err := primitive.ObjectIDFromHex(str); err == nil {
				cursorVal = oid
			}
		}

		isAsc := false
		for _, s := range opts.Sort {
			if s.Key == "_id" || s.Key == "id" {
				if s.Order == 1 {
					isAsc = true
				}
				break
			}
		}

		cursorFilter := bson.M{}
		if isAsc {
			cursorFilter["_id"] = bson.M{"$gt": cursorVal}
		} else {
			cursorFilter["_id"] = bson.M{"$lt": cursorVal}
		}

		if _, ok := filter["_id"]; ok {
			filter = bson.M{"$and": []bson.M{filter, cursorFilter}}
		} else {
			for k, v := range cursorFilter {
				filter[k] = v
			}
		}
	} else {
		skip := int64((opts.Pagination.Page - 1) * opts.Pagination.PageSize)
		findOptions.Skip = &skip
	}

	return filter, findOptions
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
