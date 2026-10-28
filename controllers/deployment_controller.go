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
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	AnnotationKEY        = "gke.gen4.np.codeamenity.in/migrate"
	ReplicaAnnotationKey = "gke.gen4.np.codeamenity.in/replica"
	Enabled              = "enabled"
	MigrationInProgress  = "migration-in-progress"
	Migrated             = "migrated"
	Progress             = "progress"
)

// DeploymentController reconciles Deployment objects to manage storage class migrations
// for persistent volumes. It handles the complete migration lifecycle including:
// - Detecting deployments with volumes using old storage classes
// - Scaling down deployments during migration
// - Updating PVC annotations to trigger volume migration
// - Waiting for volume migrations to complete
// - Scaling deployments back up after successful migration
//
// The controller uses annotations to track migration state and coordinates with
// PersistentVolumeClaim and VolumeSnapshot controllers to ensure safe data migration.
type DeploymentController struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
}

var _ reconcile.Reconciler = &DeploymentController{}

// GetDeployment retrieves a Deployment resource from the Kubernetes API server using the API reader.
// It bypasses the cache to ensure the most up-to-date deployment state is returned.
//
// Parameters:
//   - ctx: Context for the API call, used for cancellation and deadline management
//   - name: The name of the Deployment resource to retrieve
//   - namespace: The namespace where the Deployment is located
//
// Returns:
//   - *v1.Deployment: The retrieved Deployment object, or nil if an error occurred
//   - error: An error if the deployment cannot be retrieved (e.g., not found, API error)
func (r *DeploymentController) GetDeployment(ctx context.Context, name, namespace string) (*v1.Deployment, error) {
	deployment := &v1.Deployment{}
	err := r.APIReader.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, deployment)
	return deployment, err
}

// HasUnMigratedVolumes checks if a deployment has any PersistentVolumeClaims that need migration.
// It examines all volumes in the deployment's pod template spec and determines if any PVCs are:
// 1. Using one of the old storage classes (defined in OLD_STORAGE_CLASSES env var)
// 2. Not yet marked with the migration enabled annotation
// 3. Non-existent (returns true to trigger migration flow)
//
// The method reads the OLD_STORAGE_CLASSES environment variable which should contain a
// comma-separated list of storage class names that require migration.
//
// Parameters:
//   - ctx: Context for logging and API calls
//   - deployment: The Deployment object to check for unmigrated volumes
//
// Returns:
//   - bool: true if any volumes need migration or don't exist, false if all volumes are migrated
//     or if OLD_STORAGE_CLASSES is not configured
func (r *DeploymentController) HasUnMigratedVolumes(ctx context.Context, deployment *v1.Deployment) bool {
	logger := log.FromContext(ctx)
	oldStorageClasses := os.Getenv("OLD_STORAGE_CLASSES")
	if oldStorageClasses == "" {
		logger.Error(nil, "OLD_STORAGE_CLASSES environment variable is not set")
		return false
	}
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			var pvc corev1.PersistentVolumeClaim
			pvcNamespacedName := client.ObjectKey{Name: volume.PersistentVolumeClaim.ClaimName, Namespace: deployment.Namespace}
			if err := r.APIReader.Get(ctx, pvcNamespacedName, &pvc); err != nil {
				logger.Info("PVC does not exist", "pvc", volume.PersistentVolumeClaim.ClaimName)
				return true
			}
			for scName := range strings.SplitSeq(oldStorageClasses, ",") {
				if *pvc.Spec.StorageClassName == scName && pvc.Annotations[AnnotationKEY] != Enabled {
					logger.Info("PVC is in the old storage class", "pvc", volume.PersistentVolumeClaim.ClaimName)
					return true
				}
			}
		}
	}
	return false
}

// ScaleDownDeployment scales a deployment to zero replicas in preparation for volume migration.
// This method performs three key operations:
// 1. Saves the current replica count in an annotation (ReplicaAnnotationKey) if it's greater than 0
// 2. Sets the deployment replica count to 0
// 3. Adds migration-specific tolerations from the TOLERATIONS environment variable to the pod template
//
// The tolerations are added to ensure that when pods are recreated after migration, they can be
// scheduled appropriately. The original replica count is preserved so it can be restored after
// migration completes.
//
// Parameters:
//   - ctx: Context for logging and API calls
//   - deployment: The Deployment object to scale down (will be modified in-place)
//
// Returns:
//   - error: An error if the TOLERATIONS environment variable is not set, toleration parsing fails,
//     or the deployment update fails
func (r *DeploymentController) ScaleDownDeployment(ctx context.Context, deployment *v1.Deployment) error {
	logger := log.FromContext(ctx)

	logger.Info("Updating the replicated annotation if replica count is greater than 0")
	if *deployment.Spec.Replicas != 0 {
		deployment.Annotations[ReplicaAnnotationKey] = fmt.Sprintf("%d", *deployment.Spec.Replicas)
	}

	logger.Info("Scaling down the deployment")
	deployment.Spec.Replicas = new(int32)

	logger.Info("Updating the tolerations to be added to the deployment")
	currentTOLERATIONS := deployment.Spec.Template.Spec.Tolerations
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
	deployment.Spec.Template.Spec.Tolerations = currentTOLERATIONS
	err = r.Update(ctx, deployment)
	return err
}

