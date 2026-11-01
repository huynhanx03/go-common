package casbin

import (
	"context"
	"strconv"
	"sync"

	"github.com/casbin/casbin/v3/rbac"
	"github.com/huynhanx03/go-common/pkg/security/authorization"
)

type relationKey struct {
	subject string
	domain  string
}

// relationRoleManager keeps request relations separate from the immutable role
// graph loaded into Casbin. The request overlay exists only for one evaluation.
type relationRoleManager struct {
	rbac.RoleManager
	mu        sync.RWMutex
	relations map[relationKey]map[string]struct{}
	subjects  map[string][]relationKey
}

func newRelationRoleManager(manager rbac.RoleManager) *relationRoleManager {
	if manager == nil {
		return nil
	}
	return &relationRoleManager{
		RoleManager: manager,
		relations:   make(map[relationKey]map[string]struct{}),
		subjects:    make(map[string][]relationKey),
	}
}

func (manager *relationRoleManager) Clear() error {
	manager.mu.Lock()
	clear(manager.relations)
	clear(manager.subjects)
	manager.mu.Unlock()
	return manager.RoleManager.Clear()
}

func (manager *relationRoleManager) HasLink(
	subject string,
	role string,
	domains ...string,
) (bool, error) {
	linked, err := manager.RoleManager.HasLink(subject, role, domains...)
	if linked || err != nil {
		return linked, err
	}
	key := relationKey{subject: subject, domain: relationDomain(domains)}
	manager.mu.RLock()
	roles := manager.relations[key]
	_, direct := roles[role]
	manager.mu.RUnlock()
	if direct {
		return true, nil
	}
	for directRole := range roles {
		linked, err := manager.RoleManager.HasLink(directRole, role, domains...)
		if linked || err != nil {
			return linked, err
		}
	}
	return false, nil
}

func (manager *relationRoleManager) bind(subject string, relations []PolicyRule) {
	if manager == nil {
		return
	}
	grouped := make(map[relationKey]map[string]struct{}, len(relations))
	for _, relation := range relations {
		key := relationKey{subject: subject, domain: relation.V2}
		if grouped[key] == nil {
			grouped[key] = make(map[string]struct{})
		}
		grouped[key][relation.V1] = struct{}{}
	}
	manager.mu.Lock()
	for key, roles := range grouped {
		manager.relations[key] = roles
		manager.subjects[subject] = append(manager.subjects[subject], key)
	}
	manager.mu.Unlock()
}

func (manager *relationRoleManager) unbind(subject string) {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	for _, key := range manager.subjects[subject] {
		delete(manager.relations, key)
	}
	delete(manager.subjects, subject)
	manager.mu.Unlock()
}

func relationDomain(domains []string) string {
	if len(domains) == 0 {
		return ""
	}
	return domains[0]
}

func (r *Runtime) AuthorizeWithRelations(
	ctx context.Context,
	request authorization.Request,
	relations []PolicyRule,
) (authorization.Decision, error) {
	if r == nil {
		return r.Authorize(ctx, request)
	}
	if err := request.Validate(r.options.clock().UTC()); err != nil {
		return denied(authorization.ReasonInvalidRequest, r.AppliedRevision()), authorization.ErrInvalidRequest
	}
	if !request.Principal.Authenticated {
		return r.Authorize(ctx, request)
	}
	if len(relations) == 0 {
		return r.authorize(ctx, request, dynamicSubjectPrefix+strconv.FormatUint(r.subjectSequence.Add(1), 10))
	}
	live := r.current()
	if live == nil || live.groupRelations == nil {
		return denied(authorization.ReasonEvaluationError, r.AppliedRevision()), ErrInvalidRelations
	}
	originalSubject := request.Principal.Subject
	requestSubject := dynamicSubjectPrefix + strconv.FormatUint(r.subjectSequence.Add(1), 10)
	groupRelations := make([]PolicyRule, 0, len(relations))
	resourceRelations := make([]PolicyRule, 0, len(relations))
	for _, relation := range relations {
		if relation.V0 != originalSubject || relation.V1 == "" ||
			validatePolicyValue(relation.V1, defaultMaxValueBytes) != nil ||
			relation.V3 != "" || relation.V4 != "" || relation.V5 != "" {
			return denied(authorization.ReasonInvalidRequest, live.revision), ErrInvalidRelations
		}
		switch relation.Type {
		case policyTypeGrouping:
			if relation.V2 != request.Scope.Key() && relation.V2 != SystemScope().Key() {
				return denied(authorization.ReasonInvalidRequest, live.revision), ErrInvalidRelations
			}
			groupRelations = append(groupRelations, PolicyRule{Type: policyTypeGrouping, V0: requestSubject, V1: relation.V1, V2: relation.V2})
		case policyTypeResource:
			if request.ResourceID == "" || relation.V2 != request.ResourceID || live.resourceRelations == nil {
				return denied(authorization.ReasonInvalidRequest, live.revision), ErrInvalidRelations
			}
			resourceRelations = append(resourceRelations, PolicyRule{Type: policyTypeResource, V0: requestSubject, V1: relation.V1, V2: relation.V2})
		default:
			return denied(authorization.ReasonInvalidRequest, live.revision), ErrInvalidRelations
		}
	}
	live.groupRelations.bind(requestSubject, groupRelations)
	if live.resourceRelations != nil {
		live.resourceRelations.bind(requestSubject, resourceRelations)
	}
	defer live.groupRelations.unbind(requestSubject)
	if live.resourceRelations != nil {
		defer live.resourceRelations.unbind(requestSubject)
	}
	return r.authorize(ctx, request, requestSubject)
}

var _ rbac.RoleManager = (*relationRoleManager)(nil)
