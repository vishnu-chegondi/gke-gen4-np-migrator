// Package controllers provides Kubernetes controller implementations for managing
// PersistentVolumeClaims, Deployments, StatefulSets, and VolumeSnapshots.
package controllers

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

// PVCAnnotationKey is the annotation key used to store PVC metadata in VolumeSnapshot objects.
const PVCAnnotationKey = "gke.gen4.np.codeamenity.in/pvc"

func ensureAnnotations(obj metav1.Object) {
	if obj.GetAnnotations() == nil {
		obj.SetAnnotations(make(map[string]string))
	}
}

// enablePVCMigration requests migration for a PVC using only the client operations
// this behavior needs, keeping it independent from either workload controller.
func enablePVCMigration(ctx context.Context, reader client.Reader, writer client.Writer, key client.ObjectKey) error {
	var pvc v1.PersistentVolumeClaim
	if err := reader.Get(ctx, key, &pvc); err != nil {
		return err
	}
	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string)
	}
	if _, exists := pvc.Annotations[AnnotationKEY]; exists {
		return nil
	}
	pvc.Annotations[AnnotationKEY] = Enabled
	return writer.Update(ctx, &pvc)
}

// IsPVCINOLDStorageClass reports whether the named PVC should be treated as
// belonging to an old storage class. It checks the comma-separated
// OLD_STORAGE_CLASSES environment variable and ignores PVCs already marked as
// migration-enabled. It returns false if the variable is unset, and true if the
// PVC cannot be found.
func IsPVCINOLDStorageClass(ctx context.Context, apiReader client.Reader, pvcNamespacedName client.ObjectKey) bool {
	logger := log.FromContext(ctx)
	oldStorageClasses := os.Getenv("OLD_STORAGE_CLASSES")
	if oldStorageClasses == "" {
		logger.Error(nil, "OLD_STORAGE_CLASSES environment variable is not set")
		return false
	}
	var pvc v1.PersistentVolumeClaim
	if err := apiReader.Get(ctx, pvcNamespacedName, &pvc); err != nil {
		logger.Info("PVC does not exist", "pvc", pvcNamespacedName.Name)
		return true
	}
	if pvc.Spec.StorageClassName == nil {
		return false
	}
	for _, scName := range strings.Split(oldStorageClasses, ",") {
		if strings.TrimSpace(scName) == *pvc.Spec.StorageClassName && pvc.Annotations[AnnotationKEY] != Enabled {
			logger.Info("PVC is in the old storage class", "pvc", pvcNamespacedName.Name)
			return true
		}
	}
	return false
}

// CreateNewPVC creates a new PersistentVolumeClaim from a VolumeSnapshot.
// It restores the PVC metadata from the snapshot annotation, updates the storage class,
// sets the snapshot as the data source, and ensures minimum storage size of 10Gi.
// The new PVC is created with the migration annotation enabled.
func (r *SnapshotController) CreateNewPVC(ctx context.Context, snapshot *snapshotv1.VolumeSnapshot) (err error) {
	logger := log.FromContext(ctx)
	var oldPVCObject v1.PersistentVolumeClaim
	var newPVC v1.PersistentVolumeClaim
	newStorageClass := os.Getenv("STORAGE_CLASSNAME")
	if newStorageClass == "" {
		logger.Error(nil, "STORAGE_CLASSNAME environment variable is not set")
		return fmt.Errorf("STORAGE_CLASSNAME environment variable is not set")
	}

	// Check if PVC already exists
	err = yaml.Unmarshal([]byte(snapshot.Annotations[PVCAnnotationKey]), &oldPVCObject)
	if err != nil {
		return err
	}
	pvcNamespacedName := client.ObjectKey{Namespace: oldPVCObject.Namespace, Name: oldPVCObject.Name}
	if err := r.APIReader.Get(ctx, pvcNamespacedName, &v1.PersistentVolumeClaim{}); err == nil {
		return fmt.Errorf("PVC %s already exists", oldPVCObject.Name)
	} else if client.IgnoreNotFound(err) != nil {
		return err
	}

	newSpec := oldPVCObject.Spec.DeepCopy()
	if slices.Contains(newSpec.AccessModes, v1.ReadWriteMany) {
		newSpec.VolumeMode = new(v1.PersistentVolumeBlock)
	}
	newSpec.StorageClassName = new(newStorageClass)
	newSpec.DataSource = &v1.TypedLocalObjectReference{
		APIGroup: new("snapshot.storage.k8s.io"),
		Kind:     "VolumeSnapshot",
		Name:     snapshot.Name,
	}
	newSpec.VolumeName = ""

	if newSpec.Resources.Requests.Storage() == nil ||
		newSpec.Resources.Requests.Storage().Cmp(resource.MustParse("10Gi")) < 0 {
		newSpec.Resources.Requests[v1.ResourceStorage] = resource.MustParse("10Gi")
	}

	newPVCObjectMetadata := oldPVCObject.ObjectMeta.DeepCopy()
	newPVCObjectMetadata.ResourceVersion = ""
	newPVCObjectMetadata.UID = ""
	newPVCObjectMetadata.CreationTimestamp = metav1.Time{}
	if newPVCObjectMetadata.Annotations == nil {
		newPVCObjectMetadata.Annotations = make(map[string]string)
	}
	newPVCObjectMetadata.Annotations[AnnotationKEY] = Enabled
	delete(newPVCObjectMetadata.Annotations, "pv.kubernetes.io/bind-completed")
	delete(newPVCObjectMetadata.Annotations, "pv.kubernetes.io/bound-by-controller")
	delete(newPVCObjectMetadata.Annotations, "volume.beta.kubernetes.io/storage-provisioner")
	delete(newPVCObjectMetadata.Annotations, "volume.kubernetes.io/selected-node")
	delete(newPVCObjectMetadata.Annotations, "volume.kubernetes.io/storage-provisioner")

	newPVC = v1.PersistentVolumeClaim{
		TypeMeta:   oldPVCObject.TypeMeta,
		ObjectMeta: *newPVCObjectMetadata,
		Spec:       *newSpec,
	}

	logger.Info("Creating new PVC", "pvc", newPVC)
	err = r.Create(ctx, &newPVC)
	if err != nil {
		return err
	}
	return nil
}
