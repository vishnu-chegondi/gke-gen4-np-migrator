package controllers

import (
	"context"
	"fmt"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

func (r *PVCController) CreateSnapshotForPVC(ctx context.Context, pvc *v1.PersistentVolumeClaim) (err error) {
	logger := log.FromContext(ctx)
	var newSnapshotClass string
	var snapshotObjectKey types.NamespacedName
	var pvcYaml []byte
	var snapshot snapshotv1.VolumeSnapshot

	newSnapshotClass, err = ReadConfigMapKey(ctx, r, "VOLUME_SNAPHOT_CLASS")
	if err != nil {
		goto returnError
	}

	snapshotObjectKey = types.NamespacedName{
		Namespace: pvc.Namespace,
		Name:      pvc.Name + "-snapshot",
	}

	err = r.Get(ctx, snapshotObjectKey, &snapshotv1.VolumeSnapshot{})
	if err == nil {
		err = fmt.Errorf("DUPLICATE: Snapshot Already Exists")
		goto returnError
	}

	pvcYaml, err = yaml.Marshal(pvc)
	if err != nil {
		goto returnError
	}

	snapshot = snapshotv1.VolumeSnapshot{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VolumeSnapshot",
			APIVersion: "snapshot.storage.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvc.Name + "-snapshot",
			Namespace: pvc.Namespace,
			Annotations: map[string]string{
				AnnotationKEY:    "enabled",
				PVCAnnotationKey: string(pvcYaml),
			},
		},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source: snapshotv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: &pvc.Name,
			},
			VolumeSnapshotClassName: pointer.String(newSnapshotClass),
		},
	}

	err = r.Create(ctx, &snapshot)
	if err != nil {
		goto returnError
	}

	logger.Info("Snapshot created", "snapshot", snapshotObjectKey)
	pvc.Annotations[AnnotationKEY] = "snapshot-in-progress"
	err = r.Update(ctx, pvc)
	goto returnError

returnError:
	return err
}

func (r *PVCController) WaitForSnapshotCompletion(ctx context.Context, pvc *v1.PersistentVolumeClaim) error {
	if value, exists := pvc.Annotations[AnnotationKEY]; exists && value != "snapshot-in-progress" {
		return nil
	}
	logger := log.FromContext(ctx)

	snapshotObjectKey := types.NamespacedName{
		Namespace: pvc.Namespace,
		Name:      pvc.Name + "-snapshot",
	}
	for {
		var snapshot snapshotv1.VolumeSnapshot
		if err := r.Get(ctx, snapshotObjectKey, &snapshot); err != nil {
			return err
		}
		if SnapshotAvailable(&snapshot) {
			logger.Info("Snapshot completed", "snapshot", snapshotObjectKey)
			freshPVC := v1.PersistentVolumeClaim{}
			if err := r.Get(ctx, types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}, &freshPVC); err != nil {
				return err
			}
			freshPVC.Annotations[AnnotationKEY] = "snapshot-completed"
			err := r.Update(ctx, &freshPVC)
			return err
		}
		time.Sleep(60 * time.Second)
	}

}

func SnapshotAvailable(snapshot *snapshotv1.VolumeSnapshot) bool {
	return snapshot.Status != nil && snapshot.Status.ReadyToUse != nil && *snapshot.Status.ReadyToUse == true
}
