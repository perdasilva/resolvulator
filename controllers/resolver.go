package controllers

import (
	"context"
	"strings"
	"time"

	"github.com/eapache/channels"
	"github.com/operator-framework/deppy/pkg/deppy"
	"github.com/operator-framework/deppy/pkg/deppy/solver"
	"github.com/perdasilva/resolvulator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

type Resolver struct {
	requestChannel        chan bool
	ctx                   context.Context
	consensusDuration     time.Duration
	client                client.Client
	resolutionQueue       *channels.RingChannel
	reconciliationChannel chan event.GenericEvent
}

func NewResolver(ctx context.Context, client client.Client) *Resolver {
	resolver := &Resolver{
		requestChannel:        make(chan bool),
		ctx:                   ctx,
		consensusDuration:     5 * time.Second,
		client:                client,
		resolutionQueue:       channels.NewRingChannel(100),
		reconciliationChannel: make(chan event.GenericEvent, 100),
	}
	resolver.watch()
	resolver.watchResolutionQueue()
	return resolver
}

func (r *Resolver) ReconciliationChannel() chan event.GenericEvent {
	return r.reconciliationChannel
}

func (r *Resolver) Enqueue() {
	select {
	case <-r.ctx.Done():
	case r.requestChannel <- false:
	}
}

func (r *Resolver) Reset() {
	select {
	case <-r.ctx.Done():
	case r.requestChannel <- true:
	}
}

func (r *Resolver) watch() {
	go func() {
		var lastRequestTime time.Time
		ticker := time.NewTicker(1 * time.Second)
		shouldReset := false
		for {
			select {
			case <-r.ctx.Done():
				return
			case reset, hasNext := <-r.requestChannel:
				if !hasNext {
					return
				}
				if reset {
					shouldReset = true
				}
				lastRequestTime = time.Now()
			case <-ticker.C:
				if !lastRequestTime.IsZero() && time.Since(lastRequestTime) >= r.consensusDuration {
					// enqueue resolution request
					r.resolutionQueue.In() <- shouldReset

					// reset input collection
					lastRequestTime = time.Time{}
					shouldReset = false
				}
			}
		}
	}()
}

func (r *Resolver) watchResolutionQueue() {
	go func() {
		for {
			select {
			case <-r.ctx.Done():
				return
			case reset, hasNext := <-r.resolutionQueue.Out():
				if !hasNext {
					return
				}
				switch rst := reset.(type) {
				case bool:
					r.resolve(rst)
				}
			}
		}
	}()
}

func (r *Resolver) resolve(reset bool) {
	itemList := v1alpha1.ItemList{}
	if err := r.client.List(r.ctx, &itemList); err != nil {
		// TODO (perdasilva): make this more robust as it could lead to dead cycles
		return
	}

	// move items to resolving state
	puntToNextCycle := false
	for _, item := range itemList.Items {
		if reset {
			if !meta.IsStatusConditionTrue(item.Status.Conditions, v1alpha1.ConditionGlobalResolutionSucceeded) {
				itemCopy := *item.DeepCopy()
				itemCopy.Status.Phase = v1alpha1.PhaseLocalResolution
				meta.RemoveStatusCondition(&itemCopy.Status.Conditions, v1alpha1.ConditionLocalResolutionSucceeded)
				meta.RemoveStatusCondition(&itemCopy.Status.Conditions, v1alpha1.ConditionGlobalResolutionSucceeded)
				if err := r.client.Status().Update(r.ctx, &itemCopy); err != nil {
					r.reconcile(&item)
				}
				puntToNextCycle = true
			}
		} else if meta.FindStatusCondition(item.Status.Conditions, v1alpha1.ConditionLocalResolutionSucceeded) == nil {
			puntToNextCycle = true
		}
	}

	// let the next cycle resolve
	if puntToNextCycle {
		return
	}

	if len(itemList.Items) == 0 {
		return
	}

	err := r.RunGlobalResolution(r.ctx, itemList.Items...)
	if err != nil {
		// hack: block anything that is trying to be installed
		// in the error path
		itemUpdated := map[string]struct{}{}
		switch err.(type) {
		case deppy.NotSatisfiable:
			for _, appliedConstraint := range err.(deppy.NotSatisfiable) {
				variableID := string(appliedConstraint.Variable.Identifier())
				isInstalled := strings.HasPrefix(variableID, VariablePrefixInstalled)
				if isInstalled {
					continue
				}
				itemName := strings.TrimSpace(variableID[strings.Index(variableID, VariablePrefixSeparator)+1:])
				if _, ok := itemUpdated[itemName]; ok {
					continue
				}
				for _, item := range itemList.Items {
					if item.GetName() == itemName {
						meta.SetStatusCondition(&item.Status.Conditions, v1.Condition{
							Type:               v1alpha1.ConditionGlobalResolutionSucceeded,
							Status:             v1.ConditionFalse,
							ObservedGeneration: item.Generation,
							Reason:             "ResolutionFailure",
							Message:            err.Error(),
						})
						if err := r.client.Status().Update(r.ctx, &item); err != nil {
							// re-run global resolution
							r.Enqueue()
							return
						}
						itemUpdated[itemName] = struct{}{}
					}
				}
			}
		}

		// enqueue another resolution for the other items
		// that might not be responsible for the failures
		if len(itemUpdated) < len(itemList.Items) {
			r.Enqueue()
		}
	} else {
		for _, item := range itemList.Items {
			if meta.IsStatusConditionTrue(item.Status.Conditions, v1alpha1.ConditionLocalResolutionSucceeded) &&
				!meta.IsStatusConditionTrue(item.Status.Conditions, v1alpha1.ConditionGlobalResolutionSucceeded) {
				meta.SetStatusCondition(&item.Status.Conditions, v1.Condition{
					Type:               v1alpha1.ConditionGlobalResolutionSucceeded,
					Status:             v1.ConditionTrue,
					ObservedGeneration: item.Generation,
					Reason:             "ResolutionSuccess",
					Message:            "Global item resolution succeeded",
				})
				if err := r.client.Status().Update(r.ctx, &item); err != nil {
					r.Enqueue()
					return
				}
			}
		}
	}
}

func (r *Resolver) RunLocalResolution(ctx context.Context, item *v1alpha1.Item) error {
	itemList := v1alpha1.ItemList{}
	if err := r.client.List(r.ctx, &itemList); err != nil {
		return err
	}
	entitySource := NewEntitySource(itemList.Items)
	variableSource := NewItemVariableSource(*item)
	resolver, err := solver.NewDeppySolver(entitySource, variableSource)
	if err != nil {
		return err
	}
	_, err = resolver.Solve(ctx)
	return err
}

func (r *Resolver) RunGlobalResolution(ctx context.Context, items ...v1alpha1.Item) error {
	entitySource := NewEntitySource(items)
	variableSource := NewItemVariableSource(items...)
	resolver, err := solver.NewDeppySolver(entitySource, variableSource)
	if err != nil {
		return err
	}
	_, err = resolver.Solve(ctx)
	return err
}

func (r *Resolver) reconcile(item *v1alpha1.Item) {
	select {
	case <-r.ctx.Done():
	case r.reconciliationChannel <- event.GenericEvent{Object: item}:
	}
}
