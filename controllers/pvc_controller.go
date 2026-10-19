package controllers

import (
	"context"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type PVCController struct {
	client.Client
	Scheme *runtime.Scheme
}

var _ reconcile.Reconciler = &PVCController{}

func (r *PVCController) ShouldRunController(ctx context.Context, pvc *v1.PersistentVolumeClaim) bool {
	logger := log.FromContext(ctx)
	if enabled, exists := pvc.Annotations[AnnotationKEY]; !exists || enabled != "enabled" {
		logger.Info("PVC does not have annotation", "pvc", pvc.Name)
		return false
	}
	oldStorageClasses, err := ReadConfigMapKey(ctx, r, "OLD_STORAGE_CLASSES")
	if err != nil {
		logger.Error(err, "Failed to read OLD_STORAGE_CLASSES from ConfigMap")
		return false
	}

	if pvc.Spec.StorageClassName == nil {
		logger.Info("PVC does not have a storage class", "pvc", pvc.Name)
		return false
	}

	for _, oldStorageClass := range strings.Split(oldStorageClasses, ",") {
		if *pvc.Spec.StorageClassName == oldStorageClass {
			logger.Info("PVC is in the old storage class", "pvc", pvc.Name)
			return true
		}
	}

	return false
}

func (r *PVCController) ReconcilePVC(ctx context.Context, pvc *v1.PersistentVolumeClaim) error {
	logger := log.FromContext(ctx)
	if value, exists := pvc.Annotations[AnnotationKEY]; exists && value != "snapshot-completed" {
		return nil
	}

	pvc.Name = pvc.Name + "-migrated"
	pvc.Annotations[AnnotationKEY] = "renamed"
	logger.Info("Renaming PVC", "pvc", pvc.Name)
	err := r.Update(ctx, pvc)
	return err
}

func (r *PVCController) DeletePVC(ctx context.Context, pvc *v1.PersistentVolumeClaim) error {
	logger := log.FromContext(ctx)
	if value, exists := pvc.Annotations[AnnotationKEY]; exists && value != "new-pvc-created" {
		return nil
	}
	logger.Info("Deleting PVC", "pvc", pvc.Name)
	err := r.Delete(ctx, pvc)
	return err
}

func (r *PVCController) Reconcile(ctx context.Context, request reconcile.Request) (result reconcile.Result, err error) {
	var pvc v1.PersistentVolumeClaim
	if err := r.Get(ctx, request.NamespacedName, &pvc); err != nil {
		err = client.IgnoreNotFound(err)
		goto returnResult
	}

	if !r.ShouldRunController(ctx, &pvc) {
		goto returnResult
	}

	switch pvc.Annotations[AnnotationKEY] {
	case "enabled":
		err = r.CreateSnapshotForPVC(ctx, &pvc)
		goto returnResult
	case "snapshot-in-progress":
		err = r.WaitForSnapshotCompletion(ctx, &pvc)
		goto returnResult
	case "snapshot-completed":
		err = r.DeletePVC(ctx, &pvc)
		goto returnResult
	}

returnResult:
	return result, err

}

func (r *PVCController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.PersistentVolumeClaim{}).
		Complete(r)
}
