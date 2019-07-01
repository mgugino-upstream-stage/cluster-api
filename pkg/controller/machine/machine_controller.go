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

package machine

import (
	"context"
	"path"
	"time"
	"fmt"

	"github.com/pkg/errors"
	"github.com/go-log/log/info"
	corev1 "k8s.io/api/core/v1"
	//corev1Client "k8s.io/client-go/kubernetes/typed/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubedrain "github.com/openshift/kubernetes-drain"
	controllerError "sigs.k8s.io/cluster-api/pkg/controller/error"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	"k8s.io/utils/pointer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clusterv1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha2"
	capierrors "sigs.k8s.io/cluster-api/pkg/controller/error"
	"sigs.k8s.io/cluster-api/pkg/controller/remote"
	"sigs.k8s.io/cluster-api/pkg/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	// ExcludeNodeDrainingAnnotation annotation explicitly skips node draining if set
	ExcludeNodeDrainingAnnotation = "machine.cluster.sigs.k8s.io/exclude-node-draining"
)

// Add creates a new Machine Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileMachine{
		Client: mgr.GetClient(),
		scheme: mgr.GetScheme(),
		config: mgr.GetConfig(),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("machine_controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Store the controller on the ReconcileMachine instance
	// to dynamically add watchers to external objects at runtime.
	if r, ok := r.(*ReconcileMachine); ok {
		r.controller = c
	}

	// Watch for changes to Machine
	return c.Watch(
		&source.Kind{Type: &clusterv1.Machine{}},
		&handler.EnqueueRequestForObject{},
	)
}

// ReconcileMachine reconciles a Machine object
type ReconcileMachine struct {
	client.Client
	scheme     *runtime.Scheme
	controller controller.Controller
	config *rest.Config
}

// Reconcile reads that state of the cluster for a Machine object and makes changes based on the state read
// and what is in the Machine.Spec
func (r *ReconcileMachine) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	ctx := context.TODO()

	// Fetch the Machine instance
	m := &clusterv1.Machine{}
	if err := r.Client.Get(ctx, request.NamespacedName, m); err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Store Machine early state to allow patching.
	patchMachine := client.MergeFrom(m.DeepCopy())

	// Cluster might be nil as some providers might not require a cluster object
	// for machine management.
	cluster, err := r.getCluster(ctx, m)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Set the ownerRef with foreground deletion if there is a linked cluster.
	if cluster != nil {
		m.OwnerReferences = util.EnsureOwnerRef(m.OwnerReferences, metav1.OwnerReference{
			APIVersion:         cluster.APIVersion,
			Kind:               cluster.Kind,
			Name:               cluster.Name,
			UID:                cluster.UID,
			BlockOwnerDeletion: pointer.BoolPtr(true),
		})
	}

	// If the Machine hasn't been deleted and doesn't have a finalizer, add one.
	if m.ObjectMeta.DeletionTimestamp.IsZero() {
		if !util.Contains(m.Finalizers, clusterv1.MachineFinalizer) {
			m.Finalizers = append(m.ObjectMeta.Finalizers, clusterv1.MachineFinalizer)
			if err := r.Client.Patch(ctx, m, patchMachine); err != nil {
				return reconcile.Result{}, errors.Wrapf(err, "failed to add finalizer to Machine %q in namespace %q", m.Name, m.Namespace)
			}
			// Since adding the finalizer updates the object return to avoid later update issues
			return reconcile.Result{Requeue: true}, nil
		}
	}

	if err := r.reconcile(ctx, m); err != nil {
		if requeueErr, ok := errors.Cause(err).(capierrors.HasRequeueAfterError); ok {
			klog.Infof("Reconciliation for Machine %q in namespace %q asked to requeue: %v", m.Name, m.Namespace, err)
			return reconcile.Result{Requeue: true, RequeueAfter: requeueErr.GetRequeueAfter()}, nil
		}
		return reconcile.Result{}, err
	}

	if !m.ObjectMeta.DeletionTimestamp.IsZero() {
		// Drain node before deletion
		// If a machine is not linked to a node, just delete the machine. Since a node
		// can be unlinked from a machine when the node goes NotReady and is removed
		// by cloud controller manager. In that case some machines would never get
		// deleted without a manual intervention.
		if _, exists := m.ObjectMeta.Annotations[ExcludeNodeDrainingAnnotation]; !exists && m.Status.NodeRef != nil {
			if err := r.drainNode(cluster, m); err != nil {
				return reconcile.Result{}, err
			}
		}
		if m.Status.NodeRef != nil {
			klog.Infof("Deleting Node %q for Machine %q in namespace %q", m.Status.NodeRef.Name, m.Name, m.Namespace)
			if err := r.deleteNode(ctx, cluster, m.Status.NodeRef.Name); err != nil && !apierrors.IsNotFound(err) {
				return reconcile.Result{}, errors.Wrapf(err, "failed to delete Node %q for Machine %q in namespace %q",
					m.Status.NodeRef.Namespace, m.Name, m.Namespace)
			}
		}

		// Remove finalizer on machine when both the references to Infrastructure and Bootstrap are non-existent.
		var bootstrapExists, infrastructureExists bool
		if m.Spec.Bootstrap.ConfigRef != nil {
			if _, err := r.getExternal(ctx, m.Spec.Bootstrap.ConfigRef, m.Namespace); err != nil && !apierrors.IsNotFound(err) {
				return reconcile.Result{}, errors.Wrapf(err, "failed to get %s %q for Machine %q in namespace %q",
					path.Join(m.Spec.Bootstrap.ConfigRef.APIVersion, m.Spec.Bootstrap.ConfigRef.Kind),
					m.Spec.Bootstrap.ConfigRef.Name, m.Name, m.Namespace)
			}
			bootstrapExists = true
		}

		if _, err := r.getExternal(ctx, &m.Spec.InfrastructureRef, m.Namespace); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, errors.Wrapf(err, "failed to get %s %q for Machine %q in namespace %q",
				path.Join(m.Spec.InfrastructureRef.APIVersion, m.Spec.InfrastructureRef.Kind),
				m.Spec.InfrastructureRef.Name, m.Name, m.Namespace)
		} else if err == nil {
			infrastructureExists = true
		}

		if !bootstrapExists && !infrastructureExists {
			m.ObjectMeta.Finalizers = util.Filter(m.ObjectMeta.Finalizers, clusterv1.MachineFinalizer)
		}
	}

	// Patch the Machine and its status.
	if err := r.Client.Patch(ctx, m, patchMachine); err != nil {
		return reconcile.Result{}, err
	}
	if err := r.Client.Status().Patch(ctx, m, patchMachine); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileMachine) getCluster(ctx context.Context, machine *clusterv1.Machine) (*clusterv1.Cluster, error) {
	if machine.Labels[clusterv1.MachineClusterLabelName] == "" {
		klog.Infof("Machine %q in namespace %q doesn't specify %q label, assuming nil cluster", machine.Name, machine.Namespace, clusterv1.MachineClusterLabelName)
		return nil, nil
	}

	cluster := &clusterv1.Cluster{}
	key := client.ObjectKey{
		Namespace: machine.Namespace,
		Name:      machine.Labels[clusterv1.MachineClusterLabelName],
	}

	if err := r.Client.Get(ctx, key, cluster); err != nil {
		return nil, err
	}

	return cluster, nil
}

