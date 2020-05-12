/*
Copyright 2018 The Kubernetes Authors.

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

// Code generated by informer-gen. DO NOT EDIT.

package v1beta1

import (
	time "time"

	machinev1beta1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	clientset "github.com/openshift/cluster-api/pkg/client/clientset_generated/clientset"
	internalinterfaces "github.com/openshift/cluster-api/pkg/client/informers_generated/externalversions/internalinterfaces"
	v1beta1 "github.com/openshift/cluster-api/pkg/client/listers_generated/machine/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// MachineReplicaSetInformer provides access to a shared informer and lister for
// MachineReplicaSets.
type MachineReplicaSetInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1beta1.MachineReplicaSetLister
}

type machineReplicaSetInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	namespace        string
}

// NewMachineReplicaSetInformer constructs a new informer for MachineReplicaSet type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewMachineReplicaSetInformer(client clientset.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredMachineReplicaSetInformer(client, namespace, resyncPeriod, indexers, nil)
}

// NewFilteredMachineReplicaSetInformer constructs a new informer for MachineReplicaSet type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredMachineReplicaSetInformer(client clientset.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.MachineV1beta1().MachineReplicaSets(namespace).List(options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.MachineV1beta1().MachineReplicaSets(namespace).Watch(options)
			},
		},
		&machinev1beta1.MachineReplicaSet{},
		resyncPeriod,
		indexers,
	)
}

func (f *machineReplicaSetInformer) defaultInformer(client clientset.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredMachineReplicaSetInformer(client, f.namespace, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *machineReplicaSetInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&machinev1beta1.MachineReplicaSet{}, f.defaultInformer)
}

func (f *machineReplicaSetInformer) Lister() v1beta1.MachineReplicaSetLister {
	return v1beta1.NewMachineReplicaSetLister(f.Informer().GetIndexer())
}
