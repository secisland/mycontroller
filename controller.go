package main

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	myv1 "github.com/secyu/mycontroller/pkg/apis/mycontroller/v1"
	clientset "github.com/secyu/mycontroller/pkg/generated/clientset/versioned"
	myscheme "github.com/secyu/mycontroller/pkg/generated/clientset/versioned/scheme"
	informers "github.com/secyu/mycontroller/pkg/generated/informers/externalversions/mycontroller/v1"
	listers "github.com/secyu/mycontroller/pkg/generated/listers/mycontroller/v1"
)

const controllerAgentName = "mycontroller"

const (
	// SuccessSynced is used as part of the Event 'reason' when a Devops is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a Devops fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by Devops"
	// MessageResourceSynced is the message used for an Event fired when a Devops
	// is synced successfully
	MessageResourceSynced = "Devops synced successfully"
)

// Controller is the controller implementation for Devops resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// sampleclientset is a clientset for our own API group
	myclientset clientset.Interface

	deploymentsLister appslisters.DeploymentLister
	deploymentsSynced cache.InformerSynced
	devopsLister        listers.DevopsLister
	devopsSynced        cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}


// NewController returns a new sample controller
func NewController(
	kubeclientset kubernetes.Interface,
	myclientset clientset.Interface,
	deploymentInformer appsinformers.DeploymentInformer,
	devopsInformer informers.DevopsInformer) *Controller {

	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	utilruntime.Must(myscheme.AddToScheme(scheme.Scheme))
	klog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:     kubeclientset,
		myclientset:   	   myclientset,
		deploymentsLister: deploymentInformer.Lister(), // 实例化一个 deploymentLister
														// 同时如果main初始化的kubeInformerFactory工厂实例 informers字段中资源类型不存在会实例化（sharedInformerFactory的InformerFor方法）：
														// 一个资源类型对应一个 sharedIndexInformer 实例，其field包含:
														// 			indexer
														// 			controller
														// 			processor
														// 			listerWatcher 等；
		deploymentsSynced: deploymentInformer.Informer().HasSynced, // HasSynced returns true if the shared informer's store has been
																	// informed by at least one full LIST of the authoritative state
																	// of the informer's object collection.  This is unrelated to "resync".
																	//
																	// 调用Informer()时，因为上一面一条语名已完成资源类型对应的 sharedIndexInformer实例化，
																	// 这里会直接调用 sharedIndexInformer实例 的 HasSynced 方法。会依次调用：
																	//		controller.HasSynced()
																	//		controller控制器的config.Queue.HasSynced()
																	// 		Queue对应的FIFO实例的 HasSynced()方法
		devopsLister:      devopsInformer.Lister(),
		devopsSynced:      devopsInformer.Informer().HasSynced,
		workqueue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Devops"),
		recorder:          recorder,
	}

	klog.Info("Setting up event handlers")

	// 下面两个AddEventHandler方法会产生每10秒的sync动作。
	// 另外说明一点：由于devops类型最终产生的还是deployment，所以这两个 AddEventHandler 对于devops资源会重复添加到client-go的队列 DeltaFIFO 中，注意不是自定义controller中的workqueue。

	// Set up an event handler for when Devops resources change
	devopsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueDevops,
		UpdateFunc: func(old, new interface{}) {   // debug验证结果：每隔10秒sync一次都会触发UpdateFunc
			controller.enqueueDevops(new)
		},
	})
	// Set up an event handler for when Deployment resources change. This
	// handler will lookup the owner of the given Deployment, and if it is
	// owned by a Devops resource will enqueue that Devops resource for
	// processing. This way, we don't need to implement custom logic for
	// handling Deployment resources. More info on this pattern:
	// https://github.com/kubernetes/community/blob/8cafef897a22026d42f5e5bb3f104febe7e29830/contributors/devel/controllers.md
	deploymentInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{  //deployment资源是内置资源，而绑定的controller.handleObject方法只处理ownerRef.Kind为Devops类型的资源。
																					// 一般的deployment资源是没有ownerReferences信息的，可以通过kubectl get deploy XXX -o yaml查看验证。
																					// 这样资源状态发生变动，也会触发相应的处理动作。
		AddFunc: controller.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newDepl := new.(*appsv1.Deployment)
			oldDepl := old.(*appsv1.Deployment)
			if newDepl.ResourceVersion == oldDepl.ResourceVersion {
				// Periodic resync will send update events for all known Deployments.
				// Two different versions of the same Deployment will always have different RVs.
				return
			}
			controller.handleObject(new)
		},
		DeleteFunc: controller.handleObject,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Devops controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.deploymentsSynced, c.devopsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	// Launch two workers to process Devops resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}


// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// Devops resource to be synced.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}



// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Devops resource
// with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	klog.Info("###############")
	// Get the Devops resource with this namespace/name
	devops, err := c.devopsLister.Devopses(namespace).Get(name)
	if err != nil {
		// The Devops resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("devops '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}

	deploymentName := devops.Spec.DeploymentName
	if deploymentName == "" {
		// We choose to absorb the error here as the worker would requeue the
		// resource otherwise. Instead, the next time the resource is updated
		// the resource will be queued again.
		utilruntime.HandleError(fmt.Errorf("%s: deployment name must be specified", key))
		return nil
	}

	// Get the deployment with the name specified in Devops.spec
	deployment, err := c.deploymentsLister.Deployments(devops.Namespace).Get(deploymentName)
	// If the resource doesn't exist, we'll create it
	if errors.IsNotFound(err) {
		deployment, err = c.kubeclientset.AppsV1().Deployments(devops.Namespace).Create(context.TODO(), newDeployment(devops), metav1.CreateOptions{})
	}

	// If an error occurs during Get/Create, we'll requeue the item so we can
	// attempt processing again later. This could have been caused by a
	// temporary network failure, or any other transient reason.
	if err != nil {
		return err
	}

	// If the Deployment is not controlled by this Devops resource, we should log
	// a warning to the event recorder and return error msg.
	if !metav1.IsControlledBy(deployment, devops) {
		msg := fmt.Sprintf(MessageResourceExists, deployment.Name)
		c.recorder.Event(devops, corev1.EventTypeWarning, ErrResourceExists, msg)
		return fmt.Errorf(msg)
	}

	// If this number of the replicas on the Devops resource is specified, and the
	// number does not equal the current desired replicas on the Deployment, we
	// should update the Deployment resource.
	if devops.Spec.Replicas != nil && *devops.Spec.Replicas != *deployment.Spec.Replicas {
		klog.V(4).Infof("Devops %s replicas: %d, deployment replicas: %d", name, *devops.Spec.Replicas, *deployment.Spec.Replicas)
		deployment, err = c.kubeclientset.AppsV1().Deployments(devops.Namespace).Update(context.TODO(), newDeployment(devops), metav1.UpdateOptions{})
	}

	// If an error occurs during Update, we'll requeue the item so we can
	// attempt processing again later. This could have been caused by a
	// temporary network failure, or any other transient reason.
	if err != nil {
		return err
	}

	// Finally, we update the status block of the Devops resource to reflect the
	// current state of the world
	err = c.updateDevopsStatus(devops, deployment)
	if err != nil {
		return err
	}

	c.recorder.Event(devops, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return nil
}


func (c *Controller) updateDevopsStatus(devops *myv1.Devops, deployment *appsv1.Deployment) error {
	// NEVER modify objects from the store. It's a read-only, local cache.
	// You can use DeepCopy() to make a deep copy of original object and modify this copy
	// Or create a copy manually for better performance
	devopsCopy := devops.DeepCopy()
	devopsCopy.Status.AvailableReplicas = deployment.Status.AvailableReplicas
	// If the CustomResourceSubresources feature gate is not enabled,
	// we must use Update instead of UpdateStatus to update the Status block of the Devops resource.
	// UpdateStatus will not allow changes to the Spec of the resource,
	// which is ideal for ensuring nothing other than resource status has been updated.
	_, err := c.myclientset.MycontrollerV1().Devopses(devops.Namespace).Update(context.TODO(), devopsCopy, metav1.UpdateOptions{})
	return err
}

// enqueueDevops takes a Devops resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than Devops.
func (c *Controller) enqueueDevops(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.Info("##### enqueueDevops key=>",key)
	c.workqueue.Add(key)
}

// handleObject will take any resource implementing metav1.Object and attempt
// to find the Devops resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that Devops resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
func (c *Controller) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		klog.V(4).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}

	klog.Infof("#######Processing object: %s", object.GetName())

	klog.V(4).Infof("Processing object: %s", object.GetName())
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		// If this object is not owned by a Devops, we should not do anything more
		// with it.
		klog.Info("##### ownerRef.Kind=>",ownerRef.Kind)
		if ownerRef.Kind != "Devops" {
			return
		}

		devops, err := c.devopsLister.Devopses(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			klog.V(4).Infof("ignoring orphaned object '%s' of devops '%s'", object.GetSelfLink(), ownerRef.Name)
			return
		}

		c.enqueueDevops(devops)
		return
	} else {
		klog.Info("##### ownerRef.Kind 为空",object.GetName())
	}
}

// newDeployment creates a new Deployment for a Devops resource. It also sets
// the appropriate OwnerReferences on the resource so handleObject can discover
// the Devops resource that 'owns' it.
func newDeployment(devops *myv1.Devops) *appsv1.Deployment {
	labels := map[string]string{
		"app":        "nginx",
		"controller": devops.Name,
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      devops.Spec.DeploymentName,
			Namespace: devops.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(devops, myv1.SchemeGroupVersion.WithKind("Devops")),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: devops.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}
}
