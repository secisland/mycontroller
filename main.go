
package main

import (
	"flag"
	"time"

	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	clientset "github.com/secyu/mycontroller/pkg/generated/clientset/versioned"
	informers "github.com/secyu/mycontroller/pkg/generated/informers/externalversions"
	"github.com/secyu/mycontroller/pkg/signals"
)

var (
	masterURL  string
	kubeconfig string
)

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	exampleClient, err := clientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building example clientset: %s", err.Error())
	}

	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30) // 会创建一个 sharedInformerFactory 实例
	exampleInformerFactory := informers.NewSharedInformerFactory(exampleClient, time.Second*30)

	controller := NewController(kubeClient, exampleClient,
		kubeInformerFactory.Apps().V1().Deployments(), // 依次会创建一个 group、version、deploymentInformer 实例
		exampleInformerFactory.Mycontroller().V1().Devopses())  // 依次会创建一个 group、version、devopsInformer 实例

	// notice that there is no need to run Start methods in a separate goroutine. (i.e. go kubeInformerFactory.Start(stopCh)
	// Start method is non-blocking and runs all registered informers in a dedicated goroutine.
	kubeInformerFactory.Start(stopCh)	// 异步调用其字段 informer实例的 Run方法
										// informer 实例对应的是 sharedIndexInformer，Run方法主要功能是初始化 sharedIndexInformer controller字段，赋值一个基础控制器实例，并调用基础控制器的Run方法。
										// 		- 初始化 fifo队列：DeltaFIFO （DeltaFIFO是一个生产者－消费者队列，其中应将Reflector用作生产者，而消费者则是调用Pop（）方法的对象。）
										// 		- 初始化 NewReflector,并调用Reflector的 Run方法，在Run方法内，调用 r.ListAndWatch list资源并watch。
										//	 	- 调用基础控制器controller的processLoop方法，处理队列元素，即 c.config.Queue.Pop(PopProcessFunc(c.config.Process))
	exampleInformerFactory.Start(stopCh)    // 同上，区别在于资源是自定义的devops资源,另外，调用基础控制器的processLoop方法显示是处理不了自定义资源的，
											// 需要下面运行自定义的 controller.Run 处理队列元素

	if err = controller.Run(2, stopCh); err != nil {		// 主要是调用自定义的controller实例的 processNextWorkItem 方法，读取和处理workqueue消息
		klog.Fatalf("Error running controller: %s", err.Error())
	}
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
