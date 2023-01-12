package controllers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/eapache/channels"
	"github.com/perdasilva/resolvulator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Resolver struct {
	requestChannel    chan bool
	mu                sync.RWMutex
	ctx               context.Context
	consensusDuration time.Duration
	client            client.Client
	resolutionQueue   *channels.RingChannel
}

func NewResolver(ctx context.Context, client client.Client) *Resolver {
	resolver := &Resolver{
		requestChannel:    make(chan bool),
		mu:                sync.RWMutex{},
		ctx:               ctx,
		consensusDuration: 5 * time.Second,
		client:            client,
		resolutionQueue:   channels.NewRingChannel(100),
	}
	resolver.watch()
	resolver.watchResolutionQueue()
	return resolver
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
	movedAnythingToResolving := false
	for _, item := range itemList.Items {
		if item.Status.Phase == v1alpha1.PhaseReachingContextConsensus || reset {
			itemCopy := *item.DeepCopy()
			itemCopy.Status.Phase = v1alpha1.PhaseResolving
			if err := r.client.Status().Update(r.ctx, &itemCopy); err != nil {
				// TODO: (perdasilva) make this more robust - it might lead to dead cycles
				return
			}
			movedAnythingToResolving = true
		}
	}

	// let the next cycle resolve
	if movedAnythingToResolving {
		return
	}

	for _, item := range itemList.Items {
		itemCopy := item.DeepCopy()
		dependenciesMet := true
		for _, dependencyName := range item.Spec.Dependencies {
			dependency := &v1alpha1.Item{}
			err := r.client.Get(r.ctx, client.ObjectKey{Name: dependencyName}, dependency)
			if err != nil {
				message := fmt.Sprintf("dependency '%s' not found", dependencyName)
				if !errors.IsNotFound(err) {
					message = fmt.Sprintf("error: %s", err)
				}
				meta.SetStatusCondition(&itemCopy.Status.Conditions, v1.Condition{
					Type:               fmt.Sprintf("DependencyOn%sSatisfied", dependencyName),
					Status:             "False",
					ObservedGeneration: item.Generation,
					Reason:             "Error",
					Message:            message,
				})
				dependenciesMet = false
				continue
			}
			if dependency.Status.Phase != v1alpha1.PhaseInstalled {
				meta.SetStatusCondition(&itemCopy.Status.Conditions, v1.Condition{
					Type:               fmt.Sprintf("DependencyOn%sSatisfied", dependencyName),
					Status:             "False",
					ObservedGeneration: item.Generation,
					Reason:             "DependencyUnmet",
					Message:            fmt.Sprintf("Expecting dependency %s to be in state %s but it is in state %s", dependencyName, v1alpha1.PhaseInstalled, dependency.Status.Phase),
				})
				dependenciesMet = false
			} else {
				meta.SetStatusCondition(&itemCopy.Status.Conditions, v1.Condition{
					Type:               fmt.Sprintf("DependencyOn%sSatisfied", dependencyName),
					Status:             "True",
					ObservedGeneration: item.Generation,
					Reason:             "DependencyMet",
					Message:            fmt.Sprintf("Dependency '%s' in phase %s", dependencyName, dependency.Status.Phase),
				})
			}
		}
		if dependenciesMet && itemCopy.Status.Phase == v1alpha1.PhaseResolving {
			if meta.IsStatusConditionTrue(itemCopy.Status.Conditions, "BundleUnpack") {
				if meta.IsStatusConditionTrue(itemCopy.Status.Conditions, "BundleInstalled") {
					itemCopy.Status.Phase = v1alpha1.PhaseInstalled
				} else {
					itemCopy.Status.Phase = v1alpha1.PhaseInstalling
				}
			} else {
				itemCopy.Status.Phase = v1alpha1.PhaseUnpacking
			}
		}
		if err := r.client.Status().Update(r.ctx, itemCopy); err != nil {
			// TODO: (perdasilva) make this more robust - it might lead to dead cycles
			return
		}
	}
}
