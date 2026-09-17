package elasticsearch

import (
	"encoding"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"

	"github.com/huynhanx03/go-common/pkg/dto"
)

const (
	maxFieldPathBytes  = 128
	maxFieldSegments   = 8
	maxQueryValueBytes = 256
	maxWildcardBytes   = 128
	maxSortFields      = 8
	maxResultWindow    = 10_000
)

var fieldSegmentPattern = regexp.MustCompile(`^@?[A-Za-z_][A-Za-z0-9_-]*$`)

// BuildSearchQuery constructs a safe search body through the compatibility
// API. Invalid input becomes match_none rather than a broad index scan.
func BuildSearchQuery(opts *dto.QueryOptions) map[string]any {
	body, err := BuildSearchQueryChecked(opts)
	if err != nil {
		return safeSearchBody()
	}
	return body
}

// BuildSearchQueryChecked validates the complete query without mutating opts.
// Pass an optional field whitelist when the options originate from a client.
func BuildSearchQueryChecked(
	opts *dto.QueryOptions,
	allowedFields ...string,
) (map[string]any, error) {
	pagination, err := BuildPaginationChecked(queryPagination(opts))
	if err != nil {
		return nil, err
	}
	query, err := BuildFilterChecked(queryFilters(opts), allowedFields...)
	if err != nil {
		return nil, err
	}
	sortOptions, err := BuildSortChecked(querySort(opts), allowedFields...)
	if err != nil {
		return nil, err
	}
	if cursor, ok := pagination["search_after"].([]any); ok {
		if len(sortOptions) == 0 {
			return nil, fmt.Errorf("%w: search_after requires an explicit stable sort", ErrInvalidCursor)
		}
		if len(cursor) != len(sortOptions) {
			return nil, fmt.Errorf(
				"%w: got %d values for %d sort fields",
				ErrInvalidCursor,
				len(cursor),
				len(sortOptions),
			)
		}
	}

	body := make(map[string]any, len(pagination)+3)
	for key, value := range pagination {
		body[key] = value
	}
	if len(query) == 0 {
		body["query"] = map[string]any{"match_all": map[string]any{}}
	} else {
		body["query"] = query
	}
	if len(sortOptions) > 0 {
		body["sort"] = sortOptions
	}
	// Pagination metadata promises an exact total, so do not accept ES's
	// default lower-bound count after its tracking threshold.
	body["track_total_hits"] = true
	return body, nil
}

// BuildPagination creates bounded ES pagination fields through the legacy API.
func BuildPagination(pagination *dto.PaginationOptions) map[string]any {
	result, err := BuildPaginationChecked(pagination)
	if err != nil {
		return map[string]any{"size": dto.DefaultPageSize, "from": 0}
	}
	return result
}

// BuildPaginationChecked supports bounded offset and search_after pagination.
func BuildPaginationChecked(pagination *dto.PaginationOptions) (map[string]any, error) {
	normalized, err := normalizePagination(pagination)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"size": normalized.PageSize}
	if cursorProvided(normalized.Cursor) {
		cursor, err := normalizeCursor(normalized.Cursor)
		if err != nil {
			return nil, err
		}
		result["search_after"] = cursor
		return result, nil
	}
	offset, err := normalized.Offset()
	if err != nil {
		return nil, err
	}
	if offset > maxResultWindow-normalized.PageSize {
		return nil, fmt.Errorf(
			"%w: offset page exceeds the %d-result window; use a cursor",
			ErrInvalidQuery,
			maxResultWindow,
		)
	}
	result["from"] = offset
	return result, nil
}

// BuildFilter creates an ES boolean query through the compatibility API.
func BuildFilter(filters *[]dto.SearchFilter) map[string]any {
	query, err := BuildFilterChecked(filters)
	if err != nil {
		return map[string]any{"match_none": map[string]any{}}
	}
	return query
}

