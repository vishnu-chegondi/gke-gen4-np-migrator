package controllers

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type PVCController struct {
	client.Client
	Scheme *runtime.Scheme
}

var _ reconcile.Reconciler = &PVCController{}

func (r *PVCController) InitializePVCController(ctx context.Context, pvc *v1.PersistentVolumeClaim) bool {
	_, exists := pvc.Annotations[AnnotationKEY]
	if !exists {
		return false
	}

	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "standard-rwo" {
		return false
	}

	return true
}

func (r *PVCController) ReconcilePVC(ctx context.Context, pvc *v1.PersistentVolumeClaim) error {
	return nil
}

func (r *PVCController) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	var pvc v1.PersistentVolumeClaim
	if err := r.Get(ctx, request.NamespacedName, &pvc); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if !r.InitializePVCController(ctx, &pvc) {
		return reconcile.Result{}, nil
	}

	if err := r.CreateSnapshotForPVC(ctx, &pvc); err != nil {
		return reconcile.Result{}, err
	}

	// Reconcile the PVC
	if err := r.ReconcilePVC(ctx, &pvc); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *PVCController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.PersistentVolumeClaim{}).
		Complete(r)
}