// ScaleUpDeployment restores a deployment to its original replica count after migration completes.
// It reads the replica count that was saved in the ReplicaAnnotationKey annotation during scale-down
// and restores the deployment to that replica count.
//
// This method is called after all volume migrations have completed successfully to bring the
// deployment back to its normal operational state.
//
// Parameters:
//   - ctx: Context for logging and API calls
//   - deployment: The Deployment object to scale up (will be modified in-place)
//
// Returns:
//   - error: An error if the replica annotation is missing, contains an invalid integer value,
//     or the deployment update fails
func (r *DeploymentController) ScaleUpDeployment(ctx context.Context, deployment *v1.Deployment) error {
	logger := log.FromContext(ctx)
	logger.Info("Scaling up the deployment")
	oldReplica := deployment.Annotations[ReplicaAnnotationKey]
	replicaCount, err := strconv.Atoi(oldReplica)
	if err != nil {
		logger.Error(err, "Error converting old replica count to integer")
		return err
	}
	deployment.Spec.Replicas = new(int32(replicaCount))
	return r.Update(ctx, deployment)
}

// UpdateVolumeAnnotations marks all eligible PersistentVolumeClaims for migration by adding annotations.
// It iterates through all volumes in the deployment's pod template and for each PVC that:
// 1. Uses one of the old storage classes (from OLD_STORAGE_CLASSES env var)
// 2. Doesn't already have the migration annotation
//
// The method adds the migration annotation (AnnotationKEY) with value "enabled" to trigger
// the PVC migration process. This is handled by the PVCAnnotationUpdater helper.
//
// Parameters:
//   - ctx: Context for logging and API calls
//   - deployment: The Deployment object whose volumes should be annotated for migration
//
// Returns:
//   - error: An error if OLD_STORAGE_CLASSES is not set, PVC retrieval fails,
//     or annotation update fails for any PVC
func (r *DeploymentController) UpdateVolumeAnnotations(ctx context.Context, deployment *v1.Deployment) error {
	logger := log.FromContext(ctx)
	storageClassNames := os.Getenv("OLD_STORAGE_CLASSES")
	if storageClassNames == "" {
		logger.Error(nil, "OLD_STORAGE_CLASSES environment variable is not set")
		return fmt.Errorf("OLD_STORAGE_CLASSES environment variable is not set")
	}

	logger.Info("Updating the volume annotations")
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			pvcObjectKey := client.ObjectKey{Name: volume.PersistentVolumeClaim.ClaimName, Namespace: deployment.Namespace}
			var pvc corev1.PersistentVolumeClaim
			if err := r.APIReader.Get(ctx, pvcObjectKey, &pvc); err != nil {
				return err
			}
			for scName := range strings.SplitSeq(storageClassNames, ",") {
				if *pvc.Spec.StorageClassName == scName {
					if _, exists := pvc.Annotations[AnnotationKEY]; !exists {
						logger.Info("Adding annotation to PVC", "pvc", pvcObjectKey)
						PVCAnnotationUpdater := NewPVCAnnotationUpdater(r, volume.PersistentVolumeClaim.ClaimName, deployment.Namespace)
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

// WaitForVolumeMigration checks if all volumes for a deployment have completed migration.
// This method is called during the "migration-in-progress" state to poll for completion.
// It uses HasUnMigratedVolumes to determine if any volumes still need migration.
//
// If any volumes are still being migrated, it returns an error to trigger a requeue.
// Once all volumes are migrated, it returns nil to allow the state machine to proceed
// to the final scale-up phase.
//
// Parameters:
//   - ctx: Context for logging and API calls
//   - deployment: The Deployment object to check for migration completion
//
// Returns:
//   - error: An error with message "UNDER MIGRATION" if volumes are still migrating,
//     nil if all volumes have completed migration
func (r *DeploymentController) WaitForVolumeMigration(ctx context.Context, deployment *v1.Deployment) error {
	logger := log.FromContext(ctx)
	if r.HasUnMigratedVolumes(ctx, deployment) {
		logger.Info("Volumes are still being migrated")
		return fmt.Errorf("UNDER MIGRATION: volumes are still being migrated")
	}
	logger.Info("DONE: All volumes are migrated and updating the annotation to be enabled back")
	return nil
}

// Reconcile implements the main reconciliation loop for the DeploymentController.
// It manages the complete lifecycle of storage class migration for deployment volumes through
// a state machine controlled by the AnnotationKEY annotation.
//
// Migration State Machine:
//   - "enabled": Initial state - scales down deployment and transitions to "progress"
//   - "progress": Annotates PVCs for migration and transitions to "migrated"
//   - "migration-in-progress": Polls for migration completion, stays in same state until done
//   - "migrated": Scales deployment back up and transitions to "enabled"
//
// The controller only acts on deployments that:
//   - Have unmigrated volumes (checked via HasUnMigratedVolumes), OR
//   - Are already in the migration process (state is "migrated" or "migration-in-progress")
//
// Reconciliation Behavior:
//   - On errors: Requeues after 60 seconds
//   - On success: Updates deployment annotation to next state
//   - State transitions are persisted via deployment updates
//   - Uses goto-based control flow for centralized error handling
//
// Parameters:
//   - ctx: Context for the reconciliation request
//   - request: Contains the namespace and name of the deployment to reconcile
//
// Returns:
//   - result: Reconcile result indicating if/when to requeue
//   - err: An error if reconciliation failed
func (r *DeploymentController) Reconcile(
	ctx context.Context,
	request reconcile.Request,
) (result reconcile.Result, err error) {
	var deployment *v1.Deployment
	logger := log.FromContext(ctx)
	var message string
	var annotationValue string
	deployment, err = r.GetDeployment(ctx, request.Name, request.Namespace)
	if err != nil {
		goto returnResult
	}

	if !r.HasUnMigratedVolumes(ctx, deployment) &&
		(deployment.Annotations[AnnotationKEY] != Migrated &&
			deployment.Annotations[AnnotationKEY] != MigrationInProgress) {
		goto returnResult
	}

	switch deployment.Annotations[AnnotationKEY] {
	case Enabled:
		err = r.ScaleDownDeployment(ctx, deployment)
		if err != nil {
			message = "Error scaling down the deployment"
			result.RequeueAfter = 60 * time.Second
			goto returnResult
		}
		message = "Deployment scaled down"
		annotationValue = Progress
		goto returnResult
	case Progress:
		err = r.UpdateVolumeAnnotations(ctx, deployment)
		if err != nil {
			message = "Error updating volume annotations"
			result.RequeueAfter = 60 * time.Second
			goto returnResult
		}
		annotationValue = Migrated
		message = "Volume annotations updated"
		goto returnResult
	case MigrationInProgress:
		err = r.WaitForVolumeMigration(ctx, deployment)
		if err != nil {
			result.RequeueAfter = 60 * time.Second
			annotationValue = MigrationInProgress
			message = "Deployment reconciled after 1 minute"
		} else {
			message = "Deployment reconciled"
			annotationValue = Migrated
		}
		goto returnResult
	case Migrated:
		err = r.ScaleUpDeployment(ctx, deployment)
		if err != nil {
			message = "Error scaling up the deployment"
			result.RequeueAfter = 60 * time.Second
		} else {
			annotationValue = Enabled
			message = "Deployment scaled up"
		}
		goto returnResult
	}

returnResult:
	logger.Info(message)
	deployment, err = r.GetDeployment(ctx, request.Name, request.Namespace)
	if err == nil {
		if annotationValue != "" {
			deployment.Annotations[AnnotationKEY] = annotationValue
		}
		err = r.Update(ctx, deployment)
	}
	if err != nil {
		result.RequeueAfter = 60 * time.Second
	}
	return result, err
}

// SetupWithManager configures the DeploymentController with the controller manager.
// It sets up the controller to watch Deployment resources with the following configuration:
//
// - Primary resource: v1.Deployment objects
// - Concurrency: Up to 3 concurrent reconciliation loops
// - Event filtering: Uses DeploymentPredicate() to filter relevant deployment events
//
// Parameters:
//   - mgr: The controller manager that will manage this controller's lifecycle
//
// Returns:
//   - error: An error if the controller setup fails, nil otherwise
func (r *DeploymentController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Deployment{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 3}).
		WithEventFilter(DeploymentPredicate()).
		Complete(r)
}
