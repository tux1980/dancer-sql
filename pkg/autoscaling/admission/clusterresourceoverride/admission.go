package clusterresourceoverride

import (
	"fmt"
	"io"
	"strings"

	"github.com/golang/glog"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/admission/initializer"
	"k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"
	kapi "k8s.io/kubernetes/pkg/apis/core"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	internalinformers "k8s.io/kubernetes/pkg/client/informers/informers_generated/internalversion"
	"k8s.io/kubernetes/pkg/client/listers/core/internalversion"
	kadmission "k8s.io/kubernetes/pkg/kubeapiserver/admission"
	"k8s.io/kubernetes/plugin/pkg/admission/limitranger"

	api "github.com/openshift/origin/pkg/autoscaling/admission/apis/clusterresourceoverride"
	"github.com/openshift/origin/pkg/autoscaling/admission/apis/clusterresourceoverride/validation"
	configlatest "github.com/openshift/origin/pkg/cmd/server/apis/config/latest"
	"github.com/openshift/origin/pkg/project/apiserver/registry/projectrequest/delegated"
)

const (
	clusterResourceOverrideAnnotation = "autoscaling.openshift.io/cluster-resource-override-enabled"
	cpuBaseScaleFactor                = 1000.0 / (1024.0 * 1024.0 * 1024.0) // 1000 milliCores per 1GiB
)

var (
	cpuFloor = resource.MustParse("1m")
	memFloor = resource.MustParse("1Mi")
)

func Register(plugins *admission.Plugins) {
	plugins.Register(api.PluginName,
		func(config io.Reader) (admission.Interface, error) {
			pluginConfig, err := ReadConfig(config)
			if err != nil {
				return nil, err
			}
			if pluginConfig == nil {
				glog.Infof("Admission plugin %q is not configured so it will be disabled.", api.PluginName)
				return nil, nil
			}
			return newClusterResourceOverride(pluginConfig)
		})
}

type internalConfig struct {
	limitCPUToMemoryRatio     float64
	cpuRequestToLimitRatio    float64
	memoryRequestToLimitRatio float64
}
type clusterResourceOverridePlugin struct {
	*admission.Handler
	config            *internalConfig
	nsLister          corev1listers.NamespaceLister
	LimitRanger       admission.Interface
	limitRangesLister internalversion.LimitRangeLister
}

var _ = initializer.WantsExternalKubeInformerFactory(&clusterResourceOverridePlugin{})
var _ = kadmission.WantsInternalKubeInformerFactory(&clusterResourceOverridePlugin{})
var _ = kadmission.WantsInternalKubeClientSet(&clusterResourceOverridePlugin{})
var _ = admission.MutationInterface(&clusterResourceOverridePlugin{})
var _ = admission.ValidationInterface(&clusterResourceOverridePlugin{})

// newClusterResourceOverride returns an admission controller for containers that
// configurably overrides container resource request/limits
func newClusterResourceOverride(config *api.ClusterResourceOverrideConfig) (admission.Interface, error) {
	glog.V(2).Infof("%s admission controller loaded with config: %v", api.PluginName, config)
	var internal *internalConfig
	if config != nil {
		internal = &internalConfig{
			limitCPUToMemoryRatio:     float64(config.LimitCPUToMemoryPercent) / 100,
			cpuRequestToLimitRatio:    float64(config.CPURequestToLimitPercent) / 100,
			memoryRequestToLimitRatio: float64(config.MemoryRequestToLimitPercent) / 100,
		}
	}

	limitRanger, err := limitranger.NewLimitRanger(nil)
	if err != nil {
		return nil, err
	}

	return &clusterResourceOverridePlugin{
		Handler:     admission.NewHandler(admission.Create),
		config:      internal,
		LimitRanger: limitRanger,
	}, nil
}

func (d *clusterResourceOverridePlugin) SetInternalKubeInformerFactory(i internalinformers.SharedInformerFactory) {
	d.LimitRanger.(kadmission.WantsInternalKubeInformerFactory).SetInternalKubeInformerFactory(i)
	d.limitRangesLister = i.Core().InternalVersion().LimitRanges().Lister()
}

func (d *clusterResourceOverridePlugin) SetInternalKubeClientSet(c kclientset.Interface) {
	d.LimitRanger.(kadmission.WantsInternalKubeClientSet).SetInternalKubeClientSet(c)
}

func (d *clusterResourceOverridePlugin) SetExternalKubeInformerFactory(kubeInformers informers.SharedInformerFactory) {
	d.nsLister = kubeInformers.Core().V1().Namespaces().Lister()
}

