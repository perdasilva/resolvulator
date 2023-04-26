package resolver

import (
	"context"
	"fmt"
	"strings"

	"github.com/operator-framework/deppy/pkg/deppy"
	"github.com/operator-framework/deppy/pkg/deppy/constraint"
	"github.com/operator-framework/deppy/pkg/deppy/input"
	"github.com/perdasilva/resolvulator/api/v1alpha1"
)

var _ input.VariableSource = &RequiredThingVariableSource{}

type RequiredThingVariableSource struct {
	requiredThings []v1alpha1.Thing
}

func NewRequiredThingVariableSource(requiredThings ...v1alpha1.Thing) *RequiredThingVariableSource {
	return &RequiredThingVariableSource{
		requiredThings: requiredThings,
	}
}

func (r RequiredThingVariableSource) GetVariables(ctx context.Context, entitySource input.EntitySource) ([]deppy.Variable, error) {
	var variables []deppy.Variable
	var pkgQueue []deppy.Identifier
	reqThings := map[deppy.Identifier]struct{}{}
	for _, thing := range r.requiredThings {
		// add required thing constraint
		variables = append(variables, input.NewSimpleVariable(
			deppy.Identifier("required/"+thing.GetName()),
			constraint.Mandatory(),
			constraint.Dependency(deppy.Identifier(thing.GetName())),
		))
		pkgQueue = append(pkgQueue, deppy.Identifier(thing.GetName()))
		reqThings[deppy.Identifier(thing.GetName())] = struct{}{}
	}

	processed := map[deppy.Identifier]struct{}{}
	for len(pkgQueue) > 0 {
		var head deppy.Identifier
		head, pkgQueue = pkgQueue[0], pkgQueue[1:]
		if _, ok := processed[head]; ok {
			continue
		}
		entity, err := entitySource.Get(ctx, head)
		variable := input.NewSimpleVariable(entity.Identifier())
		if err != nil {
			variable.AddConstraint(
				constraint.NewUserFriendlyConstraint(
					constraint.Prohibited(),
					func(constraint deppy.Constraint, subject deppy.Identifier) string {
						return fmt.Sprintf("package %s is not available: %v", subject, err)
					},
				),
			)
		} else {
			for _, conflict := range strings.Split(entity.Properties[EntityPropertyConflicts], ",") {
				conflict = strings.TrimSpace(conflict)
				if conflict == "" {
					continue
				}
				variable.AddConstraint(constraint.Conflict(deppy.Identifier(conflict)))
				pkgQueue = append(pkgQueue, deppy.Identifier(conflict))
			}
			for _, dependency := range strings.Split(entity.Properties[EntityPropertyDependencies], ",") {
				dependency = strings.TrimSpace(dependency)
				if dependency == "" {
					continue
				}
				variable.AddConstraint(constraint.Dependency(deppy.Identifier(dependency)))
				pkgQueue = append(pkgQueue, deppy.Identifier(dependency))
			}
			if entity.Properties[EntityPropertyInstalled] == "true" {
				variable.AddConstraint(constraint.Mandatory())
			}
		}
		variables = append(variables, variable)
		processed[head] = struct{}{}
	}

	return variables, nil
}
