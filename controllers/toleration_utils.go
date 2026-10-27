package controllers

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func GetTolerationObject(yamlINPUT string) ([]corev1.Toleration, error) {
	tolerations := []corev1.Toleration{}
	err := yaml.Unmarshal([]byte(yamlINPUT), &tolerations)
	return tolerations, err
}
