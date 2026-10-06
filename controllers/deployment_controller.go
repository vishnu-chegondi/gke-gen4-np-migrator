package controllers

// TODO: Update the standard-rwo storage class to use the provided storage classes

import (
	"context"
	"fmt"
	"time"

	"k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var AnnotationKEY = "gke.gen4.np.codeamenity.in/migrate"
var ReplicaAnnotationKey = "gke.gen4.np.codeamenity.in/replica"

type DeploymentController struct {
	client.Client
	Scheme *runtime.Scheme
}

var _ reconcile.Reconciler = &DeploymentController{}

func (r *DeploymentController) InitializeDeploymentController(ctx context.Context, deployment *v1.Deployment) bool {
	_, exists := deployment.Annotations[AnnotationKEY]
	if !exists {
		return false
	}

	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			var pvc corev1.PersistentVolumeClaim
			if err := r.Get(ctx, client.ObjectKey{Name: volume.PersistentVolumeClaim.ClaimName, Namespace: deployment.Namespace}, &pvc); err != nil {
				return true // If we can't get the PVC, assume it's in the migration process and return true
			}
			if *pvc.Spec.StorageClassName == "standard-rwo" {
				return true
			}
		}
	}

	return false
}

func (r *DeploymentController) UpdateDeploymentVolumeAnnotation(ctx context.Context, deployment *v1.Deployment) error {
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {

			var pvc corev1.PersistentVolumeClaim
			if err := r.Get(ctx, client.ObjectKey{Name: volume.PersistentVolumeClaim.ClaimName, Namespace: deployment.Namespace}, &pvc); err != nil {
				return err
			}
			if *pvc.Spec.StorageClassName != "standard-rwo" {
				continue
			}

			PVCAnnotationUpdater := NewPVCAnnotationUpdater(r, volume.PersistentVolumeClaim.ClaimName, deployment.Namespace)
			if err := PVCAnnotationUpdater.EnableVolumeClaimAnnotation(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *DeploymentController) ReconcileDeployment(ctx context.Context, deployment *v1.Deployment, annotationUpdater *DeploymentAnnotationUpdater) error {
	if err := annotationUpdater.ReplicaCount(ctx); err != nil {
		return err
	}

	deployment.Spec.Replicas = pointer.Int32(0)
	// TODO: Update new tolerations and remove old tolerations for deployments
	if err := r.Update(ctx, deployment); err != nil {
		return err
	}

	return nil

}

func (r *DeploymentController) WaitForVolumeMigration(ctx context.Context, deployment *v1.Deployment) error {
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			var pvc corev1.PersistentVolumeClaim
			if err := r.Get(ctx, client.ObjectKey{Name: volume.PersistentVolumeClaim.ClaimName, Namespace: deployment.Namespace}, &pvc); err != nil {
				return err // If we can't get the PVC, assume it's in the migration process and return error to retry loop
			}
			if *pvc.Spec.StorageClassName == "standard-rwo" {
				return fmt.Errorf("PVC %s/%s is still using the standard-rwo storage class", pvc.Namespace, pvc.Name)
			}
		}
	}
	return nil
}

func (r *DeploymentController) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	var deployment v1.Deployment
	if err := r.Get(ctx, request.NamespacedName, &deployment); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if !r.InitializeDeploymentController(ctx, &deployment) {
		return reconcile.Result{}, nil
	}

	// Define the deployment annotation updater
	deploymentAnnotationUpdater := &DeploymentAnnotationUpdater{
		r:          r,
		deployment: &deployment,
	}
	if err := deploymentAnnotationUpdater.Progress(ctx); err != nil {
		return reconcile.Result{}, err
	}

	// Scaledown the deployment and update the tolerations
	if err := r.ReconcileDeployment(ctx, &deployment, deploymentAnnotationUpdater); err != nil {
		return reconcile.Result{}, err
	}

	// Loop through all the volumes and for each PVC Claim volume, add the annotation
	if err := r.UpdateDeploymentVolumeAnnotation(ctx, &deployment); err != nil {
		return reconcile.Result{}, err
	}
	if err := deploymentAnnotationUpdater.Waiting(ctx); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.WaitForVolumeMigration(ctx, &deployment); err != nil {
		return reconcile.Result{RequeueAfter: 60 * time.Second}, nil
	}
	if err := deploymentAnnotationUpdater.Done(ctx); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *DeploymentController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Deployment{}).
		Complete(r)
}
