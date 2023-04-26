package resolver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/eapache/channels"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/operator-framework/deppy/pkg/deppy"
	"github.com/operator-framework/deppy/pkg/deppy/solver"
	"github.com/perdasilva/resolvulator/api/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type MultiStageResolver interface {
	GetResolution(name types.NamespacedName) Resolution
	NotifyDeletion(thing *v1alpha1.Thing)
	NotifySpecChange(thing *v1alpha1.Thing)
}

const (
	ResolutionTypeScheduled string = "ResolutionScheduled"
	ResolutionTypeLocal     string = "LocalResolutionSucceeded"
	ResolutionTypeGlobal    string = "GlobalResolutionSucceeded"

	ResolutionReasonScheduled string = "ResolutionScheduled"
	ResolutionReasonUnknown   string = "UnknownResource"
	ResolutionFailed          string = "ResolutionFailed"
	ResolutionSucceeded       string = "ResolutionSucceeded"
)

type Resolution struct {
	v1.Condition
	Thing types.NamespacedName
}

type Resolver struct {
	requestChannel    chan interface{}
	ctx               context.Context
	consensusDuration time.Duration
	client            client.Client
	resolutionQueue   *channels.RingChannel
	resolutionState   map[string]Resolution
	lock              sync.RWMutex
	logger            logr.Logger
}

func NewResolver(ctx context.Context, client client.Client) *Resolver {
	resolver := &Resolver{
		requestChannel:    make(chan interface{}),
		ctx:               ctx,
		consensusDuration: 5 * time.Second,
		client:            client,
		resolutionQueue:   channels.NewRingChannel(100),
		lock:              sync.RWMutex{},
		resolutionState:   map[string]Resolution{},
		logger:            log.FromContext(ctx).WithName("resolver"),
	}
	resolver.startResolutionContextWatcher()
	return resolver
}

func (r *Resolver) NotifyDeletion(thing *v1alpha1.Thing) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.logger.Info("thing deleted", "thing", thing.GetName())
	delete(r.resolutionState, thing.GetName())
	r.enqueueResolution()
}

func (r *Resolver) NotifySpecChange(thing *v1alpha1.Thing) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.logger.Info("thing spec changed", "thing", thing.GetName())
	r.resolutionState[thing.Name] = Resolution{
		Thing: types.NamespacedName{Name: thing.Name, Namespace: thing.Namespace},
		Condition: v1.Condition{
			Type:               ResolutionTypeScheduled,
			Status:             v1.ConditionTrue,
			Reason:             ResolutionReasonScheduled,
			Message:            "Resolution scheduled",
			LastTransitionTime: v1.Now(),
			ObservedGeneration: thing.Generation,
		},
	}
	r.enqueueResolution()
}

func (r *Resolver) updateResolution(thingName string, resolution Resolution) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.resolutionState[thingName] = resolution
}

func (r *Resolver) GetResolution(thing types.NamespacedName) Resolution {
	r.lock.RLock()
	defer r.lock.RUnlock()
	resolution, ok := r.resolutionState[thing.Name]
	if !ok {
		return Resolution{
			Thing: types.NamespacedName{Name: thing.Name, Namespace: thing.Namespace},
			Condition: v1.Condition{
				Type:               ResolutionTypeScheduled,
				Status:             v1.ConditionFalse,
				Reason:             ResolutionReasonUnknown,
				Message:            "the resolver has not yet seen this resource",
				LastTransitionTime: v1.Now(),
			},
		}
	}
	return resolution
}

func (r *Resolver) enqueueResolution() {
	select {
	case <-r.ctx.Done():
	case r.requestChannel <- "resolve":
	}
}

func (r *Resolver) startResolutionContextWatcher() {
	go func() {
		lastRequestTime := time.Time{}
		ticker := time.NewTicker(1 * time.Second)
		for {
			select {
			case <-r.ctx.Done():
				return
			case _, hasNext := <-r.requestChannel:
				if !hasNext {
					return
				}
				lastRequestTime = time.Now()
				r.logger.Info("last resolution request time", "time", lastRequestTime)
			case <-ticker.C:
				// if new resolution context has gathered
				if !lastRequestTime.IsZero() && time.Since(lastRequestTime) >= r.consensusDuration {
					r.logger.Info("resolution context gathered", "time", time.Now())
					if err := r.resolve(); err != nil {
						fmt.Printf("failed to resolve: %v\n", err.Error())
						r.enqueueResolution() // try again
					}
					// reset input collection
					lastRequestTime = time.Time{}
				}
			}
		}
	}()
}

