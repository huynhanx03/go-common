package ent

// Driver names
const (
	DriverMySQL    = "mysql"
	DriverPostgres = "postgres"
)

// Audit column names shared by the mixins.
const (
	CreatedAtColumnName = "created_at"
	UpdatedAtColumnName = "updated_at"

	CreatedByColumnName = "created_by"
	UpdatedByColumnName = "updated_by"

	SoftDeleteAtColumnName = "deleted_at"
	SoftDeleteByColumnName = "deleted_by"
)
