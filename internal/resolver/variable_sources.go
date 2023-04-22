package resolver

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/operator-framework/deppy/pkg/deppy"
	"github.com/operator-framework/deppy/pkg/deppy/constraint"
	"github.com/operator-framework/deppy/pkg/deppy/input"
	"github.com/perdasilva/resolvulator/api/v1alpha1"
)

var _ input.VariableSource = &ThingVariableSource{}

const (
	VariablePrefixRequired  = "required"
	VariablePrefixInstalled = "installed"
	VariablePrefixSeparator = "/"
)

type ThingVariableSource struct {
	resolutionScope map[deppy.Identifier]struct{}
}

func NewThingVariableSource(things ...v1alpha1.Thing) *ThingVariableSource {
	resolutionScope := map[deppy.Identifier]struct{}{}
	for _, thing := range things {
		resolutionScope[deppy.IdentifierFromString(thing.GetName())] = struct{}{}
	}
	return &ThingVariableSource{
		resolutionScope: resolutionScope,
	}
}

func (i *ThingVariableSource) GetVariables(ctx context.Context, entitySource input.EntitySource) ([]deppy.Variable, error) {
	var variables []deppy.Variable
	createdVariables := map[deppy.Identifier]*struct{}{}
	err := entitySource.Iterate(ctx, func(entity *input.Entity) error {
		installed, err := strconv.ParseBool(entity.Properties[EntityPropertyInstalled])
		if err != nil {
			return fmt.Errorf("error parsing entity property (%s) to boolean: %s", EntityPropertyInstalled, err)
		}

		// remove uninstalled things outside the resolution scope
		if _, ok := i.resolutionScope[entity.ID]; !ok && !installed {
			return nil
		}

		localResolutionFailed, err := strconv.ParseBool(entity.Properties[EntityPropertyLocalResolutionFailed])
		if err != nil {
			return fmt.Errorf("error parsing entity property (%s) to boolean: %s", EntityPropertyLocalResolutionFailed, err)
		}
		globalResolutionFailed, err := strconv.ParseBool(entity.Properties[EntityPropertyGlobalResolutionFailed])
		if err != nil {
			return fmt.Errorf("error parsing entity property (%s) to boolean: %s", EntityPropertyGlobalResolutionFailed, err)
		}

		var variableID deppy.Identifier
		if installed {
			variableID = installedVariableID(entity.ID)
		} else {
			variableID = requiredVariableID(entity.ID)
		}
		createdVariables[variableID] = &struct{}{}

		variable := input.NewSimpleVariable(variableID)
		if !localResolutionFailed && !globalResolutionFailed {
			variable.AddConstraint(constraint.Mandatory())
		} else {
			variable.AddConstraint(constraint.Prohibited())
		}

		// add dependency constraints
		dependencies := strings.Split(entity.Properties[EntityPropertyDependencies], ",")
		for _, dependency := range dependencies {
			dependency := deppy.IdentifierFromString(strings.TrimSpace(dependency))
			if dependency == "" {
				continue
			}
			dependencyID := installedVariableID(dependency)
			if _, ok := createdVariables[dependencyID]; !ok {
				createdVariables[dependencyID] = nil
			}
			variable.AddConstraint(constraint.Dependency(dependencyID))
		}

		// add conflict constraints
		conflicts := strings.Split(entity.Properties[EntityPropertyConflicts], ",")
		for _, conflict := range conflicts {
			conflict := deppy.IdentifierFromString(strings.TrimSpace(conflict))
			if conflict == "" {
				continue
			}
			installedVarID := installedVariableID(conflict)
			if _, ok := createdVariables[installedVarID]; !ok {
				createdVariables[installedVarID] = nil
			}
			requiredVarID := requiredVariableID(conflict)
			if _, ok := createdVariables[requiredVarID]; !ok {
				createdVariables[requiredVarID] = nil
			}
			variable.AddConstraint(constraint.Conflict(installedVarID))
			variable.AddConstraint(constraint.Conflict(requiredVarID))
		}

		variables = append(variables, variable)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for variableID, variable := range createdVariables {
		if variable == nil {
			variables = append(variables, input.NewSimpleVariable(variableID))
		}
	}
	return variables, nil
}

func installedVariableID(entityID deppy.Identifier) deppy.Identifier {
	return deppy.IdentifierFromString(fmt.Sprintf("%s%s%s", VariablePrefixInstalled, VariablePrefixSeparator, entityID))
}

func requiredVariableID(entityID deppy.Identifier) deppy.Identifier {
	return deppy.IdentifierFromString(fmt.Sprintf("%s%s%s", VariablePrefixRequired, VariablePrefixSeparator, entityID))
}
