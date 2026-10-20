package mongodb

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/huynhanx03/go-common/pkg/dto"
)

const (
	maxFieldPathBytes = 128
	maxFieldSegments  = 8
	maxSearchBytes    = 256
)

var fieldSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ApplyQueryOptions builds MongoDB filter and options from QueryOptions. The
// legacy no-error API returns a safe empty result when validation fails; new
// repository code should use ApplyQueryOptionsChecked to surface the error.
func ApplyQueryOptions(opts *dto.QueryOptions) (bson.M, *options.FindOptions) {
	filter, findOptions, err := ApplyQueryOptionsChecked(opts)
	if err != nil {
		return denyAllFilter(), safeDefaultFindOptions()
	}
	return filter, findOptions
}

// ApplyQueryOptionsChecked validates keys, pagination, and cursor semantics
// without mutating the caller's options.
func ApplyQueryOptionsChecked(
	opts *dto.QueryOptions,
	allowedFields ...string,
) (bson.M, *options.FindOptions, error) {
	filter, err := BuildFilterChecked(queryFilters(opts), allowedFields...)
	if err != nil {
		return nil, nil, err
	}
	sort, err := BuildSortChecked(querySort(opts), allowedFields...)
	if err != nil {
		return nil, nil, err
	}
	pagination, err := normalizePagination(queryPagination(opts))
	if err != nil {
		return nil, nil, err
	}

	limit := int64(pagination.PageSize)
	findOptions := options.Find().SetLimit(limit).SetSort(sort)
	if cursorProvided(pagination.Cursor) {
		cursor, direction, err := decodeCursor(pagination.Cursor, sort)
		if err != nil {
			return nil, nil, err
		}
		cursorFilter := bson.M{"_id": bson.M{direction: cursor}}
		filter = andFilter(filter, cursorFilter)
		return filter, findOptions, nil
	}
	offset, err := pagination.Offset()
	if err != nil {
		return nil, nil, err
	}
	findOptions.SetSkip(int64(offset))
	return filter, findOptions, nil
}

// BuildFilterChecked creates a validated MongoDB filter. Field paths are
// syntax-checked and optionally restricted to a caller-provided whitelist.
func BuildFilterChecked(
	filters *[]dto.SearchFilter,
	allowedFields ...string,
) (bson.M, error) {
	allowed, err := fieldAllowlist(allowedFields)
	if err != nil {
		return nil, err
	}
	filter := bson.M{}
	if filters == nil {
		return filter, nil
	}
	for index, item := range *filters {
		if item.Key == "" {
			continue
		}
		key, err := safeField(item.Key, allowed)
		if err != nil {
			return nil, fmt.Errorf("%w: filter %d: %w", ErrInvalidQuery, index, err)
		}
		if item.Value == nil {
			continue
		}
		if err := validateFilterValue(item.Value); err != nil {
			return nil, fmt.Errorf("%w: filter %d: %w", ErrInvalidQuery, index, err)
		}
		switch item.Type {
		case "search":
			value, ok := item.Value.(string)
			if !ok || value == "" || len(value) > maxSearchBytes {
				return nil, fmt.Errorf("%w: search filter %d has invalid value", ErrInvalidQuery, index)
			}
			filter[key] = bson.M{"$regex": regexp.QuoteMeta(value), "$options": "i"}
		case "exact", "filter":
			if item.Type == "filter" {
				filter[key] = normalizeFilterValue(key, item.Value)
			} else {
				filter[key] = item.Value
			}
		case "":
			// An empty type retains the historical exact-match default.
			filter[key] = item.Value
		default:
			return nil, fmt.Errorf("%w: filter %d has unsupported type %q", ErrInvalidQuery, index, item.Type)
		}
	}
	return filter, nil
}

// BuildFilter creates a validated filter through the compatibility API. An
// invalid request becomes a no-match filter instead of a broad collection scan.
func BuildFilter(filters *[]dto.SearchFilter) bson.M {
	filter, err := BuildFilterChecked(filters)
	if err != nil {
		return denyAllFilter()
	}
	return filter
}

// BuildSortChecked creates a validated sort map. The default _id order gives
// cursor pagination a stable keyset when the caller does not specify a sort.
func BuildSortChecked(
	sorts *[]dto.SortOption,
	allowedFields ...string,
) (bson.M, error) {
	allowed, err := fieldAllowlist(allowedFields)
	if err != nil {
		return nil, err
	}
	sort := bson.M{}
	if sorts != nil {
		for index, item := range *sorts {
			if item.Key == "" {
				continue
			}
			key, err := safeField(item.Key, allowed)
			if err != nil {
				return nil, fmt.Errorf("%w: sort %d: %w", ErrInvalidQuery, index, err)
			}
			if item.Order != 1 && item.Order != -1 {
				return nil, fmt.Errorf("%w: sort %d has invalid order %d", ErrInvalidQuery, index, item.Order)
			}
			sort[key] = item.Order
		}
	}
	if len(sort) == 0 {
		sort["_id"] = -1
	}
	return sort, nil
}

// BuildSort is the compatibility wrapper around BuildSortChecked.
func BuildSort(sorts *[]dto.SortOption) bson.M {
	sort, err := BuildSortChecked(sorts)
	if err != nil {
		return bson.M{"_id": -1}
	}
	return sort
}

func queryFilters(opts *dto.QueryOptions) *[]dto.SearchFilter {
	if opts == nil {
		return nil
	}
	return &opts.Filters
}

