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

type DeploymentController struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
}

var _ reconcile.Reconciler = &DeploymentController{}

func (r *DeploymentController) GetDeployment(ctx context.Context, name, namespace string) (*v1.Deployment, error) {
	deployment := &v1.Deployment{}
	err := r.APIReader.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, deployment)
	return deployment, err
}

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

func (r *DeploymentController) WaitForVolumeMigration(ctx context.Context, deployment *v1.Deployment) error {
	logger := log.FromContext(ctx)
	if r.HasUnMigratedVolumes(ctx, deployment) {
		logger.Info("Volumes are still being migrated")
		return fmt.Errorf("UNDER MIGRATION: volumes are still being migrated")
	}
	logger.Info("DONE: All volumes are migrated and updating the annotation to be enabled back")
	return nil
}

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

func (r *DeploymentController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Deployment{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 3}).
		WithEventFilter(DeploymentPredicate()).
		Complete(r)
}
