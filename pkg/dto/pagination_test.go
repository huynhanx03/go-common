package dto

import (
	"errors"
	"math"
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
	for _, invalid := range []PaginationOptions{
		{Page: 0, PageSize: 10},
		{Page: 1, PageSize: 0},
		{Page: 1, PageSize: MaxPageSize + 1},
		{Page: math.MaxInt, PageSize: MaxPageSize},
	} {
		if !errors.Is(invalid.Validate(), ErrInvalidPagination) {
			t.Fatalf("Validate(%+v) accepted invalid pagination", invalid)
		}
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
}
