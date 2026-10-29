package controllers

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// StsController manages the migration of StatefulSets from old storage classes to new ones.
// It orchestrates a multiphase migration process: scaling down, annotating volumes,
// waiting for migration completion, and scaling back up.
type StsController struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
}

var _ reconcile.Reconciler = &StsController{}

// GetStatefulSet retrieves a StatefulSet by name and namespace using the API reader.
// Returns the StatefulSet object or an error if not found.
func (r *StsController) GetStatefulSet(ctx context.Context, name, namespace string) (*v1.StatefulSet, error) {
	sts := &v1.StatefulSet{}
	err := r.APIReader.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, sts)
	return sts, err
}

// GetSTSReplicaCount returns the StatefulSet's replica count.
// It uses the replica specification when set, otherwise falls back to the
// ReplicaAnnotationKey annotation, and returns 0 if neither provides a valid count.
func GetSTSReplicaCount(sts *v1.StatefulSet) int32 {
	if sts.Spec.Replicas != nil && *sts.Spec.Replicas > 0 {
		return *sts.Spec.Replicas
	}

	if val, ok := sts.Annotations[ReplicaAnnotationKey]; ok {
		if intVal, err := strconv.Atoi(val); err == nil {
			return int32(intVal)
		}
	}
	return 0
}

// HasUnMigratedVolumes checks if the StatefulSet has any volumes that still need migration.
// It reads the OLD_STORAGE_CLASSES environment variable (comma-separated list) and checks
// if any PVC uses an old storage class without the migration annotation enabled.
// Returns true if any unmigrated volumes are found, false otherwise.
func (r *StsController) HasUnMigratedVolumes(ctx context.Context, sts *v1.StatefulSet) bool {
	logger := log.FromContext(ctx)
	oldStorageClasses := os.Getenv("OLD_STORAGE_CLASSES")
	if oldStorageClasses == "" {
		logger.Error(nil, "OLD_STORAGE_CLASSES environment variable is not set")
		return false
	}
	for _, volume := range sts.Spec.VolumeClaimTemplates {
		for replicaCount := int32(0); replicaCount < GetSTSReplicaCount(sts); replicaCount++ {
			var pvc corev1.PersistentVolumeClaim
			var volumeName string

			volumeName = fmt.Sprintf("%s-%s-%d", volume.Name, sts.Name, replicaCount)
			pvcNamespacedName := client.ObjectKey{Name: volumeName, Namespace: sts.Namespace}
			if err := r.APIReader.Get(ctx, pvcNamespacedName, &pvc); err != nil {
				logger.Info("PVC does not exist", "pvc", volumeName)
				return true
			}
			for scName := range strings.SplitSeq(oldStorageClasses, ",") {
				if *pvc.Spec.StorageClassName == scName && pvc.Annotations[AnnotationKEY] != Enabled {
					logger.Info("PVC is in the old storage class", "pvc", volumeName)
					return true
				}
			}
		}
	}
	return false
}

// ScaleDownSTS scales down the StatefulSet to 0 replicas in preparation for migration.
// It performs three operations:
// 1. Saves the current replica count in the ReplicaAnnotationKey annotation
// 2. Sets replicas to 0
// 3. Adds tolerations from the TOLERATIONS environment variable to the pod template
// Returns an error if the TOLERATIONS environment variable is not set or update fails.
func (r *StsController) ScaleDownSTS(ctx context.Context, sts *v1.StatefulSet) error {
	logger := log.FromContext(ctx)
	if sts.Spec.Replicas != nil && *sts.Spec.Replicas > 0 {
		logger.Info("Updating the replicated annotation if replica count is greater than 0")
		sts.Annotations[ReplicaAnnotationKey] = string(*sts.Spec.Replicas)
	}

	logger.Info("Scaling down the StatefulSet")
	sts.Spec.Replicas = new(int32(0))

	logger.Info("Updating the tolerations to be added to the stateful set")
	currentTOLERATIONS := sts.Spec.Template.Spec.Tolerations
	tolerationSTRING := os.Getenv("TOLERATIONS")
	if tolerationSTRING == "" {
		logger.Error(nil, "TOLERATIONS environment variable is not set")
		return fmt.Errorf("TOLERATIONS environment variable is not set")
	}
	tolerationOBJECTS, err := GetTolerationObject(tolerationSTRING)
	if err != nil {
		return err
	}
	currentTOLERATIONS = append(currentTOLERATIONS, tolerationOBJECTS...)
	sts.Spec.Template.Spec.Tolerations = currentTOLERATIONS
	err = r.Update(ctx, sts)
	return err
}

