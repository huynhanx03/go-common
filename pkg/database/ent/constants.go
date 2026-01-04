package ent

const (
	// Driver names
	DriverMySQL    = "mysql"
	DriverPostgres = "postgres"

	// Ent Client Method names (Reflection)
	MethodCreate        = "Create"
	MethodUpdateOneID   = "UpdateOneID"
	MethodDeleteOneID   = "DeleteOneID"
	MethodGet           = "Get"
	MethodExec          = "Exec"
	MethodSave          = "Save"
	MethodQuery         = "Query"
	MethodLimit         = "Limit"
	MethodOffset        = "Offset"
	MethodCount         = "Count"
	MethodAll           = "All"
	MethodWhere         = "Where"
	MethodOrder         = "Order"
	MethodMapCreateBulk = "MapCreateBulk"

	// Ent Fields and Prefixes (Reflection)
	PrefixSet   = "Set"
	FieldID     = "ID"
	FieldEdges  = "Edges"
	FieldConfig = "config"
)