func (r *Resolver) resolve() error {
	resolutionLogger := r.logger.WithValues("resolutionID", uuid.New().String())
	// get resources that make up the current resolution context
	thingList := v1alpha1.ThingList{}
	if err := r.client.List(r.ctx, &thingList); err != nil {
		return err
	}
	var thingNames []string
	for _, thing := range thingList.Items {
		thingNames = append(thingNames, thing.GetName())
	}
	resolutionLogger.Info("things to resolve", "things", thingNames)

	installedThingList := v1alpha1.InstalledThingList{}
	if err := r.client.List(r.ctx, &installedThingList); err != nil {
		return err
	}
	var installedThingNames []string
	for _, installedThing := range installedThingList.Items {
		installedThingNames = append(installedThingNames, installedThing.GetName())
	}
	resolutionLogger.Info("installed things", "things", installedThingNames)

	if len(thingList.Items) == 0 {
		return nil
	}

	// todo(perdasilva): think if we also want to check installed things - my guess rn is not

	// run each installed thing through local resolution to ensure that they are individually installable
	// given the current installed things

	// todo(perdasilva): this can be parallelized with a worker pool
	var successfullyResolvedThings []v1alpha1.Thing
	for _, thing := range thingList.Items {
		resolutionLogger.Info("resolve thing", "thing", thing.GetName())
		if err := r.runLocalResolution(r.ctx, thing, thingList.Items, installedThingList.Items); err != nil {

			resolutionLogger.Error(err, "failed to resolve thing", "thing", thing.GetName())
			r.updateResolution(thing.GetName(), Resolution{
				Thing: types.NamespacedName{
					Namespace: thing.GetNamespace(),
					Name:      thing.GetName(),
				},
				Condition: v1.Condition{
					Type:               ResolutionTypeLocal,
					Status:             v1.ConditionFalse,
					Reason:             ResolutionFailed,
					ObservedGeneration: thing.GetGeneration(),
					Message:            fmt.Sprintf("%v", err.Error()),
					LastTransitionTime: v1.Now(),
				},
			})
		} else {
			resolutionLogger.Info("successfully resolved thing", "thing", thing.GetName())
			r.updateResolution(thing.GetName(), Resolution{
				Thing: types.NamespacedName{
					Namespace: thing.GetNamespace(),
					Name:      thing.GetName(),
				},
				Condition: v1.Condition{
					Type:               ResolutionTypeLocal,
					Status:             v1.ConditionTrue,
					Reason:             ResolutionSucceeded,
					ObservedGeneration: thing.GetGeneration(),
					Message:            fmt.Sprintf("local resolution succeeded"),
					LastTransitionTime: v1.Now(),
				},
			})
			successfullyResolvedThings = append(successfullyResolvedThings, thing)
		}
	}

	// run global resolution
	for len(successfullyResolvedThings) > 0 {
		thingNames = []string{}
		for _, thing := range successfullyResolvedThings {
			thingNames = append(thingNames, thing.GetName())
		}
		resolutionLogger.Info("successfully resolved things", "things", thingNames)
		resolutionLogger.Info("starting global resolution")
		err := r.runGlobalResolution(r.ctx, successfullyResolvedThings, installedThingList.Items)

		// weed out failing things
		if err != nil {
			switch err.(type) {
			case deppy.NotSatisfiable:
				resolutionLogger.Error(err, "global resolution failed")
				for _, appliedConstraint := range err.(deppy.NotSatisfiable) {
					variableID := string(appliedConstraint.Variable.Identifier())
					isInstalled := func() bool {
						for _, installedThing := range installedThingList.Items {
							if installedThing.GetName() == variableID {
								return true
							}
						}
						return false
					}()
					if isInstalled {
						continue
					}
					thingName := strings.TrimSpace(variableID[strings.Index(variableID, VariablePrefixSeparator)+1:])
					thing := func() *v1alpha1.Thing {
						for _, thing := range thingList.Items {
							if thing.GetName() == thingName {
								return &thing
							}
						}
						return nil
					}()
					r.updateResolution(thingName, Resolution{
						Thing: types.NamespacedName{
							Namespace: v1.NamespaceAll,
							Name:      thingName,
						},
						Condition: v1.Condition{
							Type:               ResolutionTypeGlobal,
							Status:             v1.ConditionFalse,
							Reason:             ResolutionFailed,
							ObservedGeneration: thing.GetGeneration(),
							Message:            fmt.Sprintf("%v", err.Error()),
							LastTransitionTime: v1.Now(),
						},
					})

					// drop the failing thing from the list of successfully resolved things
					for index, thing := range successfullyResolvedThings {
						if thing.GetName() == thingName {
							successfullyResolvedThings = append(successfullyResolvedThings[:index], successfullyResolvedThings[index+1:]...)
							break
						}
					}
				}
			}
			continue
		}
		for _, thing := range successfullyResolvedThings {
			r.updateResolution(thing.GetName(), Resolution{
				Thing: types.NamespacedName{
					Namespace: thing.GetNamespace(),
					Name:      thing.GetName(),
				},
				Condition: v1.Condition{
					Type:               ResolutionTypeGlobal,
					Status:             v1.ConditionTrue,
					Reason:             ResolutionSucceeded,
					Message:            "global resolution succeeded",
					LastTransitionTime: v1.Now(),
				},
			})
		}
		resolutionLogger.Info("finished global resolution", "successfullyResolvedThings", thingNames, "installedThings", installedThingNames)
		break
	}
	return nil
}

func (r *Resolver) runResolution(ctx context.Context, things []v1alpha1.Thing, installedThings []v1alpha1.InstalledThing, requiredThings ...v1alpha1.Thing) error {
	entitySource := NewEntitySource(things, installedThings)
	variableSource := NewRequiredThingVariableSource(requiredThings...)
	resolver, err := solver.NewDeppySolver(entitySource, variableSource)
	if err != nil {
		return err
	}
	solution, err := resolver.Solve(ctx)
	if err != nil {
		return err
	}
	if len(solution.Error().(deppy.NotSatisfiable)) > 0 {
		return solution.Error()
	}
	return nil
}

func (r *Resolver) runLocalResolution(ctx context.Context, thing v1alpha1.Thing, things []v1alpha1.Thing, installedThings []v1alpha1.InstalledThing) error {
	return r.runResolution(ctx, things, installedThings, thing)
}

func (r *Resolver) runGlobalResolution(ctx context.Context, things []v1alpha1.Thing, installedThings []v1alpha1.InstalledThing) error {
	return r.runResolution(ctx, things, installedThings, things...)
}
