package controllers

import (
	"context"
	"fmt"

	v1 "k8s.io/api/apps/v1"
)

type DeploymentAnnotationUpdater struct {
	r          *DeploymentController
	deployment *v1.Deployment
}

func (u *DeploymentAnnotationUpdater) Progress(ctx context.Context) error {
	value, exists := u.deployment.Annotations[AnnotationKEY]
	if !exists {
		return fmt.Errorf("deployment %s/%s does not have the annotation %s", u.deployment.Namespace, u.deployment.Name, AnnotationKEY)
	}

	if value != "enabled" {
		return nil
	}

	u.deployment.Annotations[AnnotationKEY] = "progress"
	if err := u.r.Update(ctx, u.deployment); err != nil {
		return err
	}

	return nil
}

func (u *DeploymentAnnotationUpdater) Waiting(ctx context.Context) error {
	value, exists := u.deployment.Annotations[AnnotationKEY]
	if !exists {
		return fmt.Errorf("deployment %s/%s does not have the annotation %s", u.deployment.Namespace, u.deployment.Name, AnnotationKEY)
	}

	if value != "progress" {
		return nil
	}

	u.deployment.Annotations[AnnotationKEY] = "waiting"
	if err := u.r.Update(ctx, u.deployment); err != nil {
		return err
	}
	return nil
}

func (u *DeploymentAnnotationUpdater) Done(ctx context.Context) error {
	delete(u.deployment.Annotations, ReplicaAnnotationKey)
	u.deployment.Annotations[AnnotationKEY] = "enabled"
	if err := u.r.Update(ctx, u.deployment); err != nil {
		return err
	}
	return nil
}

func (u *DeploymentAnnotationUpdater) ReplicaCount(ctx context.Context) error {
	_, exists := u.deployment.Annotations[ReplicaAnnotationKey]
	if exists {
		return nil
	}
	u.deployment.Annotations[ReplicaAnnotationKey] = fmt.Sprintf("%d", *u.deployment.Spec.Replicas)
	if err := u.r.Update(ctx, u.deployment); err != nil {
		return err
	}
	return nil
}