func ReadConfig(configFile io.Reader) (*api.ClusterResourceOverrideConfig, error) {
	obj, err := configlatest.ReadYAML(configFile)
	if err != nil {
		glog.V(5).Infof("%s error reading config: %v", api.PluginName, err)
		return nil, err
	}
	if obj == nil {
		return nil, nil
	}
	config, ok := obj.(*api.ClusterResourceOverrideConfig)
	if !ok {
		return nil, fmt.Errorf("unexpected config object: %#v", obj)
	}
	glog.V(5).Infof("%s config is: %v", api.PluginName, config)
	if errs := validation.Validate(config); len(errs) > 0 {
		return nil, errs.ToAggregate()
	}

	return config, nil
}

func (a *clusterResourceOverridePlugin) ValidateInitialization() error {
	if a.nsLister == nil {
		return fmt.Errorf("%s did not get a namespace lister", api.PluginName)
	}
	v, ok := a.LimitRanger.(admission.InitializationValidator)
	if !ok {
		return fmt.Errorf("LimitRanger does not implement kadmission.Validator")
	}
	return v.ValidateInitialization()
}

func isExemptedNamespace(name string) bool {
	for _, s := range delegated.ForbiddenNames {
		if name == s {
			return true
		}
	}
	for _, s := range delegated.ForbiddenPrefixes {
		if strings.HasPrefix(name, s) {
			return true
		}
	}
	return false
}

func (a *clusterResourceOverridePlugin) Admit(attr admission.Attributes) error {
	return a.admit(attr, true)
}

func (a *clusterResourceOverridePlugin) Validate(attr admission.Attributes) error {
	return a.admit(attr, false)
}

// TODO this will need to update when we have pod requests/limits
func (a *clusterResourceOverridePlugin) admit(attr admission.Attributes, mutationAllowed bool) error {
	glog.V(6).Infof("%s admission controller is invoked", api.PluginName)
	if a.config == nil || attr.GetResource().GroupResource() != kapi.Resource("pods") || attr.GetSubresource() != "" {
		return nil // not applicable
	}
	pod, ok := attr.GetObject().(*kapi.Pod)
	if !ok {
		return admission.NewForbidden(attr, fmt.Errorf("unexpected object: %#v", attr.GetObject()))
	}
	glog.V(5).Infof("%s is looking at creating pod %s in project %s", api.PluginName, pod.Name, attr.GetNamespace())

	// allow annotations on project to override
	ns, err := a.nsLister.Get(attr.GetNamespace())
	if err != nil {
		glog.Warningf("%s got an error retrieving namespace: %v", api.PluginName, err)
		return admission.NewForbidden(attr, err) // this should not happen though
	}

	projectEnabledPlugin, exists := ns.Annotations[clusterResourceOverrideAnnotation]
	if exists && projectEnabledPlugin != "true" {
		glog.V(5).Infof("%s is disabled for project %s", api.PluginName, attr.GetNamespace())
		return nil // disabled for this project, do nothing
	}

	if isExemptedNamespace(ns.Name) {
		glog.V(5).Infof("%s is skipping exempted project %s", api.PluginName, attr.GetNamespace())
		return nil // project is exempted, do nothing
	}

	namespaceLimits := []*kapi.LimitRange{}

	if a.limitRangesLister != nil {
		limits, err := a.limitRangesLister.LimitRanges(attr.GetNamespace()).List(labels.Everything())
		if err != nil {
			return err
		}
		namespaceLimits = limits
	}

	// Don't mutate resource requirements below the namespace
	// limit minimums.
	nsCPUFloor := minResourceLimits(namespaceLimits, kapi.ResourceCPU)
	nsMemFloor := minResourceLimits(namespaceLimits, kapi.ResourceMemory)

	// Reuse LimitRanger logic to apply limit/req defaults from the project. Ignore validation
	// errors, assume that LimitRanger will run after this plugin to validate.
	glog.V(5).Infof("%s: initial pod limits are: %#v", api.PluginName, pod.Spec)
	if err := a.LimitRanger.(admission.MutationInterface).Admit(attr); err != nil {
		glog.V(5).Infof("%s: error from LimitRanger: %#v", api.PluginName, err)
	}
	glog.V(5).Infof("%s: pod limits after LimitRanger: %#v", api.PluginName, pod.Spec)
	for i := range pod.Spec.InitContainers {
		if err := updateContainerResources(a.config, &pod.Spec.InitContainers[i], nsCPUFloor, nsMemFloor, mutationAllowed); err != nil {
			return admission.NewForbidden(attr, fmt.Errorf("spec.initContainers[%d].%v", i, err))
		}
	}
	for i := range pod.Spec.Containers {
		if err := updateContainerResources(a.config, &pod.Spec.Containers[i], nsCPUFloor, nsMemFloor, mutationAllowed); err != nil {
			return admission.NewForbidden(attr, fmt.Errorf("spec.containers[%d].%v", i, err))
		}
	}
	glog.V(5).Infof("%s: pod limits after overrides are: %#v", api.PluginName, pod.Spec)
	return nil
}

