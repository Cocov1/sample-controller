/*
Copyright 2017 The Kubernetes Authors.

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

package main

import (
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/util/metrics"
	"time"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
)

const maxRetries=3

const (
	// SuccessSynced is used as part of the Event 'reason' when a Foo is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a Foo fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by Foo"
	// MessageResourceSynced is the message used for an Event fired when a Foo
	// is synced successfully
	MessageResourceSynced = "Foo synced successfully"
)

// Controller is the controller implementation for Foo resources
type ProblemPodController struct {
	client           clientset.Interface
	eventBroadcaster record.EventBroadcaster
	eventRecorder    record.EventRecorder

	// podLister is able to list/get pods and is populated by the shared informer passed to
	// NewEndpointController.
	podLister corelisters.PodLister
	// podsSynced returns true if the pod shared informer has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	podsSynced cache.InformerSynced

	// endpointsLister is able to list/get endpoints and is populated by the shared informer passed to
	// NewEndpointController.
	problemPodsLister corelisters.EndpointsLister
	// endpointsSynced returns true if the endpoints shared informer has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	problemPodsSynced cache.InformerSynced

	queue workqueue.RateLimitingInterface
	workerLoopPeriod time.Duration

}

// NewController returns a new sample controller
func NewProblemPodController(
	podInformer coreinformers.PodInformer,
	client clientset.Interface,
	) *ProblemPodController {

	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	broadcaster := record.NewBroadcaster()
	broadcaster.StartLogging(klog.Infof)
	broadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: client.CoreV1().Events("")})
	recorder := broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "problemPod-controller"})

	if client != nil && client.CoreV1().RESTClient().GetRateLimiter() != nil {
		metrics.RegisterMetricAndTrackRateLimiterUsage("endpoint_controller", client.CoreV1().RESTClient().GetRateLimiter())
	}

	//utilruntime.Must(samplescheme.AddToScheme(scheme.Scheme))
	//glog.V(4).Info("Creating event broadcaster")
	//eventBroadcaster := record.NewBroadcaster()
	//eventBroadcaster.StartLogging(glog.Infof)
	//eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	//recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	p := &ProblemPodController{
		client:           client,
		queue:            workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "problemPod"),
		workerLoopPeriod: time.Second,
	}

	// Set up an event handler for when Foo resources change
	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: p.updatePod,
	})
	p.podLister = podInformer.Lister()
	p.podsSynced = podInformer.Informer().HasSynced

	p.eventBroadcaster = broadcaster
	p.eventRecorder = recorder


	return p
}


// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *ProblemPodController) Run(workers int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting problemPod controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	defer klog.Infof("Shutting down endpoint controller")

		glog.Info("Starting workers")
		// Launch two workers to process Foo resources
		for i := 0; i < workers; i++ {
			go wait.Until(c.worker, c.workerLoopPeriod, stopCh)
		}
		go func() {
			defer utilruntime.HandleCrash()
		}()

		<-stopCh
		return nil
}


// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *ProblemPodController) worker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *ProblemPodController) processNextWorkItem() bool {
	eKey, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(eKey)

	err := c.syncPod(eKey.(string))
	c.handleErr(err, eKey)

	return true
}

func (c *ProblemPodController) syncPod(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}
	pod,err := c.podLister.Pods(namespace).Get(name)
	if err!=nil {
		return nil
	}
	var labels = pod.ObjectMeta.Labels
	delete(labels,"app")
	labels["status"] = "problem"
	return nil
}

func (e *ProblemPodController) updatePod(old, cur interface{}) {
	newPod := cur.(*v1.Pod)
	oldPod := old.(*v1.Pod)
	if newPod.ResourceVersion == oldPod.ResourceVersion {
		// Periodic resync will send update events for all known pods.
		// Two different versions of the same pod will always have different RVs.
		return
	}
	podChangedFlag := podChanged(oldPod, newPod)

	// If  the pod  is unchanged, no update is needed
	if !podChangedFlag  {
		return
	}
		e.queue.Add(newPod)

}

func podChanged(oldPod, newPod *v1.Pod) bool {
	// If the pod's deletion timestamp is set, remove endpoint from ready address.
	if newPod.DeletionTimestamp != oldPod.DeletionTimestamp {
		return true
	}
	// If the pod's readiness has changed, the associated endpoint address
	// will move from the unready endpoints set to the ready endpoints.
	// So for the purposes of an endpoint, a readiness change on a pod
	// means we have a changed pod.
	if podutil.IsPodReady(oldPod) != podutil.IsPodReady(newPod) &&(podutil.IsPodReady(newPod) == false){
		return true
	}
	return false
}
func (e *ProblemPodController) handleErr(err error, key interface{}) {
	if err == nil {
		e.queue.Forget(key)
		return
	}

	if e.queue.NumRequeues(key) < maxRetries {
		klog.V(2).Infof("Error syncing pod %q, retrying. Error: %v", key, err)
		e.queue.AddRateLimited(key)
		return
	}

	klog.Warningf("Dropping service %q out of the queue: %v", key, err)
	e.queue.Forget(key)
	utilruntime.HandleError(err)
}
