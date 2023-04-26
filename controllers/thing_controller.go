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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const requeueDelay = 3 * time.Second

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
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// add finalizer to handle resource deletion
	if thing.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(thing, resolutionControllerFinalizer) {
			controllerutil.AddFinalizer(thing, resolutionControllerFinalizer)
			if err := r.Update(ctx, thing); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(thing, resolutionControllerFinalizer) {
			// remove finalizer
			controllerutil.RemoveFinalizer(thing, resolutionControllerFinalizer)
			if err := r.Update(ctx, thing); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
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

	// if the resolver conditions are met and the installed thing does not exist, create it
	resolverConditionsMet := meta.IsStatusConditionTrue(thing.Status.Conditions, resolvulatorv1alpha1.ConditionGlobalResolutionSucceeded)
	if !(isInstalled) && resolverConditionsMet {
		return ctrl.Result{}, r.Client.Create(ctx, &desiredInstalledThing)
	}

	// if resolver conditions aren't met, requeue the event
	if !resolverConditionsMet {
		return ctrl.Result{RequeueAfter: requeueDelay}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ThingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resolvulatorv1alpha1.Thing{}).
		Owns(&resolvulatorv1alpha1.InstalledThing{}).
		// WithEventFilter(&predicate.GenerationChangedPredicate{}).
		Complete(r)
}