func querySort(opts *dto.QueryOptions) *[]dto.SortOption {
	if opts == nil {
		return nil
	}
	return &opts.Sort
}

func queryPagination(opts *dto.QueryOptions) *dto.PaginationOptions {
	if opts == nil {
		return nil
	}
	return opts.Pagination
}

func normalizePagination(input *dto.PaginationOptions) (dto.PaginationOptions, error) {
	pagination := dto.PaginationOptions{Page: 1, PageSize: dto.DefaultPageSize}
	if input != nil {
		pagination = *input
		if pagination.Page < 0 || pagination.PageSize < 0 {
			return dto.PaginationOptions{}, dto.ErrInvalidPagination
		}
		if pagination.Page == 0 {
			pagination.Page = 1
		}
		if pagination.PageSize == 0 {
			pagination.PageSize = dto.DefaultPageSize
		}
	}
	if err := pagination.Validate(); err != nil {
		return dto.PaginationOptions{}, err
	}
	return pagination, nil
}

func decodeCursor(value any, sort bson.M) (primitive.ObjectID, string, error) {
	var cursor primitive.ObjectID
	switch typed := value.(type) {
	case primitive.ObjectID:
		cursor = typed
	case string:
		parsed, err := primitive.ObjectIDFromHex(typed)
		if err != nil {
			return primitive.NilObjectID, "", fmt.Errorf("%w: expected ObjectID", ErrInvalidCursor)
		}
		cursor = parsed
	default:
		return primitive.NilObjectID, "", ErrInvalidCursor
	}
	if cursor.IsZero() {
		return primitive.NilObjectID, "", ErrInvalidCursor
	}
	if len(sort) != 1 {
		return primitive.NilObjectID, "", fmt.Errorf("%w: cursor requires an _id-only sort", ErrInvalidCursor)
	}
	order, ok := sort["_id"].(int)
	if !ok {
		return primitive.NilObjectID, "", fmt.Errorf("%w: cursor requires an _id-only sort", ErrInvalidCursor)
	}
	if order == 1 {
		return cursor, "$gt", nil
	}
	return cursor, "$lt", nil
}

func andFilter(base, extra bson.M) bson.M {
	if len(base) == 0 {
		return extra
	}
	clauses := make([]bson.M, 0, 2)
	clauses = append(clauses, cloneFilter(base), extra)
	return bson.M{"$and": clauses}
}

func cloneFilter(filter bson.M) bson.M {
	clone := make(bson.M, len(filter))
	for key, value := range filter {
		clone[key] = value
	}
	return clone
}

func normalizeFilterValue(field string, value any) any {
	if field != "_id" {
		return value
	}
	if stringValue, ok := value.(string); ok {
		if objectID, err := primitive.ObjectIDFromHex(stringValue); err == nil {
			return bson.M{"$in": []primitive.ObjectID{objectID}}
		}
	}
	return value
}

func validateFilterValue(value any) error {
	return validateFilterValueDepth(reflect.ValueOf(value), 0)
}

func validateFilterValueDepth(value reflect.Value, depth int) error {
	if !value.IsValid() {
		return nil
	}
	if depth > maxFieldSegments {
		return fmt.Errorf("%w: filter value nesting is too deep", ErrInvalidQuery)
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			key := iterator.Key()
			if key.Kind() == reflect.String && strings.HasPrefix(key.String(), "$") {
				return fmt.Errorf("%w: MongoDB operators are not accepted in filter values", ErrInvalidQuery)
			}
			if err := validateFilterValueDepth(iterator.Value(), depth+1); err != nil {
				return err
			}
		}
	case reflect.Array, reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			if err := validateFilterValueDepth(value.Index(index), depth+1); err != nil {
				return err
			}
		}
	case reflect.Struct:
		// Struct values (for example time.Time and ObjectID) are encoded as
		// literal values by the driver and cannot introduce query operators.
	default:
	}
	return nil
}

func fieldAllowlist(fields []string) (map[string]struct{}, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	allowed := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		name, err := normalizeField(field)
		if err != nil {
			return nil, fmt.Errorf("%w: allowlist field %q", ErrInvalidIdentifier, field)
		}
		allowed[name] = struct{}{}
	}
	return allowed, nil
}

func safeField(field string, allowed map[string]struct{}) (string, error) {
	name, err := normalizeField(field)
	if err != nil {
		return "", err
	}
	if allowed != nil {
		if _, ok := allowed[name]; !ok {
			return "", fmt.Errorf("%w: field %q is not allowed", ErrInvalidIdentifier, field)
		}
	}
	return name, nil
}

func normalizeField(field string) (string, error) {
	if field == "id" {
		return "_id", nil
	}
	if field == "" || len(field) > maxFieldPathBytes || strings.HasPrefix(field, "$") {
		return "", ErrInvalidIdentifier
	}
	parts := strings.Split(field, ".")
	if len(parts) > maxFieldSegments {
		return "", ErrInvalidIdentifier
	}
	for _, part := range parts {
		if !fieldSegmentPattern.MatchString(part) {
			return "", ErrInvalidIdentifier
		}
	}
	return field, nil
}

func denyAllFilter() bson.M {
	return bson.M{"$expr": bson.M{"$eq": []any{1, 0}}}
}

func safeDefaultFindOptions() *options.FindOptions {
	limit := int64(dto.DefaultPageSize)
	skip := int64(0)
	sort := bson.M{"_id": -1}
	return &options.FindOptions{Limit: &limit, Skip: &skip, Sort: sort}
}
