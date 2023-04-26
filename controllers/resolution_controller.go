package controllers

import (
	"context"

	resolvulatorv1alpha1 "github.com/perdasilva/resolvulator/api/v1alpha1"
	"github.com/perdasilva/resolvulator/internal/resolver"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ResolutionController consumes changes from Things and InstallThings to provide resolution guidance
type ResolutionController struct {
	client.Client
	Scheme   *runtime.Scheme
	Resolver *resolver.Resolver
}

func (r *ResolutionController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	thing := resolvulatorv1alpha1.Thing{}
	if err := r.Client.Get(ctx, req.NamespacedName, &thing); err != nil {
		return ctrl.Result{}, err
	}

	// resource is being deleted
	if !(thing.ObjectMeta.DeletionTimestamp.IsZero()) {
		r.Resolver.NotifyDeletion(&thing)
		return ctrl.Result{}, nil
	}

	// notify resolver of spec change to a thing
	r.Resolver.NotifySpecChange(&thing)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ResolutionController) SetupWithManager(mgr ctrl.Manager) error {

	myPredicate := func() predicate.Predicate {
		return predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				// Reconcile on status change
				return !e.ObjectOld.GetDeletionTimestamp().IsZero() || (e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration())
			},
			CreateFunc: func(e event.CreateEvent) bool {
				// Ignore create events
				return true
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				// Reconcile on deletion
				return true
			},
			GenericFunc: func(e event.GenericEvent) bool {
				// Ignore generic events
				return false
			},
		}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&resolvulatorv1alpha1.Thing{}).
		// only care about spec changes and deletions
		WithEventFilter(myPredicate()).
		Complete(r)
}
