package dto

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
)

func TestPaginationValidateAndOffset(t *testing.T) {
	t.Parallel()

	options := PaginationOptions{Page: 3, PageSize: 25}
	if err := options.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	offset, err := options.Offset()
	if err != nil || offset != 50 {
		t.Fatalf("Offset = %d, %v", offset, err)
	}
	if offset, err := (PaginationOptions{Page: 0, PageSize: 10}).Offset(); !errors.Is(err, ErrInvalidPagination) || offset != 0 {
		t.Fatalf("invalid Offset = %d, %v; want 0 and ErrInvalidPagination", offset, err)
	}
	for _, invalid := range []PaginationOptions{
		{Page: 0, PageSize: 10},
		{Page: 1, PageSize: 0},
		{Page: math.MaxInt, PageSize: 100},
	} {
		if !errors.Is(invalid.Validate(), ErrInvalidPagination) {
			t.Fatalf("Validate(%+v) accepted invalid pagination", invalid)
		}
	}
}

func TestPaginationSetDefaults(t *testing.T) {
	t.Parallel()

	var nilOptions *PaginationOptions
	nilOptions.SetDefaults()

	options := PaginationOptions{Page: -1, PageSize: 0}
	options.SetDefaults()
	if options.Page != 1 || options.PageSize != DefaultPageSize {
		t.Fatalf("defaults = %+v, want page 1 and page size %d", options, DefaultPageSize)
	}

	options = PaginationOptions{Page: 3, PageSize: 25}
	options.SetDefaults()
	if options.Page != 3 || options.PageSize != 25 {
		t.Fatalf("SetDefaults changed valid values: %+v", options)
	}
}

func TestCalculatePaginationCheckedHandlesEmptyAndInvalidInputs(t *testing.T) {
	t.Parallel()

	meta, err := CalculatePaginationChecked(1, 10, 0)
	if err != nil {
		t.Fatalf("empty pagination: %v", err)
	}
	if meta.CurrentPage != 1 || meta.TotalPages != 1 || meta.HasNext || meta.HasPrev {
		t.Fatalf("empty metadata = %+v", meta)
	}
	if _, err := CalculatePaginationChecked(1, 10, -1); !errors.Is(
		err,
		ErrInvalidPagination,
	) {
		t.Fatalf("negative total error = %v", err)
	}

	for _, input := range []struct {
		page, pageSize int
		total          int64
	}{
		{page: 0, pageSize: 10, total: 1},
		{page: 1, pageSize: 0, total: 1},
	} {
		if _, err := CalculatePaginationChecked(input.page, input.pageSize, input.total); !errors.Is(err, ErrInvalidPagination) {
			t.Errorf("CalculatePaginationChecked(%d, %d, %d) error = %v", input.page, input.pageSize, input.total, err)
		}
	}

	meta, err = CalculatePaginationChecked(2, 10, 21)
	if err != nil || meta.TotalPages != 3 || meta.CurrentPage != 2 || !meta.HasNext || !meta.HasPrev {
		t.Fatalf("middle page metadata = %+v, error = %v", meta, err)
	}
	meta, err = CalculatePaginationChecked(9, 10, 21)
	if err != nil || meta.CurrentPage != 3 || meta.HasNext || !meta.HasPrev {
		t.Fatalf("clamped page metadata = %+v, error = %v", meta, err)
	}
	meta, err = CalculatePaginationChecked(1, 10, 20)
	if err != nil || meta.TotalPages != 2 || !meta.HasNext || meta.HasPrev {
		t.Fatalf("exact page boundary metadata = %+v, error = %v", meta, err)
	}
	if _, err := calculatePaginationChecked(1, 1, 6, 5); !errors.Is(err, ErrInvalidPagination) {
		t.Fatalf("pagination count exceeding int range error = %v", err)
	}
}

func TestCalculatePaginationUsesCheckedResultOrFallback(t *testing.T) {
	t.Parallel()

	if got := CalculatePagination(2, 10, 21); got.CurrentPage != 2 || got.TotalPages != 3 || !got.HasNext || !got.HasPrev {
		t.Fatalf("valid legacy metadata = %+v", got)
	}
	if got := CalculatePagination(0, 0, -1); got.CurrentPage != 1 || got.PageSize != DefaultPageSize || got.TotalPages != 1 {
		t.Fatalf("invalid legacy fallback = %+v", got)
	}
}

func TestMapPaginatedPreservesPaginationAndNilState(t *testing.T) {
	t.Parallel()

	meta := &PaginationMeta{CurrentPage: 2, PageSize: 2, TotalPages: 4, TotalItems: 7}
	page := &Paginated[int]{Records: &[]int{3, 5}, Pagination: meta}
	mapped := MapPaginated(page, func(value int) string { return fmt.Sprint(value) })
	if mapped.Pagination != meta {
		t.Fatalf("mapped metadata = %+v; want original metadata pointer", mapped.Pagination)
	}
	if mapped.Records == nil || !reflect.DeepEqual(*mapped.Records, []string{"3", "5"}) {
		t.Fatalf("mapped records = %v, want [3 5]", mapped.Records)
	}

	emptyRecords := []int{}
	withEmptySlice := MapPaginated(&Paginated[int]{Records: &emptyRecords}, func(value int) string { return fmt.Sprint(value) })
	if withEmptySlice.Records == nil || *withEmptySlice.Records == nil || len(*withEmptySlice.Records) != 0 {
		t.Fatalf("mapping empty record slice should preserve initialized empty slice: %+v", withEmptySlice)
	}
}
