# gke-gen4-np-migrator

It is a Kubernetes operator to migrate Deployments and StatefulSets to GKE Gen4 NodePools.

## Description

On November 2025, Google Cloud announced the availability of Gen4 Node Pools. This operator is designed to migrate Deployments and StatefulSets to Gen4 Node Pools. Major challenges are:

`Gen4` machines do not support pd-ssd, pd-balanced, pd-extreme, which are the default storage classes for persistent volumes. The operator will create a new storage class for hyper disk balanced storage class and migrate the persistent volumes to use this new storage type hyper-disk balanced storage class.

## Installation

Install the operator with Helm:

```bash
helm install gke-gen4-np-migrator oci://ghcr.io/vishnu-chegondi/charts/gke-gen4-np-migrator \
 --namespace gke-gen4-np-migrator-system \
 --create-namespace --version 1.0.0
```

To install a specific chart version, append `--version <version>` to the command. Verify that the operator is running with:


```bash
kubectl get pods -n gke-gen4-np-migrator-system
```

## Under the hood

Please refer to the [docs/reconcile-flow.md](docs/reconcile-flow.md) for the detailed flow of the operator.
