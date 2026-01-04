package ent

import (
	"github.com/huynhanx03/go-common/pkg/constraints"
	"github.com/huynhanx03/go-common/pkg/database"
)

// Model defines the interface for Ent models with ID access.
type Model[ID constraints.ID] interface {
	GetID() ID
	SetID(ID)
}

// Repository defines the contract for Ent repositories.
type Repository[T Model[ID], ID constraints.ID] interface {
	database.Repository[T, ID]
}
