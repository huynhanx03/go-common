package ent

import (
	"fmt"
	"reflect"

	"github.com/huynhanx03/go-common/pkg/dto"

	"entgo.io/ent/dialect/sql"
)

// ApplyQueryOptions applies pagination, filters, and sorting options to the Ent query builder.
func ApplyQueryOptions(queryBuilder reflect.Value, opts *dto.QueryOptions) reflect.Value {
	if opts == nil {
		return queryBuilder
	}

	if len(opts.Filters) > 0 {
		queryBuilder = applyFilters(queryBuilder, &opts.Filters)
	}

	if len(opts.Sort) > 0 {
		queryBuilder = applySorts(queryBuilder, &opts.Sort)
	}

	if opts.Pagination != nil {
		opts.Pagination.SetDefaults()
		queryBuilder = applyPagination(queryBuilder, opts.Pagination, &opts.Sort)
	}

	return queryBuilder
}

func applyPagination(queryBuilder reflect.Value, p *dto.PaginationOptions, sorts *[]dto.SortOption) reflect.Value {
	limit := p.PageSize

	if p.Cursor != nil && p.Cursor != "" {
		whereMethod := queryBuilder.MethodByName(MethodWhere)
		if whereMethod.IsValid() {
			predType := whereMethod.Type().In(0).Elem()

			hook := func(args []reflect.Value) []reflect.Value {
				s := args[0].Interface().(*sql.Selector)

				// Determine sort direction
				isAsc := false
				if sorts != nil {
					for _, sort := range *sorts {
						if sort.Key == "id" || sort.Key == "ID" || sort.Key == "_id" { // Check primary sort key
							if sort.Order == 1 {
								isAsc = true
							}
							break
						}
					}
				}

				if isAsc {
					s.Where(sql.GT(FieldID, p.Cursor))
				} else {
					s.Where(sql.LT(FieldID, p.Cursor)) // Default DESC
				}

				return nil
			}

			fn := reflect.MakeFunc(predType, hook)
			queryBuilder = whereMethod.Call([]reflect.Value{fn})[0]
		}
	} else {
		offset := (p.Page - 1) * limit
		queryBuilder = queryBuilder.MethodByName(MethodOffset).Call([]reflect.Value{reflect.ValueOf(offset)})[0]
	}

	queryBuilder = queryBuilder.MethodByName(MethodLimit).Call([]reflect.Value{reflect.ValueOf(limit)})[0]
	return queryBuilder
}

func applyFilters(queryBuilder reflect.Value, filters *[]dto.SearchFilter) reflect.Value {
	whereMethod := queryBuilder.MethodByName(MethodWhere)
	if !whereMethod.IsValid() {
		return queryBuilder
	}

	predType := whereMethod.Type().In(0).Elem()

	hook := func(args []reflect.Value) []reflect.Value {
		s := args[0].Interface().(*sql.Selector)

		for _, f := range *filters {
			if f.Key == "" || f.Value == nil {
				continue
			}

			switch f.Type {
			case "exact":
				s.Where(sql.EQ(f.Key, f.Value))
			case "contains":
				valStr := fmt.Sprintf("%v", f.Value)
				s.Where(sql.Contains(f.Key, valStr))
			case "prefix":
				valStr := fmt.Sprintf("%v", f.Value)
				s.Where(sql.HasPrefix(f.Key, valStr))
			case "gt":
				s.Where(sql.GT(f.Key, f.Value))
			case "lt":
				s.Where(sql.LT(f.Key, f.Value))
			case "gte":
				s.Where(sql.GTE(f.Key, f.Value))
			case "lte":
				s.Where(sql.LTE(f.Key, f.Value))
			default:
				s.Where(sql.EQ(f.Key, f.Value))
			}
		}
		return nil
	}

	fn := reflect.MakeFunc(predType, hook)
	return whereMethod.Call([]reflect.Value{fn})[0]
}

func applySorts(queryBuilder reflect.Value, sorts *[]dto.SortOption) reflect.Value {
	orderMethod := queryBuilder.MethodByName(MethodOrder)
	if !orderMethod.IsValid() {
		return queryBuilder
	}

	orderType := orderMethod.Type().In(0).Elem()

	hook := func(args []reflect.Value) []reflect.Value {
		s := args[0].Interface().(*sql.Selector)

		for _, sortOption := range *sorts {
			key := sortOption.Key
			if key == "" {
				continue
			}

			if sortOption.Order == -1 {
				s.OrderBy(sql.Desc(key))
			} else {
				s.OrderBy(sql.Asc(key))
			}
		}
		return nil
	}

	fn := reflect.MakeFunc(orderType, hook)
	return orderMethod.Call([]reflect.Value{fn})[0]
}
