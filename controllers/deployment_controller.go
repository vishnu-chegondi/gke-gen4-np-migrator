package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var AnnotationKEY = "gke.gen4.np.codeamenity.in/migrate"
var ReplicaAnnotationKey = "gke.gen4.np.codeamenity.in/replica"

type DeploymentController struct {
	client.Client
	Scheme *runtime.Scheme
}

var _ reconcile.Reconciler = &DeploymentController{}

func (r *DeploymentController) GetDeployment(ctx context.Context, name, namespace string) (*v1.Deployment, error) {
	deployment := &v1.Deployment{}
	err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, deployment)
	return deployment, err
}

func (r *DeploymentController) HasUnMigratedVolumes(ctx context.Context, deployment *v1.Deployment) bool {
	logger := log.FromContext(ctx)
	storageClassNames, err := ReadConfigMapKey(ctx, r, "OLD_STORAGE_CLASSES")
	if err != nil {
		logger.Error(err, "Failed to read OLD_STORAGE_CLASSES from ConfigMap")
		return false
	}
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			var pvc corev1.PersistentVolumeClaim
			if err := r.Get(ctx, client.ObjectKey{Name: volume.PersistentVolumeClaim.ClaimName, Namespace: deployment.Namespace}, &pvc); err != nil {
				logger.Info("PVC does not exist", "pvc", volume.PersistentVolumeClaim.ClaimName)
				continue // Continue to check next volume
			}
			for _, scName := range strings.Split(storageClassNames, ",") {
				if *pvc.Spec.StorageClassName == scName && pvc.Annotations[AnnotationKEY] != "enabled" {
					logger.Info("PVC is in the old storage class", "pvc", volume.PersistentVolumeClaim.ClaimName)
					return true
				}
			}
		}
	}
	return false
}

func (r *DeploymentController) ShouldTriggerDeployment(ctx context.Context, deployment *v1.Deployment) bool {
	logger := log.FromContext(ctx)
	annotations, exists := deployment.Annotations[AnnotationKEY]
	if !exists {
		logger.Info("Deployment does not have annotation")
		return false
	}
	if annotations == "migrated" {
		logger.Info("Deployment is under migration")
		return true
	}
	return r.HasUnMigratedVolumes(ctx, deployment)
}

func (r *DeploymentController) ScaleDownDeployment(ctx context.Context, deployment *v1.Deployment) error {
	logger := log.FromContext(ctx)
	logger.Info("Scaling down the deployment)")
	deployment.Spec.Replicas = pointer.Int32(0)
	logger.Info("Updating the tolerations to be added to the deployment")
	currentTOLERATIONS := deployment.Spec.Template.Spec.Tolerations
	data, err := ReadConfigMapData(ctx, r)
	if err != nil {
		return err
	}
	tolerationSTRING := data["TOLERATIONS"]
	tolerationOBJECTS, err := GetTolerationObject(tolerationSTRING)
	if err != nil {
		return err
	}
	for _, toleration := range tolerationOBJECTS {
		currentTOLERATIONS = append(currentTOLERATIONS, toleration)
	}
	deployment.Spec.Template.Spec.Tolerations = currentTOLERATIONS
	logger.Info("Updating the annotation to be in progress")
	deployment.Annotations[AnnotationKEY] = "progress"
	deployment.Annotations[ReplicaAnnotationKey] = fmt.Sprintf("%d", *deployment.Spec.Replicas)
	err = r.Update(ctx, deployment)
	return err
}

func (r *DeploymentController) UpdateVolumeAnnotations(ctx context.Context, deployment *v1.Deployment) error {
	logger := log.FromContext(ctx)
	storageClassNames, err := ReadConfigMapKey(ctx, r, "OLD_STORAGE_CLASSES")
	if err != nil {
		return err
	}

	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			pvcObjectKey := client.ObjectKey{Name: volume.PersistentVolumeClaim.ClaimName, Namespace: deployment.Namespace}
			var pvc corev1.PersistentVolumeClaim
			if err := r.Get(ctx, pvcObjectKey, &pvc); err != nil {
				return err
			}
			for _, scName := range strings.Split(storageClassNames, ",") {
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
	deployment.Annotations[AnnotationKEY] = "migrated"
	err = r.Update(ctx, deployment)
	return err
}

func (r *DeploymentController) WaitForVolumeMigration(ctx context.Context, deployment *v1.Deployment) error {
	logger := log.FromContext(ctx)
	if r.HasUnMigratedVolumes(ctx, deployment) {
		logger.Info("Volumes are still being migrated")
		return fmt.Errorf("UNDER MIGRATION: volumes are still being migrated")
	}
	logger.Info("DONE: All volumes are migrated and updating the annotation to be enabled back")
	deployment.Annotations[AnnotationKEY] = "enabled"
	err := r.Update(ctx, deployment)
	return err
}

func (r *DeploymentController) Reconcile(ctx context.Context, request reconcile.Request) (result reconcile.Result, err error) {
	var deployment *v1.Deployment
	logger := log.FromContext(ctx)
	var message string
	deployment, err = r.GetDeployment(ctx, request.Name, request.Namespace)
	if err != nil {
		goto returnResult
	}

	if !r.ShouldTriggerDeployment(ctx, deployment) {
		goto returnResult
	}

	switch deployment.Annotations[AnnotationKEY] {
	case "enabled":
		deployment, err = r.GetDeployment(ctx, request.Name, request.Namespace)
		err = r.ScaleDownDeployment(ctx, deployment)
		message = "Deployment scaled down"
		goto returnResult
	case "progress":
		err = r.UpdateVolumeAnnotations(ctx, deployment)
		message = "Volume annotations updated"
		goto returnResult
	case "migrated":
		err = r.WaitForVolumeMigration(ctx, deployment)
		message = "Deployment reconciled"
		if err != nil {
			result.RequeueAfter = 60 * time.Second
			err = nil
			message = "Deployment reconciled after 1 minute"
		}
		goto returnResult
	}

returnResult:
	logger.Info(message)
	return result, err
}

func (r *DeploymentController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Deployment{}).
		Complete(r)
}
