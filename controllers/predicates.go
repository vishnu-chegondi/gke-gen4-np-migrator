// Package controllers provide Kubernetes controller implementations for managing
// PersistentVolumeClaims, Deployments, StatefulSets, and VolumeSnapshots.
// This file contains predicate functions that filter Kubernetes events to determine
// which resources should trigger reconciliation loops.
package controllers

import (
	"os"
	"reflect"
	"slices"
	"strings"

	appv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// hasRequiredConfigs checks if a PersistentVolumeClaim has the required storage class
// configuration for migration. It validates that the OLD_STORAGE_CLASSES environment
// variable is set and that the PVC's storage class matches one of the configured old
// storage classes that should be migrated.
//
// Parameters:
//   - obj: The Kubernetes object to check (expected to be a PersistentVolumeClaim)
//
// Returns:
//   - true if the object is a PVC with a storage class that matches one of the
//     old storage classes configured for migration
//   - false if the environment variable is not set, the object is not a PVC,
//     the PVC has no storage class, or the storage class doesn't match
func hasRequiredConfigs(obj client.Object) bool {
	logger := ctrl.Log.WithName("predicates").WithName("CodeAmenity")
	oldStorageClasses := os.Getenv("OLD_STORAGE_CLASSES")
	if oldStorageClasses == "" {
		logger.Error(nil, "OLD_STORAGE_CLASSES environment variable is not set")
		return false
	}

	pvc, okPVC := obj.(*v1.PersistentVolumeClaim)
	if !okPVC {
		return false
	}

	if pvc.Spec.StorageClassName == nil {
		return false
	}

	storageClassSeq := strings.Split(oldStorageClasses, ",")
	return slices.Contains(storageClassSeq, *pvc.Spec.StorageClassName)
}

// DeploymentPredicate returns a predicate that filters Deployment events for reconciliation.
// It triggers reconciliation when:
//   - A Deployment is created with the migration annotation
//   - A Deployment's migration annotation changes
//   - A Deployment's volumes specification changes
//
// Delete and Generic events are ignored and do not trigger reconciliation.
func DeploymentPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			// Only reconcile if the deployment has the annotation
			if _, ok := e.Object.GetAnnotations()[AnnotationKEY]; ok {
				return true
			}
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld.GetAnnotations()[AnnotationKEY] != e.ObjectNew.GetAnnotations()[AnnotationKEY] {
				return true
			}

			oldDeploy, okOld := e.ObjectOld.(*appv1.Deployment)
			newDeploy, okNew := e.ObjectNew.(*appv1.Deployment)

			if !okOld || !okNew {
				return false
			}

			oldVolumes := oldDeploy.Spec.Template.Spec.Volumes
			newVolumes := newDeploy.Spec.Template.Spec.Volumes

			// If the volumes have changed, return true to trigger the reconciling.
			return !reflect.DeepEqual(oldVolumes, newVolumes)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}

// PVCPredicate returns a predicate that filters PersistentVolumeClaim events for reconciliation.
// It triggers reconciliation when:
//   - A PVC is created with the migration annotation and matches the required storage class configuration
//   - A PVC's migration annotation changes, and the new state matches the required storage class configuration
//
// Delete and Generic events are ignored and do not trigger reconciliation.
func PVCPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			if _, ok := e.Object.GetAnnotations()[AnnotationKEY]; ok {
				return hasRequiredConfigs(e.Object)
			}
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld.GetAnnotations()[AnnotationKEY] != e.ObjectNew.GetAnnotations()[AnnotationKEY] {
				return hasRequiredConfigs(e.ObjectNew)
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}

// SnapshotPredicate returns a predicate that filters VolumeSnapshot events for reconciliation.
// It triggers reconciliation when:
//   - A VolumeSnapshot is created with the migration annotation
//   - A VolumeSnapshot's migration annotation changes
//
// Delete and Generic events are ignored and do not trigger reconciliation.
func SnapshotPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, ok := e.Object.GetAnnotations()[AnnotationKEY]
			return ok
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectOld.GetAnnotations()[AnnotationKEY] != e.ObjectNew.GetAnnotations()[AnnotationKEY]
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}
