package controllers

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type PVCController struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
}

var _ reconcile.Reconciler = &PVCController{}

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
	logger.Info("Deleting PVC", "pvc", pvc.Name)
	err := r.Delete(ctx, pvc)
	return err
}

func (r *PVCController) Reconcile(ctx context.Context, request reconcile.Request) (result reconcile.Result, err error) {
	logger := log.FromContext(ctx)
	var message string
	var annotationValue string
	var pvc v1.PersistentVolumeClaim
	if err := r.APIReader.Get(ctx, request.NamespacedName, &pvc); err != nil {
		err = client.IgnoreNotFound(err)
		goto returnResult
	}

	switch pvc.Annotations[AnnotationKEY] {
	case "enabled":
		err = r.CreateSnapshotForPVC(ctx, &pvc)
		message = "Snapshot created"
		annotationValue = "snapshot-in-progress"
		goto returnResult
	case "snapshot-in-progress":
		err = r.WaitForSnapshotCompletion(ctx, &pvc)
		message = "Snapshot completed"
		annotationValue = "snapshot-completed"
		goto returnResult
	case "snapshot-completed":
		err = r.DeletePVC(ctx, &pvc)
		if client.IgnoreNotFound(err) != nil {
			err = nil
		}
		message = "PVC deleted"
		annotationValue = ""
		goto returnResult
	}

returnResult:
	if err != nil {
		message = fmt.Sprintf("Error reconciling PVC: %v", err)
	} else {
		if annotationValue != "" {
			logger.Info("Updating PVC annotation", "pvc", pvc.Name, "annotation", AnnotationKEY, "value", annotationValue)
			if err := r.APIReader.Get(ctx, request.NamespacedName, &pvc); err != nil {
				err = client.IgnoreNotFound(err)
				return result, err
			}
			pvc.Annotations[AnnotationKEY] = annotationValue
			err = r.Update(ctx, &pvc)
		}
	}
	logger.Info(message, "pvc", pvc.Name)
	return result, err

}

func (r *PVCController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.PersistentVolumeClaim{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 3}).
		WithEventFilter(PVCPredicate()).
		Complete(r)
}
