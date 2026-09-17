# Data Transfer Object

## Pagination Guide

Simple guide for using cursor pagination in this library. Cursor values are
adapter-specific and clients must treat them as opaque. Some relational
adapters use the last stable ID; wide-column adapters return a driver paging
token in `pagination.next_cursor`.

### 1. Request Format

To get the next page, just send the `id` of the last item you received.

**Scenario**: You have a list of users, sorted by ID descending (newest first).
The last user on current page has `id: 1050`.

**Request JSON**:

```json
{
  "pagination": {
    "page_size": 10,
    "cursor": 1050
  },
  "sort": [
    {
      "key": "id",
      "order": -1 
    }
  ]
}
```

### 2. Why do relational adapters use the last ID?

*   **Fast**: Database jumps directly to the record (using Index) instead of counting rows like Offset pagination.
*   **Stable**: No duplicate or missing items if data is added/deleted while you are scrolling.

### 3. How relational adapters handle it

*   **Sort DESC (-1)**: Backend finds items with `ID < 1050` (Older items).
*   **Sort ASC (1)**: Backend finds items with `ID > 1050` (Newer items).

Do not apply this rule to an opaque cursor returned by another adapter. Pass
that token back unchanged and never log or decode it in application code.
