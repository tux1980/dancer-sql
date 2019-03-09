// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	operator_v1 "github.com/openshift/api/operator/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeEtcds implements EtcdInterface
type FakeEtcds struct {
	Fake *FakeOperatorV1
}

var etcdsResource = schema.GroupVersionResource{Group: "operator.openshift.io", Version: "v1", Resource: "etcds"}

var etcdsKind = schema.GroupVersionKind{Group: "operator.openshift.io", Version: "v1", Kind: "Etcd"}

// Get takes name of the etcd, and returns the corresponding etcd object, and an error if there is any.
func (c *FakeEtcds) Get(name string, options v1.GetOptions) (result *operator_v1.Etcd, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootGetAction(etcdsResource, name), &operator_v1.Etcd{})
	if obj == nil {
		return nil, err
	}
	return obj.(*operator_v1.Etcd), err
}

// List takes label and field selectors, and returns the list of Etcds that match those selectors.
func (c *FakeEtcds) List(opts v1.ListOptions) (result *operator_v1.EtcdList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootListAction(etcdsResource, etcdsKind, opts), &operator_v1.EtcdList{})
	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &operator_v1.EtcdList{ListMeta: obj.(*operator_v1.EtcdList).ListMeta}
	for _, item := range obj.(*operator_v1.EtcdList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested etcds.
func (c *FakeEtcds) Watch(opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchAction(etcdsResource, opts))
}

// Create takes the representation of a etcd and creates it.  Returns the server's representation of the etcd, and an error, if there is any.
func (c *FakeEtcds) Create(etcd *operator_v1.Etcd) (result *operator_v1.Etcd, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootCreateAction(etcdsResource, etcd), &operator_v1.Etcd{})
	if obj == nil {
		return nil, err
	}
	return obj.(*operator_v1.Etcd), err
}

// Update takes the representation of a etcd and updates it. Returns the server's representation of the etcd, and an error, if there is any.
func (c *FakeEtcds) Update(etcd *operator_v1.Etcd) (result *operator_v1.Etcd, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateAction(etcdsResource, etcd), &operator_v1.Etcd{})
	if obj == nil {
		return nil, err
	}
	return obj.(*operator_v1.Etcd), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeEtcds) UpdateStatus(etcd *operator_v1.Etcd) (*operator_v1.Etcd, error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateSubresourceAction(etcdsResource, "status", etcd), &operator_v1.Etcd{})
	if obj == nil {
		return nil, err
	}
	return obj.(*operator_v1.Etcd), err
}

// Delete takes name of the etcd and deletes it. Returns an error if one occurs.
func (c *FakeEtcds) Delete(name string, options *v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewRootDeleteAction(etcdsResource, name), &operator_v1.Etcd{})
	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeEtcds) DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error {
	action := testing.NewRootDeleteCollectionAction(etcdsResource, listOptions)

	_, err := c.Fake.Invokes(action, &operator_v1.EtcdList{})
	return err
}

// Patch applies the patch and returns the patched etcd.
func (c *FakeEtcds) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *operator_v1.Etcd, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootPatchSubresourceAction(etcdsResource, name, data, subresources...), &operator_v1.Etcd{})
	if obj == nil {
		return nil, err
	}
	return obj.(*operator_v1.Etcd), err
}
