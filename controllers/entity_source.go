package controllers

import (
	"fmt"
	"strings"

	"github.com/operator-framework/deppy/pkg/deppy"
	"github.com/operator-framework/deppy/pkg/deppy/input"
	"github.com/perdasilva/resolvulator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
)

const (
	EntityPropertyStatus                 = "entity.property.status"
	EntityPropertyInstalled              = "entity.property.installed"
	EntityPropertyDependencies           = "entity.property.dependencies"
	EntityPropertyConflicts              = "entity.property.conflicts"
	EntityPropertyLocalResolutionFailed  = "entity.property.localResolutionSucceeded"
	EntityPropertyGlobalResolutionFailed = "entity.property.globalResolutionFailed"
)

type EntitySource struct {
	*input.CacheEntitySource
}

func NewEntitySource(things []v1alpha1.Thing) *EntitySource {
	entityCache := map[deppy.Identifier]input.Entity{}
	for _, thing := range things {
		id := deppy.Identifier(thing.GetName())
		status := thing.Status.Phase
		dependencies := strings.Join(thing.Spec.Dependencies, ",")
		conflicts := strings.Join(thing.Spec.Conflicts, ",")
		isInstalled := meta.IsStatusConditionTrue(thing.Status.Conditions, v1alpha1.ConditionInstalled)
		localResolutionFailed := meta.IsStatusConditionFalse(thing.Status.Conditions, v1alpha1.ConditionLocalResolutionSucceeded)
		globalResolutionFailed := meta.IsStatusConditionFalse(thing.Status.Conditions, v1alpha1.ConditionGlobalResolutionSucceeded)
		entity := input.NewEntity(id, map[string]string{
			EntityPropertyStatus:                 status,
			EntityPropertyDependencies:           dependencies,
			EntityPropertyConflicts:              conflicts,
			EntityPropertyInstalled:              fmt.Sprintf("%t", isInstalled),
			EntityPropertyLocalResolutionFailed:  fmt.Sprintf("%t", localResolutionFailed),
			EntityPropertyGlobalResolutionFailed: fmt.Sprintf("%t", globalResolutionFailed),
		})
		entityCache[id] = *entity
	}
	return &EntitySource{
		CacheEntitySource: input.NewCacheQuerier(entityCache),
	}
}