// BuildFilterChecked creates a validated, bounded ES boolean query.
func BuildFilterChecked(
	filters *[]dto.SearchFilter,
	allowedFields ...string,
) (map[string]any, error) {
	allowed, err := fieldAllowlist(allowedFields)
	if err != nil {
		return nil, err
	}
	if filters == nil || len(*filters) == 0 {
		return nil, nil
	}

	must := make([]map[string]any, 0, len(*filters))
	filter := make([]map[string]any, 0, len(*filters))
	for index, item := range *filters {
		if item.Key == "" {
			continue
		}
		field, err := safeField(item.Key, allowed)
		if err != nil {
			return nil, fmt.Errorf("%w: filter %d: %w", ErrInvalidQuery, index, err)
		}
		if item.Value == nil {
			continue
		}

		switch item.Type {
		case "match", "search":
			if err := validateQueryScalar(item.Value, maxQueryValueBytes, false); err != nil {
				return nil, fmt.Errorf("%w: match filter %d: %w", ErrInvalidQuery, index, err)
			}
			must = append(must, map[string]any{
				"match": map[string]any{field: item.Value},
			})
		case "phrase":
			if err := validateQueryScalar(item.Value, maxQueryValueBytes, false); err != nil {
				return nil, fmt.Errorf("%w: phrase filter %d: %w", ErrInvalidQuery, index, err)
			}
			must = append(must, map[string]any{
				"match_phrase": map[string]any{field: item.Value},
			})
		case "term", "exact", "filter", "":
			if err := validateQueryScalar(item.Value, maxQueryValueBytes, true); err != nil {
				return nil, fmt.Errorf("%w: term filter %d: %w", ErrInvalidQuery, index, err)
			}
			filter = append(filter, map[string]any{
				"term": map[string]any{field: item.Value},
			})
		case "wildcard":
			value, err := wildcardValue(item.Value)
			if err != nil {
				return nil, fmt.Errorf("%w: wildcard filter %d: %w", ErrInvalidQuery, index, err)
			}
			filter = append(filter, map[string]any{
				"wildcard": map[string]any{field: value},
			})
		default:
			return nil, fmt.Errorf("%w: filter %d has unsupported type %q", ErrInvalidQuery, index, item.Type)
		}
	}

	boolQuery := make(map[string]any, 2)
	if len(must) > 0 {
		boolQuery["must"] = must
	}
	if len(filter) > 0 {
		boolQuery["filter"] = filter
	}
	if len(boolQuery) == 0 {
		return nil, nil
	}
	return map[string]any{"bool": boolQuery}, nil
}

// BuildSort creates a validated ES sort list through the compatibility API.
func BuildSort(sorts *[]dto.SortOption) []map[string]any {
	result, err := BuildSortChecked(sorts)
	if err != nil {
		return nil
	}
	return result
}

// BuildSortChecked creates an ordered sort list and rejects ambiguous keys.
func BuildSortChecked(
	sorts *[]dto.SortOption,
	allowedFields ...string,
) ([]map[string]any, error) {
	allowed, err := fieldAllowlist(allowedFields)
	if err != nil {
		return nil, err
	}
	if sorts == nil || len(*sorts) == 0 {
		return nil, nil
	}
	if len(*sorts) > maxSortFields {
		return nil, fmt.Errorf("%w: at most %d sort fields are allowed", ErrInvalidQuery, maxSortFields)
	}
	result := make([]map[string]any, 0, len(*sorts))
	seen := make(map[string]struct{}, len(*sorts))
	for index, item := range *sorts {
		if item.Key == "" {
			continue
		}
		field, err := safeField(item.Key, allowed)
		if err != nil {
			return nil, fmt.Errorf("%w: sort %d: %w", ErrInvalidQuery, index, err)
		}
		if _, duplicate := seen[field]; duplicate {
			return nil, fmt.Errorf("%w: duplicate sort field %q", ErrInvalidQuery, field)
		}
		seen[field] = struct{}{}
		if item.Order != 1 && item.Order != -1 {
			return nil, fmt.Errorf("%w: sort %d has invalid order %d", ErrInvalidQuery, index, item.Order)
		}
		order := "asc"
		if item.Order == -1 {
			order = "desc"
		}
		result = append(result, map[string]any{
			field: map[string]any{"order": order},
		})
	}
	return result, nil
}

func queryFilters(options *dto.QueryOptions) *[]dto.SearchFilter {
	if options == nil {
		return nil
	}
	return &options.Filters
}

func querySort(options *dto.QueryOptions) *[]dto.SortOption {
	if options == nil {
		return nil
	}
	return &options.Sort
}

