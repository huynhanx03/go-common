package websocket

import (
	"context"
	"net/http"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

type PrincipalResolver func(
	context.Context,
	*http.Request,
) (authentication.Principal, error)

type TopicAuthorizer func(
	context.Context,
	authentication.Principal,
	string,
) error
