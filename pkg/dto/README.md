# Pagination

Pagination splits a large result set into smaller pages for list responses.
`PaginationOptions` describes the requested page, while `Paginated[T]` carries
the records and their pagination metadata. Cursor values are adapter-specific
and should be passed back unchanged.
