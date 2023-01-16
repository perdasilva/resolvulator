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
	EntityPropertyLocalResolutionFailed  = "entity.property.localResolutionSucceeded"
	EntityPropertyGlobalResolutionFailed = "entity.property.globalResolutionFailed"
)

type EntitySource struct {
	*input.CacheEntitySource
}

func NewEntitySource(items []v1alpha1.Item) *EntitySource {
	entityCache := map[deppy.Identifier]input.Entity{}
	for _, item := range items {
		itemID := deppy.Identifier(item.GetName())
		itemStatus := item.Status.Phase
		itemDependencies := strings.Join(item.Spec.Dependencies, ",")
		itemInstalled := meta.IsStatusConditionTrue(item.Status.Conditions, v1alpha1.ConditionInstalled)
		localResolutionFailed := meta.IsStatusConditionFalse(item.Status.Conditions, v1alpha1.ConditionLocalResolutionSucceeded)
		globalResolutionFailed := meta.IsStatusConditionFalse(item.Status.Conditions, v1alpha1.ConditionGlobalResolutionSucceeded)
		itemEntity := input.NewEntity(itemID, map[string]string{
			EntityPropertyStatus:                 itemStatus,
			EntityPropertyDependencies:           itemDependencies,
			EntityPropertyInstalled:              fmt.Sprintf("%t", itemInstalled),
			EntityPropertyLocalResolutionFailed:  fmt.Sprintf("%t", localResolutionFailed),
			EntityPropertyGlobalResolutionFailed: fmt.Sprintf("%t", globalResolutionFailed),
		})
		entityCache[itemID] = *itemEntity
	}
	return &EntitySource{
		CacheEntitySource: input.NewCacheQuerier(entityCache),
	}
}
