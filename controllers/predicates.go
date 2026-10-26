package controllers

import (
	"os"
	"reflect"
	"strings"

	appv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

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

	for _, oldStorageClass := range strings.Split(oldStorageClasses, ",") {
		if *pvc.Spec.StorageClassName == oldStorageClass {
			return true
		}
	}
	return false
}

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

func PVCPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			if _, ok := e.Object.GetAnnotations()[AnnotationKEY]; ok {
				if hasRequiredConfigs(e.Object) {
					return true
				}
				return false
			}
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld.GetAnnotations()[AnnotationKEY] != e.ObjectNew.GetAnnotations()[AnnotationKEY] {
				if hasRequiredConfigs(e.ObjectNew) {
					return true
				}
				return false
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

func SnapshotPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			if _, ok := e.Object.GetAnnotations()[AnnotationKEY]; ok {
				return true
			}
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld.GetAnnotations()[AnnotationKEY] != e.ObjectNew.GetAnnotations()[AnnotationKEY] {
				return true
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
