package controllers

import (
	"context"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
)

func (r *PVCController) CreateSnapshotForPVC(ctx context.Context, pvc *v1.PersistentVolumeClaim) error {
	if value, exists := pvc.Annotations[AnnotationKEY]; exists && value != "enabled" {
		return nil
	}
	newSnapshotClass, err := ReadConfigMapKey(ctx, r, "VOLUME_SNAPHOT_CLASS")
	if err != nil {
		return err
	}

	snapshotObjectKey := types.NamespacedName{
		Namespace: pvc.Namespace,
		Name:      pvc.Name + "-snapshot",
	}

	if err := r.Get(ctx, snapshotObjectKey, &snapshotv1.VolumeSnapshot{}); err == nil {
		return nil
	}

	snapshot := snapshotv1.VolumeSnapshot{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VolumeSnapshot",
			APIVersion: "snapshot.storage.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvc.Name + "-snapshot",
			Namespace: pvc.Namespace,
		},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source: snapshotv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: &pvc.Name,
			},
			VolumeSnapshotClassName: pointer.String(newSnapshotClass),
		},
	}

	if err := r.Create(ctx, &snapshot); err != nil {
		return err
	}

	pvc.Annotations[AnnotationKEY] = "snapshot-in-progress"
	err = r.Update(ctx, pvc)

	return err
}

func (r *PVCController) WaitForSnapshotCompletion(ctx context.Context, pvc *v1.PersistentVolumeClaim) error {
	if value, exists := pvc.Annotations[AnnotationKEY]; exists && value != "snapshot-in-progress" {
		return nil
	}

	var snapshot snapshotv1.VolumeSnapshot
	snapshotObjectKey := types.NamespacedName{
		Namespace: pvc.Namespace,
		Name:      pvc.Name + "-snapshot",
	}
	for {
		if err := r.Get(ctx, snapshotObjectKey, &snapshot); err != nil {
			return err
		}
		if snapshot.Status != nil && snapshot.Status.ReadyToUse != nil && *snapshot.Status.ReadyToUse {
			pvc.Annotations[AnnotationKEY] = "snapshot-completed"
			err := r.Update(ctx, pvc)
			return err
		}
		time.Sleep(10 * time.Second)
	}
}
