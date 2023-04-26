package controllers

import (
	"context"

	resolvulatorv1alpha1 "github.com/perdasilva/resolvulator/api/v1alpha1"
	"github.com/perdasilva/resolvulator/internal/resolver"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const resolutionControllerFinalizer = "finalizer.resolvulator.resolvulator.io"

// ResolutionController consumes changes from Things and InstallThings to provide resolution guidance
type ResolutionController struct {
	client.Client
	Scheme   *runtime.Scheme
	Resolver *resolver.Resolver
}

func (r *ResolutionController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	thing := resolvulatorv1alpha1.Thing{}
	if err := r.Client.Get(ctx, req.NamespacedName, &thing); client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}

	if !(thing.ObjectMeta.DeletionTimestamp.IsZero()) {
		// notifiy resolver of resource deletion
		r.Resolver.NotifyDeletion(&thing)
		return ctrl.Result{}, nil
	}

	// notify resolver of spec change to a thing
	r.Resolver.NotifySpecChange(&thing)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ResolutionController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resolvulatorv1alpha1.Thing{}).
		Owns(&resolvulatorv1alpha1.InstalledThing{}).
		// only care about spec changes and deletions
		// WithEventFilter(myPredicate()).
		WithEventFilter(&predicate.GenerationChangedPredicate{}).
		// kick off resolution when an installed thing is created
		Watches(&source.Kind{Type: &resolvulatorv1alpha1.InstalledThing{}}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}
