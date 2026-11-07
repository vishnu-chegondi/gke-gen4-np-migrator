# Reconciliation Workflow

The controller prepares supported Deployments and StatefulSets for scheduling on Gen4 node pools and migrates their persistent storage to Hyperdisk Balanced. Reconciliation performs the following work:

## 1. Update workload tolerations

The controller updates the tolerations on the Deployment and StatefulSet pod templates so their pods can be scheduled despite the taints applied to Gen4 node pools. This allows the workloads to run on those nodes when scheduling requirements permit.

## 2. Migrate persistent storage

For workloads with persistent volume claims (PVCs), the controller migrates storage to use the Hyperdisk Balanced storage class. The migration follows these steps:

1. **Prepare storage resources.** Create the storage class used for Hyperdisk Balanced volumes and the volume snapshot class used to snapshot existing volumes.
2. **Capture the existing volume.** Create a snapshot of the source persistent volume and wait for the snapshot to be created before proceeding.
3. **Replace the claim.** Delete the existing PVC after its snapshot is available.
4. **Restore onto the new storage class.** Create a PVC with the original name and requested size, using the snapshot as its data source and the Hyperdisk Balanced storage class.

The replacement claim preserves the name and requested capacity of the original PVC while restoring its data from the snapshot onto the new storage type.