// UpdateVolumeAnnotations adds migration annotations to PVCs that use old storage classes.
// It iterates through all volumes in the StatefulSet template, identifies PVCs using
// old storage classes (from OLD_STORAGE_CLASSES environment variable), and adds the
// migration annotation if not already present.
// Returns an error if OLD_STORAGE_CLASSES is not set or PVC update fails.
func (r *StsController) UpdateVolumeAnnotations(ctx context.Context, sts *v1.StatefulSet) error {
	logger := log.FromContext(ctx)
	storageClassNames := os.Getenv("OLD_STORAGE_CLASSES")
	if storageClassNames == "" {
		logger.Error(nil, "OLD_STORAGE_CLASSES environment variable is not set")
		return fmt.Errorf("OLD_STORAGE_CLASSES environment variable is not set")
	}
	// Further logic to update volume annotations based on storageClassNames
	logger.Info("Updating the volume annotations")
	for _, volume := range sts.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			pvcObjectKey := client.ObjectKey{Name: volume.PersistentVolumeClaim.ClaimName, Namespace: sts.Namespace}
			var pvc corev1.PersistentVolumeClaim
			if err := r.APIReader.Get(ctx, pvcObjectKey, &pvc); err != nil {
				return err
			}
			for scName := range strings.SplitSeq(storageClassNames, ",") {
				if *pvc.Spec.StorageClassName == scName {
					if _, exists := pvc.Annotations[AnnotationKEY]; !exists {
						logger.Info("Adding annotation to PVC", "pvc", pvcObjectKey)
						PVCAnnotationUpdater := NewSTSPVCAnnotationUpdater(r, volume.PersistentVolumeClaim.ClaimName, sts.Namespace)
						if err := PVCAnnotationUpdater.EnableVolumeClaimAnnotation(ctx); err != nil {
							return err
						}
						break
					}
				}
			}
		}
	}
	return nil
}

// WaitForVolumeMigration checks if all volumes have completed migration.
// Returns an error with the "UNDER MIGRATION" prefix if volumes are still being migrated,
// which signals the reconciler to requeue the request.
// Returns nil when all volumes have been successfully migrated.
func (r *StsController) WaitForVolumeMigration(ctx context.Context, sts *v1.StatefulSet) error {
	logger := log.FromContext(ctx)
	if r.HasUnMigratedVolumes(ctx, sts) {
		logger.Info("Volumes are still being migrated")
		return fmt.Errorf("UNDER MIGRATION: volumes are still being migrated")
	}
	logger.Info("DONE: All volumes are migrated and updating the annotation to be enabled back")
	return nil
}

// ScaleUpSTS restores the StatefulSet to its original replica count after migration.
// It reads the replica count from the ReplicaAnnotationKey annotation that was set
// during the scale-down phase and updates the StatefulSet spec.
// Returns an error if the annotation value is invalid or update fails.
func (r *StsController) ScaleUpSTS(ctx context.Context, sts *v1.StatefulSet) error {
	logger := log.FromContext(ctx)
	logger.Info("Scaling up the StatefulSet")
	oldReplica := sts.Annotations[ReplicaAnnotationKey]
	replicaCount, err := strconv.Atoi(oldReplica)
	if err != nil {
		logger.Error(err, "Error converting old replica count to integer")
		return err
	}
	sts.Spec.Replicas = new(int32(replicaCount))
	return r.Update(ctx, sts)
}

// Reconcile implements the reconciliation loop for StatefulSet volume migration.
// It uses a state machine driven by the AnnotationKEY annotation with these states:
//
// 1. Enabled: Scale down the StatefulSet to 0 replicas and add tolerations
// 2. Progress: Add migration annotations to all PVCs using old storage classes
// 3. MigrationInProgress: Poll every 60 seconds until all volumes are migrated
// 4. Migrated: Scale the StatefulSet back to original replica count
//
// The reconciler automatically requeues after 60 seconds on errors or during migration.
// State transitions are tracked by updating the AnnotationKEY annotation value.
func (r *StsController) Reconcile(ctx context.Context, request reconcile.Request) (result reconcile.Result, err error) {
	logger := log.FromContext(ctx)
	var message string
	var annotationValue string
	var sts *v1.StatefulSet

	sts, err = r.GetStatefulSet(ctx, request.Name, request.Namespace)
	if err != nil {
		goto ReturnResult
	}

	switch sts.Annotations[AnnotationKEY] {
	case Enabled:
		logger.Info("Scaling down the StatefulSet")
		err = r.ScaleDownSTS(ctx, sts)
		if err != nil {
			message = "Error scaling down the StatefulSet"
			goto ReturnResult
		}
		message = "Scaled down the StatefulSet"
		annotationValue = Progress
		goto ReturnResult
	case Progress:
		logger.Info("Updating annotations for volumes of the StatefulSet")
		err = r.UpdateVolumeAnnotations(ctx, sts)
		if err != nil {
			message = "Error updating annotations for volumes of the StatefulSet"
			goto ReturnResult
		}
		message = "Updated annotations for volumes of the StatefulSet"
		annotationValue = MigrationInProgress
		goto ReturnResult
	case MigrationInProgress:
		logger.Info("Waiting for the Volumes to be migrated")
		err = r.WaitForVolumeMigration(ctx, sts)
		if err != nil {
			result.RequeueAfter = 60 * time.Second
			annotationValue = MigrationInProgress
			message = "StatefulSet reconciled after 1 minute"
		} else {
			message = "StatefulSet reconciled"
			annotationValue = Migrated
		}
		goto ReturnResult
	case Migrated:
		logger.Info("Scaling up the StatefulSet")
		err = r.ScaleUpSTS(ctx, sts)
		if err != nil {
			message = "Error scaling up the StatefulSet"
			result.RequeueAfter = 60 * time.Second
		} else {
			annotationValue = Enabled
			message = "StatefulSet scaled up"
		}
		goto ReturnResult
	}

ReturnResult:
	logger.Info(message)
	if err != nil {
		result.RequeueAfter = 60 * time.Second
	}
	if annotationValue != "" {
		sts.Annotations[AnnotationKEY] = annotationValue
		err = r.Update(ctx, sts)
	}
	return result, err
}

// SetupWithManager registers the StatefulSet controller with the manager.
// It watches StatefulSet resources and delegates reconciliation to r.
func (r *StsController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.StatefulSet{}).
		WithEventFilter(StsPredicate()).
		Complete(r)
}
