package controllers

import (
	"context"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
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
	if _, exists := pvc.Annotations[AnnotationKEY]; !exists {
		return false
	}
	oldStorageClasses, err := ReadConfigMapKey(ctx, r, "OLD_STORAGE_CLASSES")
	if err != nil {
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

func (r *PVCController) ReconcilePVC(ctx context.Context, pvc *v1.PersistentVolumeClaim) error {
	if value, exists := pvc.Annotations[AnnotationKEY]; exists && value != "snapshot-completed" {
		return nil
	}

	pvc.Name = pvc.Name + "-migrated"
	pvc.Annotations[AnnotationKEY] = "renamed"
	err := r.Update(ctx, pvc)
	return err
}

func (r *PVCController) CreateNewPVC(ctx context.Context, pvc *v1.PersistentVolumeClaim) error {
	if value, exists := pvc.Annotations[AnnotationKEY]; exists && value != "renamed" {
		return nil
	}
	newStorageClass, err := ReadConfigMapKey(ctx, r, "STORAGE_CLASSNAME")
	if err != nil {
		return err
	}

	pvcName := strings.TrimSuffix(pvc.Name, "-migrated")
	pvcNamespacedName := client.ObjectKey{
		Namespace: pvc.Namespace,
		Name:      pvcName,
	}
	var newPVC v1.PersistentVolumeClaim

	if err := r.Get(ctx, pvcNamespacedName, &newPVC); err == nil {
		return nil
	}

	newObjectMeta := new(pvc.ObjectMeta)
	newObjectMeta.Name = pvcName

	newSpec := new(pvc.Spec)
	for _, accessMode := range pvc.Spec.AccessModes {
		if accessMode == v1.ReadWriteMany {
			newSpec.VolumeMode = new(v1.PersistentVolumeBlock)
			break
		}
	}
	*newSpec.StorageClassName = newStorageClass
	newSpec.DataSource = &v1.TypedLocalObjectReference{
		APIGroup: pointer.String("snapshot.storage.k8s.io"),
		Kind:     "VolumeSnapshot",
		Name:     pvc.Name + "-snapshot",
	}

	newPVC = v1.PersistentVolumeClaim{
		TypeMeta:   pvc.TypeMeta,
		ObjectMeta: *newObjectMeta,
		Spec:       *newSpec,
	}

	if err := r.Create(ctx, &newPVC); err != nil {
		return err
	}

	pvc.Annotations[AnnotationKEY] = "new-pvc-created"
	err = r.Update(ctx, pvc)
	return err
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
	if err := r.WaitForSnapshotCompletion(ctx, &pvc); err != nil {
		return reconcile.Result{}, err
	}

	// Reconcile the PVC
	if err := r.ReconcilePVC(ctx, &pvc); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.CreateNewPVC(ctx, &pvc); err != nil {
		return reconcile.Result{RequeueAfter: 30 * time.Second}, err
	}

	return reconcile.Result{}, nil
}

func (r *PVCController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.PersistentVolumeClaim{}).
		Complete(r)
}
