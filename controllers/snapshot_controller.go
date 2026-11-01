package controllers

import (
	"context"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// SnapshotController reconciles VolumeSnapshot objects to automate the migration
// of PersistentVolumeClaims from one storage class to another.
// It monitors snapshot readiness, waits for PVC deletion, and creates new PVCs from snapshots.
type SnapshotController struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
}

var _ reconcile.Reconciler = &SnapshotController{}

const (
	// Available indicates that a VolumeSnapshot is ready and bound.
	Available = "available"
	// PVCCreated indicates that a new PVC has been successfully created from the snapshot.
	PVCCreated = "pvc-created"
)

// UpdateAnnotation updates the migration annotation on a VolumeSnapshot with the given value.
// If the value is empty, no update is performed.
// Returns an error if the update operation fails.
func (r *SnapshotController) UpdateAnnotation(
	ctx context.Context,
	snapshot *snapshotv1.VolumeSnapshot,
	value string,
) error {
	if value == "" {
		return nil
	}
	snapshot.Annotations[AnnotationKEY] = value
	return r.Update(ctx, snapshot)
}

// Reconcile implements the reconciliation loop for VolumeSnapshot resources.
// It handles three annotation states: Enabled, Available, and PVCCreated.
// - Enabled: waits for snapshot to become available
// - Available: waits for original PVC deletion and creates new PVC from snapshot
// - PVCCreated: reconciliation complete
// Returns a Result indicating whether to requeue and any error encountered.
func (r *SnapshotController) Reconcile(ctx context.Context,
	request reconcile.Request,
) (result reconcile.Result, err error) {
	logger := log.FromContext(ctx)
	var snapshot snapshotv1.VolumeSnapshot
	var annotationValue string
	if err := r.APIReader.Get(ctx, request.NamespacedName, &snapshot); err != nil {
		goto returnResult
	}

	switch snapshot.Annotations[AnnotationKEY] {
	case Enabled:
		for !SnapshotAvailable(&snapshot) {
			time.Sleep(60 * time.Second)
			if err := r.APIReader.Get(ctx, request.NamespacedName, &snapshot); err != nil {
				goto returnResult
			}
		}
		annotationValue = Available
		goto returnResult
	case Available:
		err = waitForPVCDeletion(ctx, r.APIReader, *snapshot.Spec.Source.PersistentVolumeClaimName, snapshot.Namespace)
		if err != nil {
			result.RequeueAfter = 60 * time.Second
			goto returnResult
		}
		err = r.CreateNewPVC(ctx, &snapshot)
		if err == nil {
			annotationValue = PVCCreated
		}
		goto returnResult
	case PVCCreated:
		return result, err
	}

returnResult:
	if err != nil {
		logger.Error(err, "Error reconciling snapshot")
		result = reconcile.Result{RequeueAfter: 60 * time.Second}
	}
	err = r.UpdateAnnotation(ctx, &snapshot, annotationValue)
	return result, err
}

// SetupWithManager registers the SnapshotController with the controller manager.
// It configures the controller to watch VolumeSnapshot resources with a maximum of 3
// concurrent reconciliations and applies a custom event filter predicate.
func (r *SnapshotController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1.VolumeSnapshot{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 3}).
		WithEventFilter(SnapshotPredicate()).
		Complete(r)
}