func (r *ReconcileMachine) deleteNode(ctx context.Context, cluster *clusterv1.Cluster, name string) error {
	if cluster == nil {
		// Try to retrieve the Node from the local cluster, if no Cluster reference is found.
		var node corev1.Node
		if err := r.Client.Get(ctx, client.ObjectKey{Name: name}, &node); err != nil {
			return err
		}
		return r.Client.Delete(ctx, &node)
	}

	// Otherwise, proceed to get the remote cluster client and get the Node.
	remoteClient, err := remote.NewClusterClient(r.Client, cluster)
	if err != nil {
		klog.Errorf("Error creating a remote client for cluster %q while deleting Machine %q, won't retry: %v",
			cluster.Name, name, err)
		return nil
	}

	corev1Remote, err := remoteClient.CoreV1()
	if err != nil {
		klog.Errorf("Error creating a remote client for cluster %q while deleting Machine %q, won't retry: %v",
			cluster.Name, name, err)
		return nil
	}

	return corev1Remote.Nodes().Delete(name, &metav1.DeleteOptions{})
}

func (r *ReconcileMachine) drainNode(cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	var kubeClient kubernetes.Interface
	if cluster == nil {
		var err error
		kubeClient, err = kubernetes.NewForConfig(r.config)
		if err != nil {
			return fmt.Errorf("unable to build kube client: %v", err)
		}
	}  else {
		// Otherwise, proceed to get the remote cluster client and get the Node.
		remoteClient, err := remote.NewClusterClient(r.Client, cluster)
		if err != nil {
			klog.Errorf("Error creating a remote client for cluster %q while deleting Machine %q, won't retry: %v",
				cluster.Name, machine.Name, err)
			return nil
		}
		var err2 error
		kubeClient, err2 = kubernetes.NewForConfig(remoteClient.RESTConfig())
		if err2 != nil {
			klog.Errorf("Error creating a remote client for cluster %q while deleting Machine %q, won't retry: %v",
				cluster.Name, machine.Name, err)
			return nil
		}
	}

	node, err := kubeClient.CoreV1().Nodes().Get(machine.Status.NodeRef.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to get node %q: %v", machine.Status.NodeRef.Name, err)
	}

	if err := kubedrain.Drain(
		kubeClient,
		[]*corev1.Node{node},
		&kubedrain.DrainOptions{
			Force:              true,
			IgnoreDaemonsets:   true,
			DeleteLocalData:    true,
			GracePeriodSeconds: -1,
			Logger:             info.New(klog.V(0)),
			// If a pod is not evicted in 20 second, retry the eviction next time the
			// machine gets reconciled again (to allow other machines to be reconciled)
			Timeout: 20 * time.Second,
		},
	); err != nil {
		// Machine still tries to terminate after drain failure
		klog.Warningf("drain failed for machine %q: %v", machine.Name, err)
		return &controllerError.RequeueAfterError{RequeueAfter: 20 * time.Second}
	}

	klog.Infof("drain successful for machine %q", machine.Name)

	return nil
}
