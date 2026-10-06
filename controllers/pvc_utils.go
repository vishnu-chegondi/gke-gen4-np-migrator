package controllers

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
