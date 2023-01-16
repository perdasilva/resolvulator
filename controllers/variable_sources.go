package controllers

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

var _ input.VariableSource = &ItemVariableSource{}

const (
	VariablePrefixRequired  = "required"
	VariablePrefixInstalled = "installed"
	VariablePrefixSeparator = "/"
)

type ItemVariableSource struct {
	resolutionScope map[deppy.Identifier]struct{}
}

func NewItemVariableSource(items ...v1alpha1.Item) *ItemVariableSource {
	resolutionScope := map[deppy.Identifier]struct{}{}
	for _, item := range items {
		resolutionScope[deppy.IdentifierFromString(item.GetName())] = struct{}{}
	}
	return &ItemVariableSource{
		resolutionScope: resolutionScope,
	}
}

func (i *ItemVariableSource) GetVariables(ctx context.Context, entitySource input.EntitySource) ([]deppy.Variable, error) {
	var variables []deppy.Variable
	installedVariables := map[deppy.Identifier]*struct{}{}
	err := entitySource.Iterate(ctx, func(entity *input.Entity) error {
		dependencies := strings.Split(entity.Properties[EntityPropertyDependencies], ",")
		installed, err := strconv.ParseBool(entity.Properties[EntityPropertyInstalled])
		if err != nil {
			return fmt.Errorf("error parsing entity property (%s) to boolean: %s", EntityPropertyInstalled, err)
		}

		// remove uninstalled items outside the resolution scope
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
			variableID = deppy.IdentifierFromString(fmt.Sprintf("%s%s%s", VariablePrefixInstalled, VariablePrefixSeparator, entity.ID))
			installedVariables[variableID] = &struct{}{}
		} else {
			variableID = deppy.IdentifierFromString(fmt.Sprintf("%s%s%s", VariablePrefixRequired, VariablePrefixSeparator, entity.ID))
		}

		variable := input.NewSimpleVariable(variableID)
		if !localResolutionFailed || !globalResolutionFailed {
			variable.AddConstraint(constraint.Mandatory())
		} else {
			variable.AddConstraint(constraint.Prohibited())
		}
		for _, dependency := range dependencies {
			dependency := strings.TrimSpace(dependency)
			if dependency == "" {
				continue
			}
			dependencyID := deppy.IdentifierFromString(fmt.Sprintf("installed/%s", dependency))
			if _, ok := installedVariables[dependencyID]; !ok {
				installedVariables[dependencyID] = nil
			}
			variable.AddConstraint(constraint.Dependency(dependencyID))
		}
		variables = append(variables, variable)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for variableID, installedVariable := range installedVariables {
		if installedVariable == nil {
			variables = append(variables, input.NewSimpleVariable(variableID, constraint.Prohibited()))
		}
	}
	return variables, nil
}
