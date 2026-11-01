// Package controllers provides Kubernetes controller implementations for managing
// PersistentVolumeClaims and orchestrating PVC migration workflows through snapshots.
// The PVCController manages the lifecycle of PVCs during migration, handling snapshot
// creation, completion verification, and cleanup of old PVCs.
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

const (
	// SnapshotCompleted indicates that a VolumeSnapshot has been successfully created
	// and is ready to be used for creating a new PVC. This annotation value triggers
	// the deletion of the old PVC.
	SnapshotCompleted = "snapshot-completed"

	// SnapshotInProgress indicates that a VolumeSnapshot creation has been initiated
	// but not yet completed. The controller will wait for snapshot completion before
	// proceeding to the next stage.
	SnapshotInProgress = "snapshot-in-progress"
)

// PVCController reconciles PersistentVolumeClaim objects to manage PVC migration
// through snapshots. It monitors PVCs with migration annotations and orchestrates
// the snapshot creation, waiting, and cleanup process.
//
// Fields:
//   - Client: Used for updating and deleting PVC objects
//   - APIReader: Provides direct reads from the API server bypassing the cache
//   - Scheme: Runtime scheme for object type registration
type PVCController struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
}

var _ reconcile.Reconciler = &PVCController{}

// ReconcilePVC handles the renaming and annotation update of a PersistentVolumeClaim
// during the migration process. It only processes PVCs that have completed the snapshot
// phase (annotation value is SnapshotCompleted or doesn't exist).
//
// Parameters:
//   - ctx: Context for the operation
//   - pvc: The PersistentVolumeClaim to reconcile
//
// Returns an error if the update operation fails, nil otherwise.
func (r *PVCController) ReconcilePVC(ctx context.Context, pvc *v1.PersistentVolumeClaim) error {
	logger := log.FromContext(ctx)
	if value, exists := pvc.Annotations[AnnotationKEY]; exists && value != SnapshotCompleted {
		return nil
	}

	pvc.Name = pvc.Name + "-migrated"
	pvc.Annotations[AnnotationKEY] = "renamed"
	logger.Info("Renaming PVC", "pvc", pvc.Name)
	err := r.Update(ctx, pvc)
	return err
}

// DeletePVC removes a PersistentVolumeClaim from the cluster after the migration
// process has completed. This is typically called after a snapshot has been successfully
// created and the new PVC has been provisioned.
//
// Parameters:
//   - ctx: Context for the operation
//   - pvc: The PersistentVolumeClaim to delete
//
// Returns an error if the deletion fails, nil otherwise.
func (r *PVCController) DeletePVC(ctx context.Context, pvc *v1.PersistentVolumeClaim) (err error) {
	logger := log.FromContext(ctx)
	logger.Info("Deleting PVC", "pvc", pvc.Name)
	err = r.Delete(ctx, pvc)
	return err
}

// Reconcile manages the PVC migration lifecycle through a state machine based on annotation values.
// It handles three main states:
//   - Enabled: Creates a VolumeSnapshot from the PVC and transitions to SnapshotInProgress
//   - SnapshotInProgress: Waits for snapshot completion and transitions to SnapshotCompleted
//   - SnapshotCompleted: Deletes the old PVC after successful snapshot creation
//
// The method uses goto statements for centralized error handling and annotation updates.
// After each successful state transition, it updates the PVC annotation to reflect the new state.
//
// Parameters:
//   - ctx: Context for the operation, used for logging and API calls
//   - request: Reconcile request containing the NamespacedName of the PVC to reconcile
//
// Returns:
//   - result: Reconcile result (currently always empty, no requeue)
//   - err: Error if any operation fails, nil on success or if PVC is not found
func (r *PVCController) Reconcile(ctx context.Context, request reconcile.Request) (result reconcile.Result, err error) {
	logger := log.FromContext(ctx)
	var message string
	var annotationValue string
	var pvc v1.PersistentVolumeClaim
	if err := r.APIReader.Get(ctx, request.NamespacedName, &pvc); err != nil {
		goto returnResult
	}

	switch pvc.Annotations[AnnotationKEY] {
	case Enabled:
		err = r.CreateSnapshotForPVC(ctx, &pvc)
		message = "Snapshot created"
		annotationValue = SnapshotInProgress
		goto returnResult
	case SnapshotInProgress:
		err = r.WaitForSnapshotCompletion(ctx, &pvc)
		message = "Snapshot completed"
		annotationValue = SnapshotCompleted
		goto returnResult
	case SnapshotCompleted:
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

// SetupWithManager configures the PVCController with the controller manager.
// It sets up a controller that:
//   - Watches PersistentVolumeClaim objects
//   - Allows up to 3 concurrent reconciliations for better throughput
//   - Uses a custom event filter (PVCPredicate) to limit which PVCs trigger reconciliation
//
// Parameters:
//   - mgr: Controller manager that will manage this controller's lifecycle
//
// Returns an error if controller setup fails, nil otherwise.
func (r *PVCController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.PersistentVolumeClaim{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 3}).
		WithEventFilter(PVCPredicate()).
		Complete(r)
}
