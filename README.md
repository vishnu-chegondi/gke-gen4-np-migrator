# gke-gen4-np-migrator

It is a Kubernetes operator to migrate Deployments and StatefulSets to GKE Gen4 NodePools.

## Description

On November 2025, Google Cloud announced the availability of Gen4 Node Pools. This operator is designed to migrate Deployments and StatefulSets to Gen4 Node Pools. Major challenges are:

`Gen4` machines do not support pd-ssd, pd-balanced, pd-extreme, which are the default storage classes for persistent volumes. The operator will create a new storage class for hyper disk balanced storage class and migrate the persistent volumes to use this new storage type hyper-disk balanced storage class.

## Workflow

- Update the tolerations of the Deployment and StatefulSet to ignore the taints of Gen4 Node Pools.
- Migrate the persistent volumes of the Deployment and StatefulSet to use hyper disk balanced storage classes.
    - Creates a storage class for the hyper disk balanced storage class.
    - Creates a volume snapshot class to take snapshots of the persistent volumes.
    - Delete the old PVC once the snapshot is created.
    - Create new PVCs with the same name, size and hyper disk balanced storage class using the snapshot.


## Installation

> TODO: Update the installation instructions.