func updateContainerResources(config *internalConfig, container *kapi.Container, nsCPUFloor, nsMemFloor *resource.Quantity, mutationAllowed bool) error {
	resources := container.Resources
	memLimit, memFound := resources.Limits[kapi.ResourceMemory]
	if memFound && config.memoryRequestToLimitRatio != 0 {
		// memory is measured in whole bytes.
		// the plugin rounds down to the nearest MiB rather than bytes to improve ease of use for end-users.
		amount := memLimit.Value() * int64(config.memoryRequestToLimitRatio*100) / 100
		// TODO: move into resource.Quantity
		var mod int64
		switch memLimit.Format {
		case resource.BinarySI:
			mod = 1024 * 1024
		default:
			mod = 1000 * 1000
		}
		if rem := amount % mod; rem != 0 {
			amount = amount - rem
		}
		q := resource.NewQuantity(int64(amount), memLimit.Format)
		if memFloor.Cmp(*q) > 0 {
			q = memFloor.Copy()
		}
		if nsMemFloor != nil && q.Cmp(*nsMemFloor) < 0 {
			glog.V(5).Infof("%s: %s pod limit %q below namespace limit; setting limit to %q", api.PluginName, kapi.ResourceMemory, q.String(), nsMemFloor.String())
			q = nsMemFloor.Copy()
		}
		if err := applyQuantity(resources.Requests, kapi.ResourceMemory, *q, mutationAllowed); err != nil {
			return fmt.Errorf("resources.requests.%s %v", kapi.ResourceMemory, err)
		}
	}
	if memFound && config.limitCPUToMemoryRatio != 0 {
		amount := float64(memLimit.Value()) * config.limitCPUToMemoryRatio * cpuBaseScaleFactor
		q := resource.NewMilliQuantity(int64(amount), resource.DecimalSI)
		if cpuFloor.Cmp(*q) > 0 {
			q = cpuFloor.Copy()
		}
		if nsCPUFloor != nil && q.Cmp(*nsCPUFloor) < 0 {
			glog.V(5).Infof("%s: %s pod limit %q below namespace limit; setting limit to %q", api.PluginName, kapi.ResourceCPU, q.String(), nsCPUFloor.String())
			q = nsCPUFloor.Copy()
		}
		if err := applyQuantity(resources.Limits, kapi.ResourceCPU, *q, mutationAllowed); err != nil {
			return fmt.Errorf("resources.limits.%s %v", kapi.ResourceCPU, err)
		}
	}

	cpuLimit, cpuFound := resources.Limits[kapi.ResourceCPU]
	if cpuFound && config.cpuRequestToLimitRatio != 0 {
		amount := float64(cpuLimit.MilliValue()) * config.cpuRequestToLimitRatio
		q := resource.NewMilliQuantity(int64(amount), cpuLimit.Format)
		if cpuFloor.Cmp(*q) > 0 {
			q = cpuFloor.Copy()
		}
		if nsCPUFloor != nil && q.Cmp(*nsCPUFloor) < 0 {
			glog.V(5).Infof("%s: %s pod limit %q below namespace limit; setting limit to %q", api.PluginName, kapi.ResourceCPU, q.String(), nsCPUFloor.String())
			q = nsCPUFloor.Copy()
		}
		if err := applyQuantity(resources.Requests, kapi.ResourceCPU, *q, mutationAllowed); err != nil {
			return fmt.Errorf("resources.requests.%s %v", kapi.ResourceCPU, err)
		}
	}

	return nil
}

func applyQuantity(l kapi.ResourceList, r kapi.ResourceName, v resource.Quantity, mutationAllowed bool) error {
	if mutationAllowed {
		l[r] = v
		return nil
	}

	if oldValue, ok := l[r]; !ok {
		return fmt.Errorf("mutated, expected: %v, now absent", v)
	} else if oldValue.Cmp(v) != 0 {
		return fmt.Errorf("mutated, expected: %v, got %v", v, oldValue)
	}

	return nil
}

// minResourceLimits finds the Min limit for resourceName. Nil is
// returned if limitRanges is empty or limits contains no resourceName
// limits.
func minResourceLimits(limitRanges []*kapi.LimitRange, resourceName kapi.ResourceName) *resource.Quantity {
	limits := []*resource.Quantity{}

	for _, limitRange := range limitRanges {
		for _, limit := range limitRange.Spec.Limits {
			if limit.Type == kapi.LimitTypeContainer {
				if limit, found := limit.Min[resourceName]; found {
					limits = append(limits, limit.Copy())
				}
			}
		}
	}

	if len(limits) == 0 {
		return nil
	}

	return minQuantity(limits)
}

func minQuantity(quantities []*resource.Quantity) *resource.Quantity {
	min := quantities[0].Copy()

	for i := range quantities {
		if quantities[i].Cmp(*min) < 0 {
			min = quantities[i].Copy()
		}
	}

	return min
}
