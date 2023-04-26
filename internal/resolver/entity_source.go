package resolver

import (
	"fmt"
	"strings"

	"github.com/operator-framework/deppy/pkg/deppy"
	"github.com/operator-framework/deppy/pkg/deppy/input"
	"github.com/perdasilva/resolvulator/api/v1alpha1"
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

func NewEntitySource(things []v1alpha1.Thing, installedThings []v1alpha1.InstalledThing) *EntitySource {
	entityCache := map[deppy.Identifier]input.Entity{}

	// collect installed packages
	installedPkgs := map[deppy.Identifier]struct{}{}
	for _, installedThing := range installedThings {
		installedPkgs[deppy.Identifier(installedThing.GetName())] = struct{}{}
	}

	for _, thing := range things {
		id := deppy.Identifier(thing.GetName())
		dependencies := strings.Join(thing.Spec.Dependencies, ",")
		conflicts := strings.Join(thing.Spec.Conflicts, ",")
		_, isInstalled := installedPkgs[id]
		entity := input.NewEntity(id, map[string]string{
			EntityPropertyDependencies: dependencies,
			EntityPropertyConflicts:    conflicts,
			EntityPropertyInstalled:    fmt.Sprintf("%t", isInstalled),
		})
		entityCache[id] = *entity
	}
	return &EntitySource{
		CacheEntitySource: input.NewCacheQuerier(entityCache),
	}
}
