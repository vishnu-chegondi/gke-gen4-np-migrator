package controllers

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// GetTolerationObject parses a YAML string and returns a slice of Kubernetes Toleration objects.
// It takes a YAML-formatted string as input and unmarshals it into a slice of corev1.Toleration.
// Returns the parsed tolerations and any error encountered during unmarshaling.
func GetTolerationObject(yamlINPUT string) ([]corev1.Toleration, error) {
	var tolerations []corev1.Toleration
	err := yaml.Unmarshal([]byte(yamlINPUT), &tolerations)
	return tolerations, err
}