func queryPagination(options *dto.QueryOptions) *dto.PaginationOptions {
	if options == nil {
		return nil
	}
	return options.Pagination
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

func normalizeCursor(cursor any) ([]any, error) {
	value := reflect.ValueOf(cursor)
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return nil, ErrInvalidCursor
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil, ErrInvalidCursor
	}

	if value.Kind() != reflect.Array && value.Kind() != reflect.Slice {
		if err := validateCursorScalar(value); err != nil {
			return nil, err
		}
		return []any{value.Interface()}, nil
	}
	if value.Len() == 0 || value.Len() > maxSortFields {
		return nil, fmt.Errorf("%w: cursor must contain between 1 and %d values", ErrInvalidCursor, maxSortFields)
	}
	result := make([]any, value.Len())
	for index := 0; index < value.Len(); index++ {
		item := value.Index(index)
		if err := validateCursorScalar(item); err != nil {
			return nil, fmt.Errorf("%w: value %d: %w", ErrInvalidCursor, index, err)
		}
		result[index] = item.Interface()
	}
	return result, nil
}

func validateCursorScalar(value reflect.Value) error {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.String:
		if value.Len() > maxQueryValueBytes {
			return fmt.Errorf("%w: string cursor value is too large", ErrInvalidCursor)
		}
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
	default:
		return fmt.Errorf("%w: cursor values must be JSON scalars", ErrInvalidCursor)
	}
	if value.Kind() == reflect.Float32 || value.Kind() == reflect.Float64 {
		if number := value.Float(); math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("%w: cursor number must be finite", ErrInvalidCursor)
		}
	}
	return nil
}

func validateQueryScalar(value any, maximum int, allowEmpty bool) error {
	if marshaler, ok := value.(encoding.TextMarshaler); ok && !isNilValue(marshaler) {
		encoded, err := marshaler.MarshalText()
		if err != nil || len(encoded) > maximum || (!allowEmpty && len(encoded) == 0) {
			return fmt.Errorf("query scalar text is invalid")
		}
		return nil
	}
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() && (reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer) {
		if reflected.IsNil() {
			return fmt.Errorf("query scalar is nil")
		}
		reflected = reflected.Elem()
	}
	if !reflected.IsValid() {
		return fmt.Errorf("query scalar is nil")
	}
	switch reflected.Kind() {
	case reflect.String:
		if reflected.Len() > maximum || (!allowEmpty && reflected.Len() == 0) {
			return fmt.Errorf("query string is empty or too large")
		}
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
	case reflect.Float32, reflect.Float64:
		if number := reflected.Float(); math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("query number must be finite")
		}
	default:
		return fmt.Errorf("query value must be a scalar")
	}
	return nil
}

func wildcardValue(value any) (string, error) {
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() && (reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer) {
		if reflected.IsNil() {
			return "", fmt.Errorf("wildcard value is nil")
		}
		reflected = reflected.Elem()
	}
	if !reflected.IsValid() || reflected.Kind() != reflect.String {
		return "", fmt.Errorf("wildcard value must be a string")
	}
	result := reflected.String()
	if result == "" || len(result) > maxWildcardBytes {
		return "", fmt.Errorf("wildcard value is empty or too large")
	}
	if strings.HasPrefix(result, "*") || strings.HasPrefix(result, "?") {
		return "", fmt.Errorf("leading wildcards are not allowed")
	}
	return result, nil
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
		if _, exists := allowed[name]; !exists {
			return "", fmt.Errorf("%w: field %q is not allowed", ErrInvalidIdentifier, field)
		}
	}
	return name, nil
}

func normalizeField(field string) (string, error) {
	if field == "" || len(field) > maxFieldPathBytes {
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

func cursorProvided(cursor any) bool {
	if cursor == nil {
		return false
	}
	if value, ok := cursor.(string); ok {
		return value != ""
	}
	return true
}

func isNilValue(value any) bool {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return true
	}
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func safeSearchBody() map[string]any {
	return map[string]any{
		"size":             dto.DefaultPageSize,
		"from":             0,
		"query":            map[string]any{"match_none": map[string]any{}},
		"track_total_hits": true,
	}
}
