package controllers

import (
	"context"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type SnapshotController struct {
	client.Client
	Scheme *runtime.Scheme
}

var _ reconcile.Reconciler = &SnapshotController{}

func (r *SnapshotController) ShouldTriggerSnapshot(ctx context.Context, snapshot *snapshotv1.VolumeSnapshot) bool {
	logger := log.FromContext(ctx)
	if _, exits := snapshot.Annotations[AnnotationKEY]; !exits {
		logger.Info("Snapshot does not have annotation")
		return false
	}
	logger.Info("Snapshot is under migration")
	return true
}

func (r *SnapshotController) UpdateAnnotation(ctx context.Context, snapshot *snapshotv1.VolumeSnapshot, value string) error {
	snapshot.Annotations[AnnotationKEY] = value
	return r.Update(ctx, snapshot)
}

func (r *SnapshotController) Reconcile(ctx context.Context, request reconcile.Request) (result reconcile.Result, err error) {
	var snapshot snapshotv1.VolumeSnapshot
	var annotationValue string
	if err := r.Get(ctx, request.NamespacedName, &snapshot); err != nil {
		err = client.IgnoreNotFound(err)
	}

	if !r.ShouldTriggerSnapshot(ctx, &snapshot) {
		goto returnResult
	}

	switch snapshot.Annotations[AnnotationKEY] {
	case "enabled":
		for !SnapshotAvailable(&snapshot) {
			time.Sleep(60 * time.Second)
		}
		annotationValue = "available"
		goto returnResult
	case "available":
		err = r.CreateNewPVC(ctx, &snapshot)
		annotationValue = "pvc-created"
		goto returnResult
	case "pvc-created":
		return result, err
	}

returnResult:
	err = r.UpdateAnnotation(ctx, &snapshot, annotationValue)
	return result, err
}

func (r *SnapshotController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&snapshotv1.VolumeSnapshot{}).
		Complete(r)
}
