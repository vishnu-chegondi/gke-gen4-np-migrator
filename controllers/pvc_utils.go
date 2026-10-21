package controllers

import (
	"context"
	"fmt"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

const PVCAnnotationKey = "gke.gen4.np.codeamenity.in/pvc"

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

func (r *SnapshotController) CreateNewPVC(ctx context.Context, snapshot *snapshotv1.VolumeSnapshot) error {
	logger := log.FromContext(ctx)
	var oldPVCObject v1.PersistentVolumeClaim
	var newPVC v1.PersistentVolumeClaim
	newStorageClass, err := ReadConfigMapKey(ctx, r, "STORAGE_CLASSNAME")
	if err != nil {
		return err
	}

	// Check if PVC already exists
	err = yaml.Unmarshal([]byte(snapshot.Annotations[PVCAnnotationKey]), oldPVCObject)
	if err != nil {
		return err
	}
	pvcNamespacedName := client.ObjectKey{Namespace: oldPVCObject.Namespace, Name: oldPVCObject.Name}
	if err := r.Get(ctx, pvcNamespacedName, &v1.PersistentVolumeClaim{}); err == nil {
		return fmt.Errorf("PVC %s already exists", oldPVCObject.Name)
	}

	newSpec := oldPVCObject.Spec.DeepCopy()
	for _, accessMode := range newPVC.Spec.AccessModes {
		if accessMode == v1.ReadWriteMany {
			newSpec.VolumeMode = new(v1.PersistentVolumeBlock)
			break
		}
	}
	*newSpec.StorageClassName = newStorageClass
	newSpec.DataSource = &v1.TypedLocalObjectReference{
		APIGroup: pointer.String("snapshot.storage.k8s.io"),
		Kind:     "VolumeSnapshot",
		Name:     snapshot.Name,
	}

	newPVC = v1.PersistentVolumeClaim{
		TypeMeta:   oldPVCObject.TypeMeta,
		ObjectMeta: oldPVCObject.ObjectMeta,
		Spec:       *newSpec,
	}

	logger.Info("Creating new PVC", "pvc", oldPVCObject.Name)
	err = r.Create(ctx, &newPVC)
	return err
}
