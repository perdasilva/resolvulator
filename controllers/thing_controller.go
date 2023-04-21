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
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const resolvulatorFinalizerName = "resolvulator.io/finalizer"

// ThingReconciler reconciles a Thing object
type ThingReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	resolver *Resolver
}

//+kubebuilder:rbac:groups=resolvulator.resolvulator.io,resources=things,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=resolvulator.resolvulator.io,resources=things/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=resolvulator.resolvulator.io,resources=things/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Thing object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.1/pkg/reconcile
func (r *ThingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	thing := &resolvulatorv1alpha1.Thing{}
	if err := r.Client.Get(ctx, req.NamespacedName, thing); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// thing is being deleted
	if thing.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(thing, resolvulatorFinalizerName) {
			controllerutil.AddFinalizer(thing, resolvulatorFinalizerName)
			if err := r.Update(ctx, thing); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(thing, resolvulatorFinalizerName) {
			r.resolver.Reset()
			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(thing, resolvulatorFinalizerName)
			if err := r.Update(ctx, thing); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// reconcile thing
	switch thing.Status.Phase {
	case resolvulatorv1alpha1.PhaseNewlyCreated:
		return ctrl.Result{}, r.updatePhase(ctx, thing, resolvulatorv1alpha1.PhaseLocalResolution)
	case resolvulatorv1alpha1.PhaseLocalResolution:
		if meta.FindStatusCondition(thing.Status.Conditions, resolvulatorv1alpha1.ConditionLocalResolutionSucceeded) == nil {
			err := r.resolver.RunLocalResolution(ctx, thing)
			if err != nil {
				meta.SetStatusCondition(&thing.Status.Conditions, v1.Condition{
					Type:               resolvulatorv1alpha1.ConditionLocalResolutionSucceeded,
					Status:             v1.ConditionFalse,
					ObservedGeneration: thing.Generation,
					Reason:             "ResolutionFailure",
					Message:            err.Error(),
				})
			} else {
				meta.SetStatusCondition(&thing.Status.Conditions, v1.Condition{
					Type:               resolvulatorv1alpha1.ConditionLocalResolutionSucceeded,
					Status:             v1.ConditionTrue,
					ObservedGeneration: thing.Generation,
					Reason:             "ResolutionSuccess",
					Message:            "Local thing resolution succeeded",
				})
				thing.Status.Phase = resolvulatorv1alpha1.PhaseGlobalResolution
			}
			return ctrl.Result{}, r.Client.Status().Update(ctx, thing)
		}
		return ctrl.Result{}, nil
	case resolvulatorv1alpha1.PhaseGlobalResolution:
		if meta.FindStatusCondition(thing.Status.Conditions, resolvulatorv1alpha1.ConditionGlobalResolutionSucceeded) == nil {
			r.resolver.Enqueue()
		} else if meta.IsStatusConditionTrue(thing.Status.Conditions, resolvulatorv1alpha1.ConditionGlobalResolutionSucceeded) {
			return ctrl.Result{}, r.updatePhase(ctx, thing, resolvulatorv1alpha1.PhaseUnpacking)
		}
		return ctrl.Result{}, nil
	case resolvulatorv1alpha1.PhaseUnpacking:
		meta.SetStatusCondition(&thing.Status.Conditions, v1.Condition{
			Type:               resolvulatorv1alpha1.ConditionUnpacked,
			Status:             v1.ConditionFalse,
			ObservedGeneration: thing.Generation,
			Reason:             "BundleNotUnpacked",
			Message:            "Bundle is still unpacking",
		})
		return ctrl.Result{}, r.waitAndUpdatePhase(ctx, thing, resolvulatorv1alpha1.PhaseInstalling)
	case resolvulatorv1alpha1.PhaseInstalling:
		meta.SetStatusCondition(&thing.Status.Conditions, v1.Condition{
			Type:               resolvulatorv1alpha1.ConditionUnpacked,
			Status:             v1.ConditionTrue,
			ObservedGeneration: thing.Generation,
			Reason:             "BundleUnpacked",
			Message:            "Bundle successfully unpacked",
		})
		meta.SetStatusCondition(&thing.Status.Conditions, v1.Condition{
			Type:               resolvulatorv1alpha1.ConditionInstalled,
			Status:             v1.ConditionFalse,
			ObservedGeneration: thing.Generation,
			Reason:             "BundleInstalling",
			Message:            "Bundle is installing...",
		})
		return ctrl.Result{}, r.waitAndUpdatePhase(ctx, thing, resolvulatorv1alpha1.PhaseInstalled)
	case resolvulatorv1alpha1.PhaseInstalled:
		if !meta.IsStatusConditionTrue(thing.Status.Conditions, resolvulatorv1alpha1.ConditionInstalled) {
			meta.SetStatusCondition(&thing.Status.Conditions, v1.Condition{
				Type:               resolvulatorv1alpha1.ConditionInstalled,
				Status:             v1.ConditionTrue,
				ObservedGeneration: thing.Generation,
				Reason:             "BundleInstalled",
				Message:            "Bundle successfully installed...",
			})
			if err := r.Client.Status().Update(ctx, thing); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			r.resolver.Reset()
			return ctrl.Result{}, nil
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ThingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.resolver = NewResolver(context.Background(), r.Client)

	return ctrl.NewControllerManagedBy(mgr).
		For(&resolvulatorv1alpha1.Thing{}).
		Watches(&source.Channel{Source: r.resolver.ReconciliationChannel()}, &handler.EnqueueRequestForObject{}).
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
