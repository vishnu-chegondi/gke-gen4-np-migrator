package controllers

import (
	"context"
	"fmt"
	"os"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

// CreateSnapshotForPVC creates a VolumeSnapshot from a PersistentVolumeClaim.
// It requires the VOLUME_SNAPHOT_CLASS environment variable to be set.
// The snapshot is created with the name "{pvc-name}-snapshot" in the same namespace as the PVC.
// The original PVC metadata is stored in the snapshot's annotations for later restoration.
// If a snapshot with the same name already exists, the function returns without error.
//
// Parameters:
//   - ctx: The context for the operation
//   - pvc: The PersistentVolumeClaim to create a snapshot from
//
// Returns an error if:
//   - VOLUME_SNAPHOT_CLASS environment variable is not set
//   - PVC metadata cannot be marshaled to YAML
//   - Snapshot creation fails
func (r *PVCController) CreateSnapshotForPVC(ctx context.Context, pvc *v1.PersistentVolumeClaim) (err error) {
	logger := log.FromContext(ctx)
	var newSnapshotClass string
	var snapshotObjectKey types.NamespacedName
	var pvcYaml []byte
	var snapshot snapshotv1.VolumeSnapshot

	newSnapshotClass = os.Getenv("VOLUME_SNAPHOT_CLASS")
	if newSnapshotClass == "" {
		logger.Error(nil, "VOLUME_SNAPHOT_CLASS environment variable is not set")
		return fmt.Errorf("VOLUME_SNAPHOT_CLASS environment variable is not set")
	}

	snapshotObjectKey = types.NamespacedName{
		Namespace: pvc.Namespace,
		Name:      pvc.Name + "-snapshot",
	}

	err = r.APIReader.Get(ctx, snapshotObjectKey, &snapshotv1.VolumeSnapshot{})
	if err == nil {
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
			VolumeSnapshotClassName: new(newSnapshotClass),
		},
	}

	err = r.Create(ctx, &snapshot)
	goto returnError

returnError:
	if err != nil {
		logger.Error(err, "Error creating snapshot")
	} else {
		logger.Info("Snapshot created", "snapshot", snapshotObjectKey)
	}
	return err
}

// WaitForSnapshotCompletion polls for the completion of a VolumeSnapshot associated with a PVC.
// It only waits if the PVC has the migration annotation set to "snapshot-in-progress".
// The function polls every 60 seconds until the snapshot's ReadyToUse status becomes true.
// Once complete, it updates the PVC's annotation to SnapshotCompleted.
//
// Parameters:
//   - ctx: The context for the operation
//   - pvc: The PersistentVolumeClaim whose snapshot completion is being waited for
//
// Returns an error if:
//   - Unable to get the fresh PVC object
//   - Unable to update the PVC annotation after snapshot completion
//
// The function runs indefinitely until the snapshot is ready or an error occurs.
func (r *PVCController) WaitForSnapshotCompletion(ctx context.Context, pvc *v1.PersistentVolumeClaim) error {
	if value, exists := pvc.Annotations[AnnotationKEY]; exists && value != "snapshot-in-progress" {
		return nil
	}
	logger := log.FromContext(ctx)

	snapshotObjectKey := types.NamespacedName{
		Namespace: pvc.Namespace,
		Name:      pvc.Name + "-snapshot",
	}
	logger.Info("Waiting for snapshot to complete", "snapshot", snapshotObjectKey)
	for {
		var snapshot snapshotv1.VolumeSnapshot
		_ = r.APIReader.Get(ctx, snapshotObjectKey, &snapshot)
		if SnapshotAvailable(&snapshot) {
			logger.Info("Snapshot completed", "snapshot", snapshotObjectKey)
			freshPVC := v1.PersistentVolumeClaim{}
			pvcNamespacedName := types.NamespacedName{Name: pvc.Name, Namespace: pvc.Namespace}
			if err := r.APIReader.Get(ctx, pvcNamespacedName, &freshPVC); err != nil {
				return err
			}
			freshPVC.Annotations[AnnotationKEY] = SnapshotCompleted
			err := r.Update(ctx, &freshPVC)
			return err
		}
		logger.Info("Snapshot not yet available", "snapshot", snapshotObjectKey)
		time.Sleep(60 * time.Second)
	}
}

// SnapshotAvailable checks if a VolumeSnapshot is ready to use.
// A snapshot is considered available when its Status is not nil,
// ReadyToUse is not nil, and ReadyToUse is true.
//
// Parameters:
//   - snapshot: The VolumeSnapshot to check
//
// Returns:
//   - true if the snapshot is ready to use
//   - false otherwise
func SnapshotAvailable(snapshot *snapshotv1.VolumeSnapshot) bool {
	return snapshot.Status != nil && snapshot.Status.ReadyToUse != nil && *snapshot.Status.ReadyToUse
}
