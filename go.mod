module github.com/secyu/mycontroller

go 1.14

require (
	k8s.io/api v0.0.0-20210202201024-9f65ac4826aa
	k8s.io/apimachinery v0.0.0-20210202200849-9e39a13d2cab
	k8s.io/client-go v0.0.0-20210202201239-3147a30d7bb5
	k8s.io/code-generator v0.0.0-20210202200712-b6eef682227f
	k8s.io/klog/v2 v2.5.0
)

replace (
	k8s.io/api => k8s.io/api v0.0.0-20210202201024-9f65ac4826aa
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20210202200849-9e39a13d2cab
	k8s.io/client-go => k8s.io/client-go v0.0.0-20210202201239-3147a30d7bb5
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20210202200712-b6eef682227f
)
