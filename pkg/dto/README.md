# Data Transfer Object

## Pagination Guide

Simple guide for using Cursor Pagination in this library.

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

### 2. Why Cursor = Last ID?

*   **Fast**: Database jumps directly to the record (using Index) instead of counting rows like Offset pagination.
*   **Stable**: No duplicate or missing items if data is added/deleted while you are scrolling.

### 3. How Backend Handles It

*   **Sort DESC (-1)**: Backend finds items with `ID < 1050` (Older items).
*   **Sort ASC (1)**: Backend finds items with `ID > 1050` (Newer items).
