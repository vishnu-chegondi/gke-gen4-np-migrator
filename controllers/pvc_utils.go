package controllers

import (
	"context"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type PVCAnnotationUpdater struct {
	r         *DeploymentController
	name      string
	namespace string
}

func NewPVCAnnotationUpdater(r *DeploymentController, name, namespace string) *PVCAnnotationUpdater {
	return &PVCAnnotationUpdater{r, name, namespace}
}

func (u *PVCAnnotationUpdater) EnableVolumeClaimAnnotation(ctx context.Context) error {
	// Get the PVC object
	var pvc v1.PersistentVolumeClaim
	if err := u.r.Get(ctx, client.ObjectKey{Name: u.name, Namespace: u.namespace}, &pvc); err != nil {
		return err
	}

	// Add the annotation to the PVC
	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string)
	}

	if _, exists := pvc.Annotations[AnnotationKEY]; exists {
		return nil
	}

	pvc.Annotations[AnnotationKEY] = "enabled"
	if err := u.r.Update(ctx, &pvc); err != nil {
		return err
	}

	return nil
}

// TODO: Update to use snapshot controller to create new PVC from snapshot instead of creating a new PVC directly
func (r *SnapshotController) CreateNewPVC(ctx context.Context, pvc *v1.PersistentVolumeClaim) error {
	logger := log.FromContext(ctx)
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

	logger.Info("Creating new PVC", "pvc", pvcName)
	if err := r.Create(ctx, &newPVC); err != nil {
		return err
	}

	pvc.Annotations[AnnotationKEY] = "new-pvc-created"
	err = r.Update(ctx, pvc)
	return err
}
