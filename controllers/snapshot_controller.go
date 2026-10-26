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

type SnapshotController struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
}

var _ reconcile.Reconciler = &SnapshotController{}

func (r *SnapshotController) UpdateAnnotation(ctx context.Context, snapshot *snapshotv1.VolumeSnapshot, value string) error {
	if value == "" {
		return nil
	}
	snapshot.Annotations[AnnotationKEY] = value
	return r.Update(ctx, snapshot)
}

func (r *SnapshotController) Reconcile(ctx context.Context, request reconcile.Request) (result reconcile.Result, err error) {
	logger := log.FromContext(ctx)
	var snapshot snapshotv1.VolumeSnapshot
	var annotationValue string
	if err := r.APIReader.Get(ctx, request.NamespacedName, &snapshot); err != nil {
		err = client.IgnoreNotFound(err)
	}

	switch snapshot.Annotations[AnnotationKEY] {
	case "enabled":
		for !SnapshotAvailable(&snapshot) {
			time.Sleep(60 * time.Second)
			if err := r.APIReader.Get(ctx, request.NamespacedName, &snapshot); err != nil {
				err = client.IgnoreNotFound(err)
			}
		}
		annotationValue = "available"
		goto returnResult
	case "available":
		err = r.CreateNewPVC(ctx, &snapshot)
		if err == nil {
			annotationValue = "pvc-created"
		}
		goto returnResult
	case "pvc-created":
		return result, err
	}

returnResult:
	if err != nil {
		logger.Error(err, "Error reconciling snapshot")
		err = nil
		result = reconcile.Result{RequeueAfter: 60 * time.Second}
	}
	err = r.UpdateAnnotation(ctx, &snapshot, annotationValue)
	return result, err
}

func (r *SnapshotController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1.VolumeSnapshot{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 3}).
		WithEventFilter(SnapshotPredicate()).
		Complete(r)
}
