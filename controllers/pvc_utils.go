package controllers

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

const PVCAnnotationKey = "gke.gen4.np.codeamenity.in/pvc"

type PVCAnnotationUpdater struct {
	r         *DeploymentController
	name      string
	namespace string
}

func NewPVCAnnotationUpdater(r *DeploymentController, name, namespace string) *PVCAnnotationUpdater {
	return &PVCAnnotationUpdater{r, name, namespace}
}

func (u *PVCAnnotationUpdater) EnableVolumeClaimAnnotation(ctx context.Context) error {
	// Get the PVC object
	var pvc v1.PersistentVolumeClaim
	if err := u.r.APIReader.Get(ctx, client.ObjectKey{Name: u.name, Namespace: u.namespace}, &pvc); err != nil {
		return err
	}

	// Add the annotation to the PVC
	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string)
	}

	if _, exists := pvc.Annotations[AnnotationKEY]; exists {
		return nil
	}

	pvc.Annotations[AnnotationKEY] = Enabled
	if err := u.r.Update(ctx, &pvc); err != nil {
		return err
	}

	return nil
}

type STSPVCAnnotationUpdater struct {
	r         *StsController
	name      string
	namespace string
}

func NewSTSPVCAnnotationUpdater(r *StsController, name, namespace string) *STSPVCAnnotationUpdater {
	return &STSPVCAnnotationUpdater{r, name, namespace}
}

func (u *STSPVCAnnotationUpdater) EnableVolumeClaimAnnotation(ctx context.Context) error {
	// Get the PVC object
	var pvc v1.PersistentVolumeClaim
	if err := u.r.APIReader.Get(ctx, client.ObjectKey{Name: u.name, Namespace: u.namespace}, &pvc); err != nil {
		return err
	}

	// Add the annotation to the PVC
	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string)
	}

	if _, exists := pvc.Annotations[AnnotationKEY]; exists {
		return nil
	}

	pvc.Annotations[AnnotationKEY] = Enabled
	if err := u.r.Update(ctx, &pvc); err != nil {
		return err
	}

	return nil
}

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
	}

	newSpec := oldPVCObject.Spec.DeepCopy()
	if slices.Contains(newPVC.Spec.AccessModes, v1.ReadWriteMany) {
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

func waitForPVCDeletion(ctx context.Context, r client.Reader, name, namespace string) (err error) {
	logger := log.FromContext(ctx)
	logger.Info("Waiting for PVC to be deleted", "pvc", name)
	timeout := time.After(15 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-timeout:
			err = fmt.Errorf("timeout waiting for PVC deletion after 15 minutes: %s", name)
			return err
		case <-ticker.C:
			err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, &v1.PersistentVolumeClaim{})
			if client.IgnoreNotFound(err) == nil {
				return nil
			}
		}
	}
}
