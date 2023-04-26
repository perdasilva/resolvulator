/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"time"

	resolvulatorv1alpha1 "github.com/perdasilva/resolvulator/api/v1alpha1"
	"github.com/perdasilva/resolvulator/internal/resolver"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const resolvulatorFinalizerName = "resolvulator.io/finalizer"

// ThingReconciler reconciles a Thing object
type ThingReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Resolver resolver.MultiStageResolver
}

//+kubebuilder:rbac:groups=resolvulator.resolvulator.io,resources=things,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=resolvulator.resolvulator.io,resources=things/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=resolvulator.resolvulator.io,resources=things/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ThingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("starting thing reconciliation", "request", req)
	defer logger.Info("finished thing reconciliation", "request", req)

	// get thing
	thing := &resolvulatorv1alpha1.Thing{}
	if err := r.Client.Get(ctx, req.NamespacedName, thing); err != nil {
		return ctrl.Result{}, err
	}

	// get corresponding installed thing if it exists
	desiredInstalledThing := resolvulatorv1alpha1.InstalledThing{
		ObjectMeta: ctrl.ObjectMeta{
			Name: thing.Name,
		},
	}

	if err := controllerutil.SetOwnerReference(thing, &desiredInstalledThing, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	installedThing := &resolvulatorv1alpha1.InstalledThing{}
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(&desiredInstalledThing), installedThing)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	isInstalled := !errors.IsNotFound(err)

	// get resolver state for this thing
	resolution := r.Resolver.GetResolution(req.NamespacedName)

	// update thing conditions from resolver and installed thing
	meta.SetStatusCondition(&thing.Status.Conditions, resolution.Condition)
	if isInstalled {
		for _, condition := range installedThing.Status.Conditions {
			meta.SetStatusCondition(&thing.Status.Conditions, condition)
		}
	}
	if err := r.Client.Status().Update(ctx, thing); err != nil {
		return ctrl.Result{}, err
	}

	resolverConditionsMet := meta.IsStatusConditionTrue(thing.Status.Conditions, resolvulatorv1alpha1.ConditionGlobalResolutionSucceeded)

	if !(isInstalled) && resolverConditionsMet {
		return ctrl.Result{}, r.Client.Create(ctx, &desiredInstalledThing)
	}

	return ctrl.Result{Requeue: !resolverConditionsMet}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ThingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resolvulatorv1alpha1.Thing{}).
		Owns(&resolvulatorv1alpha1.InstalledThing{}).
		WithEventFilter(&predicate.GenerationChangedPredicate{}).
		Complete(r)
}

func (r *ThingReconciler) waitAndUpdatePhase(ctx context.Context, thing *resolvulatorv1alpha1.Thing, phase string) error {
	time.Sleep(time.Duration(rand.Int63nRange(1, 3)) * time.Second)
	return r.updatePhase(ctx, thing, phase)
}

func (r *ThingReconciler) updatePhase(ctx context.Context, thing *resolvulatorv1alpha1.Thing, phase string) error {
	thingCopy := thing.DeepCopy()
	thingCopy.Status.Phase = phase
	return r.Client.Status().Update(ctx, thingCopy)
}
