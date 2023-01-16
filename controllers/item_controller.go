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

// ItemReconciler reconciles a Item object
type ItemReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	resolver *Resolver
}

//+kubebuilder:rbac:groups=resolvulator.resolvulator.io,resources=items,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=resolvulator.resolvulator.io,resources=items/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=resolvulator.resolvulator.io,resources=items/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Item object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.1/pkg/reconcile
func (r *ItemReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	item := &resolvulatorv1alpha1.Item{}
	if err := r.Client.Get(ctx, req.NamespacedName, item); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// item is being deleted
	if item.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(item, resolvulatorFinalizerName) {
			controllerutil.AddFinalizer(item, resolvulatorFinalizerName)
			if err := r.Update(ctx, item); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(item, resolvulatorFinalizerName) {
			r.resolver.Reset()
			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(item, resolvulatorFinalizerName)
			if err := r.Update(ctx, item); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// reconcile item
	switch item.Status.Phase {
	case resolvulatorv1alpha1.PhaseNewlyCreated:
		return ctrl.Result{}, r.updatePhase(ctx, item, resolvulatorv1alpha1.PhaseLocalResolution)
	case resolvulatorv1alpha1.PhaseLocalResolution:
		if meta.FindStatusCondition(item.Status.Conditions, resolvulatorv1alpha1.ConditionLocalResolutionSucceeded) == nil {
			err := r.resolver.RunLocalResolution(ctx, item)
			if err != nil {
				meta.SetStatusCondition(&item.Status.Conditions, v1.Condition{
					Type:               resolvulatorv1alpha1.ConditionLocalResolutionSucceeded,
					Status:             v1.ConditionFalse,
					ObservedGeneration: item.Generation,
					Reason:             "ResolutionFailure",
					Message:            err.Error(),
				})
			} else {
				meta.SetStatusCondition(&item.Status.Conditions, v1.Condition{
					Type:               resolvulatorv1alpha1.ConditionLocalResolutionSucceeded,
					Status:             v1.ConditionTrue,
					ObservedGeneration: item.Generation,
					Reason:             "ResolutionSuccess",
					Message:            "Local item resolution succeeded",
				})
				item.Status.Phase = resolvulatorv1alpha1.PhaseGlobalResolution
			}
			return ctrl.Result{}, r.Client.Status().Update(ctx, item)
		}
		return ctrl.Result{}, nil
	case resolvulatorv1alpha1.PhaseGlobalResolution:
		if meta.FindStatusCondition(item.Status.Conditions, resolvulatorv1alpha1.ConditionGlobalResolutionSucceeded) == nil {
			r.resolver.Enqueue()
		} else if meta.IsStatusConditionTrue(item.Status.Conditions, resolvulatorv1alpha1.ConditionGlobalResolutionSucceeded) {
			return ctrl.Result{}, r.updatePhase(ctx, item, resolvulatorv1alpha1.PhaseUnpacking)
		}
		return ctrl.Result{}, nil
	case resolvulatorv1alpha1.PhaseUnpacking:
		meta.SetStatusCondition(&item.Status.Conditions, v1.Condition{
			Type:               resolvulatorv1alpha1.ConditionUnpacked,
			Status:             v1.ConditionFalse,
			ObservedGeneration: item.Generation,
			Reason:             "BundleNotUnpacked",
			Message:            "Bundle is still unpacking",
		})
		return ctrl.Result{}, r.waitAndUpdatePhase(ctx, item, resolvulatorv1alpha1.PhaseInstalling)
	case resolvulatorv1alpha1.PhaseInstalling:
		meta.SetStatusCondition(&item.Status.Conditions, v1.Condition{
			Type:               resolvulatorv1alpha1.ConditionUnpacked,
			Status:             v1.ConditionTrue,
			ObservedGeneration: item.Generation,
			Reason:             "BundleUnpacked",
			Message:            "Bundle successfully unpacked",
		})
		meta.SetStatusCondition(&item.Status.Conditions, v1.Condition{
			Type:               resolvulatorv1alpha1.ConditionInstalled,
			Status:             v1.ConditionFalse,
			ObservedGeneration: item.Generation,
			Reason:             "BundleInstalling",
			Message:            "Bundle is installing...",
		})
		return ctrl.Result{}, r.waitAndUpdatePhase(ctx, item, resolvulatorv1alpha1.PhaseInstalled)
	case resolvulatorv1alpha1.PhaseInstalled:
		if !meta.IsStatusConditionTrue(item.Status.Conditions, resolvulatorv1alpha1.ConditionInstalled) {
			meta.SetStatusCondition(&item.Status.Conditions, v1.Condition{
				Type:               resolvulatorv1alpha1.ConditionInstalled,
				Status:             v1.ConditionTrue,
				ObservedGeneration: item.Generation,
				Reason:             "BundleInstalled",
				Message:            "Bundle successfully installed...",
			})
			if err := r.Client.Status().Update(ctx, item); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			r.resolver.Reset()
			return ctrl.Result{}, nil
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ItemReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.resolver = NewResolver(context.Background(), r.Client)

	return ctrl.NewControllerManagedBy(mgr).
		For(&resolvulatorv1alpha1.Item{}).
		Watches(&source.Channel{Source: r.resolver.ReconciliationChannel()}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}

func (r *ItemReconciler) waitAndUpdatePhase(ctx context.Context, item *resolvulatorv1alpha1.Item, phase string) error {
	time.Sleep(time.Duration(rand.Int63nRange(1, 3)) * time.Second)
	return r.updatePhase(ctx, item, phase)
}

func (r *ItemReconciler) updatePhase(ctx context.Context, item *resolvulatorv1alpha1.Item, phase string) error {
	itemCopy := item.DeepCopy()
	itemCopy.Status.Phase = phase
	return r.Client.Status().Update(ctx, itemCopy)
}
